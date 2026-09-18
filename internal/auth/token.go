package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TokenURL is where the signed assertion is traded for a bearer token.
const TokenURL = Audience + "/auth/oauth2/token"

// Token is an access token and the moment it stops working.
type Token struct {
	AccessToken string
	ExpiresAt   time.Time
}

// Source hands out access tokens, minting a new one only when the last has
// nearly expired.
//
// Apple's tokens last an hour. A tool that exchanged on every call would burn
// a round trip per request and, worse, would make a rate limit on the token
// endpoint look like a rate limit on the reporting API.
type Source struct {
	Creds  Credentials
	Client *http.Client

	mu     sync.Mutex
	cached Token
}

// Get returns a valid token, reusing the cached one until it is nearly stale.
//
// The minute of headroom is not decoration: a token that passes the check here
// and expires in flight produces a 401 on a request that was correct, and that
// failure is intermittent enough to be genuinely hard to read.
func (s *Source) Get(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cached.AccessToken != "" && time.Until(s.cached.ExpiresAt) > time.Minute {
		return s.cached.AccessToken, nil
	}

	secret, err := ClientSecret(s.Creds, time.Hour)
	if err != nil {
		return "", err
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {s.Creds.ClientID},
		"client_secret": {secret},
		"scope":         {"searchadsorg"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Host", "appleid.apple.com")

	res, err := s.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", err
	}

	if res.StatusCode != http.StatusOK {
		// Apple answers `invalid_client` for every credential mistake there
		// is, so the body alone never says which one. The hint costs nothing
		// and saves the afternoon described in secret.go.
		return "", fmt.Errorf("token request: HTTP %d: %s\nhint: check that sub is the client id and iss is the team id, and that the public key is uploaded for this key id", res.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("token response carried no access_token: %s", strings.TrimSpace(string(body)))
	}

	s.cached = Token{
		AccessToken: payload.AccessToken,
		ExpiresAt:   time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second),
	}

	return s.cached.AccessToken, nil
}

func (s *Source) httpClient() *http.Client {
	if s.Client != nil {
		return s.Client
	}

	return &http.Client{Timeout: 30 * time.Second}
}
