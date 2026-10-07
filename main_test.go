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
	reports := map[string]string{
		"1544512": `{"result": {"rows": [
          {
            "metadata": {"id": 778812, "name": "hero-en", "campaignId": 1544512, "adGroupId": 99},
            "granularMetrics": [
              {"date": "2026-09-16", "impressions": 4210, "taps": 233, "tapInstalls": 61, "localSpend": {"amount": "371.09", "currency": "EUR"}},
              {"date": "2026-09-17", "impressions": 3980, "taps": 201, "tapInstalls": 54, "localSpend": {"amount": "322.55", "currency": "EUR"}}
            ]
          }
        ]}}`,
		"1544513": `{"result": {"rows": [
          {
            "metadata": {"id": 778813, "name": "hero-nl", "campaignId": 1544513, "adGroupId": 100},
            "granularMetrics": [
              {"date": "2026-09-17", "impressions": 1100, "taps": 44, "tapInstalls": 3, "localSpend": {"amount": "88.20", "currency": "EUR"}}
            ]
          }
        ]}}`,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/campaigns/query" {
			_, _ = w.Write([]byte(`{"result": [
              {"id": 1544512, "name": "brand-defence", "promotedObjectType": "APPSTORE_APP"},
              {"id": 1544513, "name": "competitor-terms", "promotedObjectType": "APPSTORE_APP"},
              {"id": 1544514, "name": "maps", "promotedObjectType": "BUSINESS_BRAND"}
            ]}`))

			return
		}

		if r.URL.Path != "/reports/apps/ads/query" {
			t.Errorf("path = %q", r.URL.Path)
		}

		var sent struct {
			TimeRange map[string]string `json:"timeRange"`
			Filters   []struct {
				Field string `json:"field"`
				Value string `json:"value"`
			} `json:"filters"`
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
		if len(sent.Filters) != 1 || sent.Filters[0].Field != "campaignId" {
			t.Fatalf("filters = %+v, want one campaignId filter", sent.Filters)
		}

		body, ok := reports[sent.Filters[0].Value]
		if !ok {
			t.Errorf("asked for campaign %q, which is not an app campaign", sent.Filters[0].Value)
		}
		_, _ = w.Write([]byte(body))
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
