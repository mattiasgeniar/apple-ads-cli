package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

type staticToken string

func (s staticToken) Get(context.Context) (string, error) { return string(s), nil }

func clientFor(server *httptest.Server, slept *[]time.Duration) *Client {
	return &Client{
		AdAccountID: "9876543",
		Tokens:      staticToken("tok-abc"),
		BaseURL:     server.URL,
		HTTP:        server.Client(),
		Sleep:       func(d time.Duration) { *slept = append(*slept, d) },
	}
}

// adAccountId, not orgId. The v5 habit answers 401 with nothing useful in it,
// so the header is worth pinning down.
func TestPostScopesTheRequestWithAdAccountId(t *testing.T) {
	var gotContext, gotAuth, gotType string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContext = r.Header.Get("X-AP-Context")
		gotAuth = r.Header.Get("Authorization")
		gotType = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	var slept []time.Duration
	if err := clientFor(server, &slept).Post(context.Background(), "/reports", map[string]string{}, nil); err != nil {
		t.Fatalf("post: %v", err)
	}

	if gotContext != "adAccountId=9876543" {
		t.Errorf("X-AP-Context = %q, want adAccountId=9876543", gotContext)
	}
	if gotAuth != "Bearer tok-abc" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotType != "application/json" {
		t.Errorf("Content-Type = %q", gotType)
	}
}

func TestPostSendsTheBodyAndDecodesTheResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var sent map[string]any
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if sent["hello"] != "world" {
			t.Errorf("body = %v", sent)
		}
		_, _ = w.Write([]byte(`{"answer":42}`))
	}))
	defer server.Close()

	var into struct {
		Answer int `json:"answer"`
	}

	var slept []time.Duration
	if err := clientFor(server, &slept).Post(context.Background(), "/x", map[string]string{"hello": "world"}, &into); err != nil {
		t.Fatalf("post: %v", err)
	}

	if into.Answer != 42 {
		t.Errorf("answer = %d", into.Answer)
	}
}

func TestPostRetriesA429AndHonoursRetryAfter(t *testing.T) {
	calls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 3 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)

			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	var slept []time.Duration
	if err := clientFor(server, &slept).Post(context.Background(), "/x", nil, nil); err != nil {
		t.Fatalf("post: %v", err)
	}

	if calls != 3 {
		t.Errorf("called %d times, want 3", calls)
	}
	for _, d := range slept {
		if d != 7*time.Second {
			t.Errorf("slept %v, want Retry-After's 7s to win over the doubling", d)
		}
	}
}

// Apple's own guidance is to slow down before the 429, not after it.
func TestPostThrottlesItselfWhenTheRemainingBudgetIsLow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("RateLimit-Remaining", "2")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	var slept []time.Duration
	if err := clientFor(server, &slept).Post(context.Background(), "/x", nil, nil); err != nil {
		t.Fatalf("post: %v", err)
	}

	if len(slept) != 1 {
		t.Fatalf("slept %d times, want 1 proactive pause", len(slept))
	}
}

func TestPostDoesNotThrottleWhenThereIsBudgetLeft(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("RateLimit-Remaining", "400")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	var slept []time.Duration
	if err := clientFor(server, &slept).Post(context.Background(), "/x", nil, nil); err != nil {
		t.Fatalf("post: %v", err)
	}

	if len(slept) != 0 {
		t.Errorf("slept %v, want no pause", slept)
	}
}

// A 400 will be a 400 again. Retrying it wastes the rate-limit budget that the
// real problem will need.
func TestPostDoesNotRetryAClientError(t *testing.T) {
	calls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad range"}`))
	}))
	defer server.Close()

	var slept []time.Duration
	err := clientFor(server, &slept).Post(context.Background(), "/x", nil, nil)

	if err == nil {
		t.Fatal("want an error")
	}
	if calls != 1 {
		t.Errorf("called %d times, want 1", calls)
	}
}

func TestPostGivesUpAfterFiveRateLimitedAttempts(t *testing.T) {
	calls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	var slept []time.Duration
	err := clientFor(server, &slept).Post(context.Background(), "/x", nil, nil)

	if err == nil {
		t.Fatal("want an error")
	}
	if calls != 5 {
		t.Errorf("called %d times, want 5", calls)
	}
}

func TestBackoffDoublesToACeilingOfSixteenSeconds(t *testing.T) {
	want := []int{1, 2, 4, 8, 16, 16, 16}

	for attempt, seconds := range want {
		got := backoff(attempt, 0)
		if got != time.Duration(seconds)*time.Second {
			t.Errorf("backoff(%d) = %v, want %ds", attempt, got, seconds)
		}
	}
}

func TestHeaderIntFallsBackWhenTheHeaderIsAbsentOrJunk(t *testing.T) {
	if got := headerInt("", -1); got != -1 {
		t.Errorf("empty header = %d, want the fallback", got)
	}
	if got := headerInt("not a number", -1); got != -1 {
		t.Errorf("junk header = %d, want the fallback", got)
	}
	if got := headerInt(strconv.Itoa(12), -1); got != 12 {
		t.Errorf("good header = %d, want 12", got)
	}
}
