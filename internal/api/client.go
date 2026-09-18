// Package api talks to the Apple Ads Platform API.
//
// Note the version. Apple shipped Platform API 1.0 in August 2026 and it
// supersedes Campaign Management API 5, which is sunset on 26 January 2027.
// Almost every example, SDK and blog post online targets v5, so anything that
// looks like prior art probably points at the wrong API.
//
//	v5  https://api.searchads.apple.com/api/v5/   X-AP-Context: orgId=...
//	1.0 https://api.ads.apple.com/v1/             X-AP-Context: adAccountId=...
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// BaseURL is the Platform API 1.0 root.
const BaseURL = "https://api.ads.apple.com/v1"

// TokenSource hands out a bearer token. Satisfied by auth.Source.
type TokenSource interface {
	Get(ctx context.Context) (string, error)
}

// Client is a thin, honest HTTP client for one ad account.
type Client struct {
	AdAccountID string
	Tokens      TokenSource
	HTTP        *http.Client
	BaseURL     string

	// Sleep exists so the backoff is testable without a test that actually
	// waits sixteen seconds.
	Sleep func(time.Duration)
}

// Post sends a JSON body and decodes a JSON response.
//
// Apple documents rate limits as response headers in the draft-polli style and
// asks callers to slow down *before* being throttled, so this reads
// RateLimit-Remaining on the way past and backs off when it gets low. That is
// cheaper than discovering the limit with a 429 and far cheaper than
// discovering it halfway through a paginated report.
func (c *Client) Post(ctx context.Context, path string, body any, into any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	const attempts = 5

	for attempt := range attempts {
		res, err := c.send(ctx, path, payload)
		if err != nil {
			return err
		}

		// 429 is the only status worth retrying. A 400 will be a 400 again.
		if res.status == http.StatusTooManyRequests && attempt < attempts-1 {
			c.sleep(backoff(attempt, res.retryAfter))

			continue
		}

		if res.status < 200 || res.status >= 300 {
			return fmt.Errorf("POST %s: HTTP %d: %s", path, res.status, strings.TrimSpace(string(res.body)))
		}

		// Apple's advice, verbatim in its own docs, is to throttle proactively
		// once the remaining budget is low rather than to wait for the 429.
		if res.remaining >= 0 && res.remaining < 5 {
			c.sleep(2 * time.Second)
		}

		if into == nil {
			return nil
		}

		return json.Unmarshal(res.body, into)
	}

	return fmt.Errorf("POST %s: still rate limited after %d attempts", path, attempts)
}

type response struct {
	status     int
	body       []byte
	remaining  int
	retryAfter time.Duration
}

func (c *Client) send(ctx context.Context, path string, payload []byte) (response, error) {
	token, err := c.Tokens.Get(ctx)
	if err != nil {
		return response{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+path, bytes.NewReader(payload))
	if err != nil {
		return response{}, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	// The scoping header, and the one thing that differs most from v5. An
	// orgId here is a v5 habit and answers 401 with nothing useful in it.
	req.Header.Set("X-AP-Context", "adAccountId="+c.AdAccountID)

	res, err := c.httpClient().Do(req)
	if err != nil {
		return response{}, fmt.Errorf("POST %s: %w", path, err)
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return response{}, err
	}

	return response{
		status:     res.StatusCode,
		body:       body,
		remaining:  headerInt(res.Header.Get("RateLimit-Remaining"), -1),
		retryAfter: time.Duration(headerInt(res.Header.Get("Retry-After"), 0)) * time.Second,
	}, nil
}

// backoff doubles to a ceiling of sixteen seconds, which is what Apple's own
// guidance asks for. Retry-After wins whenever Apple sent one.
func backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}

	seconds := math.Min(math.Pow(2, float64(attempt)), 16)

	return time.Duration(seconds) * time.Second
}

func headerInt(value string, fallback int) int {
	if value == "" {
		return fallback
	}

	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}

	return n
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}

	return BaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}

	return &http.Client{Timeout: 120 * time.Second}
}

func (c *Client) sleep(d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(d)

		return
	}

	time.Sleep(d)
}
