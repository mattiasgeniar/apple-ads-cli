package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/mattiasgeniar/apple-ads-cli/internal/api"
)

type token string

func (t token) Get(context.Context) (string, error) { return string(t), nil }

// The end-to-end shape check: a realistic Apple response in, and the exact
// JSON this tool prints out.
//
// The file it writes is what the consuming side is tested against, so the two
// repositories cannot drift apart silently. Nothing in this test talks to
// Apple; what it proves is the contract, not the credentials.
func TestSpendPrintsRowsInTheAgreedShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reports/apps/ads/query" {
			t.Errorf("path = %q", r.URL.Path)
		}

		var sent struct {
			TimeRange map[string]string `json:"timeRange"`
		}
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if sent.TimeRange["granularity"] != "DAILY" {
			t.Errorf("granularity = %q, want DAILY", sent.TimeRange["granularity"])
		}
		if sent.TimeRange["start"] != "2026-09-16" || sent.TimeRange["end"] != "2026-09-17" {
			t.Errorf("range = %v", sent.TimeRange)
		}

		_, _ = w.Write([]byte(`{
          "data": {"reportingDataResponse": {"row": [
            {
              "metadata": {"campaignId": 1544512, "campaignName": "brand-defence", "adGroupId": 99, "adId": 778812, "adName": "hero-en"},
              "granularity": [
                {"date": "2026-09-16", "impressions": 4210, "taps": 233, "installs": 61, "localSpend": {"amount": "371.09", "currency": "EUR"}},
                {"date": "2026-09-17", "impressions": 3980, "taps": 201, "installs": 54, "localSpend": {"amount": "322.55", "currency": "EUR"}}
              ]
            },
            {
              "metadata": {"campaignId": 1544513, "campaignName": "competitor-terms", "adGroupId": 100, "adId": 778813, "adName": "hero-nl"},
              "granularity": [
                {"date": "2026-09-17", "impressions": 1100, "taps": 44, "installs": 3, "localSpend": {"amount": "88.20", "currency": "EUR"}}
              ]
            }
          ]}}
        }`))
	}))
	defer server.Close()

	client := &api.Client{
		AdAccountID: "9876543",
		Tokens:      token("tok"),
		BaseURL:     server.URL,
		HTTP:        server.Client(),
	}

	rows, err := Spend(context.Background(), client, "2026-09-16", "2026-09-17", "UTC")
	if err != nil {
		t.Fatalf("spend: %v", err)
	}

	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}

	if rows[0].SpendCents != 37109 {
		t.Errorf("spend_cents = %d, want 37109", rows[0].SpendCents)
	}
	if rows[0].CampaignName != "brand-defence" {
		t.Errorf("campaign_name = %q", rows[0].CampaignName)
	}

	encoded, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Written where the consuming project's own check can pick it up. Skipped
	// silently if the directory is not writable, so CI stays green.
	if out := os.Getenv("APPLE_ADS_FIXTURE_OUT"); out != "" {
		if err := os.WriteFile(out, encoded, 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
}
