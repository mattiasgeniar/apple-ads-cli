package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetExchangesTheAssertionAndSendsTheFieldsAppleWants(t *testing.T) {
	var got struct {
		grantType string
		clientID  string
		secret    string
		scope     string
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		got.grantType = r.PostForm.Get("grant_type")
		got.clientID = r.PostForm.Get("client_id")
		got.secret = r.PostForm.Get("client_secret")
		got.scope = r.PostForm.Get("scope")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok-abc","token_type":"Bearer","expires_in":3600,"scope":"searchadsorg"}`))
	}))
	defer server.Close()

	source := sourceAgainst(t, server)

	token, err := source.Get(context.Background())
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if token != "tok-abc" {
		t.Errorf("token = %q, want tok-abc", token)
	}
	if got.grantType != "client_credentials" {
		t.Errorf("grant_type = %q", got.grantType)
	}
	if got.scope != "searchadsorg" {
		t.Errorf("scope = %q, want searchadsorg", got.scope)
	}
	if got.clientID != source.Creds.ClientID {
		t.Errorf("client_id = %q", got.clientID)
	}
	if got.secret == "" {
		t.Error("client_secret was empty: the assertion is the credential")
	}
}

// An hour of validity means one exchange, not one per request. Getting this
// wrong makes a rate limit on the token endpoint look like a rate limit on the
// reporting API, which is a genuinely confusing place to start debugging.
func TestGetReusesATokenUntilItIsNearlyStale(t *testing.T) {
	exchanges := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		exchanges++
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
	}))
	defer server.Close()

	source := sourceAgainst(t, server)

	for range 5 {
		if _, err := source.Get(context.Background()); err != nil {
			t.Fatalf("get: %v", err)
		}
	}

	if exchanges != 1 {
		t.Errorf("exchanged %d times, want 1", exchanges)
	}
}

func TestGetMintsAgainOnceTheCachedTokenIsWithinAMinuteOfExpiring(t *testing.T) {
	exchanges := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		exchanges++
		_, _ = w.Write([]byte(`{"access_token":"tok","expires_in":3600}`))
	}))
	defer server.Close()

	source := sourceAgainst(t, server)

	if _, err := source.Get(context.Background()); err != nil {
		t.Fatalf("get: %v", err)
	}

	source.cached.ExpiresAt = time.Now().Add(30 * time.Second)

	if _, err := source.Get(context.Background()); err != nil {
		t.Fatalf("get: %v", err)
	}

	if exchanges != 2 {
		t.Errorf("exchanged %d times, want 2", exchanges)
	}
}

func TestGetReportsAppleErrorsWithAHintRatherThanJustInvalidClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer server.Close()

	source := sourceAgainst(t, server)

	_, err := source.Get(context.Background())
	if err == nil {
		t.Fatal("want an error")
	}
	if !contains(err.Error(), "invalid_client") || !contains(err.Error(), "hint") {
		t.Errorf("error should carry Apple's body and a hint, got: %v", err)
	}
}

func TestGetRejectsASuccessfulResponseThatCarriesNoToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"token_type":"Bearer"}`))
	}))
	defer server.Close()

	if _, err := sourceAgainst(t, server).Get(context.Background()); err == nil {
		t.Fatal("want an error when access_token is missing")
	}
}

// The token URL is a package constant, so the test server is reached by
// pointing the client's transport at it rather than by rewriting the URL.
func sourceAgainst(t *testing.T, server *httptest.Server) *Source {
	t.Helper()

	return &Source{
		Creds: testCreds(t),
		Client: &http.Client{
			Transport: rewrite{to: server.URL, inner: server.Client().Transport},
		},
	}
}

type rewrite struct {
	to    string
	inner http.RoundTripper
}

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	target, err := http.NewRequest(req.Method, r.to, req.Body)
	if err != nil {
		return nil, err
	}
	target.Header = req.Header

	return http.DefaultTransport.RoundTrip(target)
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}

		return false
	})()
}
