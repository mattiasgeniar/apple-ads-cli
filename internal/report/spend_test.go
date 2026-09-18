package report

import (
	"encoding/json"
	"testing"
)

func TestCentsParsesAppleDecimalStringsExactly(t *testing.T) {
	cases := map[string]int64{
		"12.55":   1255,
		"0.01":    1,
		"0":       0,
		"":        0,
		"100":     10000,
		"1234.5":  123450,
		"-3.20":   -320,
		"0.005":   1, // rounds up, not away
		"0.004":   0, // rounds down
		"9999.99": 999999,
	}

	for amount, want := range cases {
		t.Run(amount, func(t *testing.T) {
			got, err := Money{Amount: amount, Currency: "EUR"}.Cents()
			if err != nil {
				t.Fatalf("cents: %v", err)
			}
			if got != want {
				t.Errorf("Cents(%q) = %d, want %d", amount, got, want)
			}
		})
	}
}

// Money through float64 is the classic way a cost report drifts. A hundred
// rows of 0.07 must be exactly 700 cents, not 699 and not 701.
func TestCentsDoesNotDriftAcrossManyRows(t *testing.T) {
	var total int64

	for range 100 {
		cents, err := Money{Amount: "0.07"}.Cents()
		if err != nil {
			t.Fatalf("cents: %v", err)
		}
		total += cents
	}

	if total != 700 {
		t.Errorf("100 rows of 0.07 summed to %d cents, want 700", total)
	}
}

func TestCentsRefusesSomethingThatIsNotANumber(t *testing.T) {
	if _, err := (Money{Amount: "twelve euros"}).Cents(); err == nil {
		t.Fatal("want an error")
	}
}

func TestRowsFlattensOneRowPerAdPerDay(t *testing.T) {
	rows, err := Rows([]byte(`{
      "data": {"reportingDataResponse": {"row": [
        {
          "metadata": {"campaignId": 111, "campaignName": "brand", "adGroupId": 222, "adId": 333, "adName": "hero"},
          "granularity": [
            {"date": "2026-09-16", "impressions": 100, "taps": 10, "installs": 2, "localSpend": {"amount": "12.55", "currency": "eur"}},
            {"date": "2026-09-17", "impressions": 200, "taps": 20, "installs": 4, "localSpend": {"amount": "25.10", "currency": "eur"}}
          ]
        }
      ]}}
    }`))
	if err != nil {
		t.Fatalf("rows: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}

	first := rows[0]
	if first.Platform != "apple" {
		t.Errorf("platform = %q", first.Platform)
	}
	if first.CampaignID != "111" || first.AdID != "333" {
		t.Errorf("ids = %q/%q, want 111/333", first.CampaignID, first.AdID)
	}
	if first.SpendCents != 1255 {
		t.Errorf("spend = %d, want 1255", first.SpendCents)
	}
	if first.Currency != "EUR" {
		t.Errorf("currency = %q, want EUR upper-cased", first.Currency)
	}
	if first.Clicks != 10 {
		t.Errorf("taps should land in clicks, got %d", first.Clicks)
	}
	if first.Conversions != 2 {
		t.Errorf("installs should land in conversions, got %d", first.Conversions)
	}
}

// Apple returns every day in the range whether or not the ad ran. Storing the
// empty ones puts zero-spend rows in a cost table for no reason.
func TestRowsDropsDaysWhereNothingHappened(t *testing.T) {
	rows, err := Rows([]byte(`{
      "data": {"reportingDataResponse": {"row": [
        {
          "metadata": {"campaignId": 1, "adId": 2},
          "granularity": [
            {"date": "2026-09-16", "impressions": 0, "taps": 0, "installs": 0, "localSpend": {"amount": "0", "currency": "EUR"}},
            {"date": "2026-09-17", "impressions": 5, "taps": 1, "installs": 0, "localSpend": {"amount": "1.00", "currency": "EUR"}}
          ]
        }
      ]}}
    }`))
	if err != nil {
		t.Fatalf("rows: %v", err)
	}

	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Date != "2026-09-17" {
		t.Errorf("kept the wrong day: %q", rows[0].Date)
	}
}

// Apple sends ids as JSON numbers. They must survive as strings, because they
// are identifiers rather than quantities and 64-bit ids lose precision through
// float64, which is what encoding/json uses for a bare number.
func TestRowsKeepsLargeIdsExact(t *testing.T) {
	rows, err := Rows([]byte(`{
      "data": {"reportingDataResponse": {"row": [
        {
          "metadata": {"campaignId": 9007199254740993, "adId": 9007199254740995},
          "granularity": [{"date": "2026-09-17", "impressions": 1, "taps": 1, "installs": 0, "localSpend": {"amount": "1.00", "currency": "EUR"}}]
        }
      ]}}
    }`))
	if err != nil {
		t.Fatalf("rows: %v", err)
	}

	if rows[0].CampaignID != "9007199254740993" {
		t.Errorf("campaign id = %q, lost precision", rows[0].CampaignID)
	}
	if rows[0].AdID != "9007199254740995" {
		t.Errorf("ad id = %q, lost precision", rows[0].AdID)
	}
}

func TestRowsIsEmptyRatherThanNullWhenAppleSendsNothing(t *testing.T) {
	rows, err := Rows([]byte(`{"data": {"reportingDataResponse": {"row": []}}}`))
	if err != nil {
		t.Fatalf("rows: %v", err)
	}

	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// `null` on stdin is a different thing to parse than `[]` at the far end,
	// and the far end is a shell pipeline.
	if string(encoded) != "[]" {
		t.Errorf("marshalled to %s, want []", encoded)
	}
}

func TestRowsRefusesGarbage(t *testing.T) {
	if _, err := Rows([]byte(`not json`)); err == nil {
		t.Fatal("want an error")
	}
}
