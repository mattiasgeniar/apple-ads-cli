// Package report turns an Apple Ads reporting response into the one row shape
// every CLI in this family prints.
//
// The shape is deliberately boring and identical across platforms, so that
// whatever consumes it never has to know which platform a row came from:
//
//	{"platform":"apple","date":"2026-09-17","campaign_id":"123",
//	 "campaign_name":"brand-defence","ad_id":"456","ad_name":"…",
//	 "spend_cents":12550,"currency":"EUR","impressions":5000,"clicks":120,
//	 "conversions":8}
//
// Money is integer cents and never a float. A cent of drift per row, in one
// direction, across a month of daily rows per ad, is a visibly wrong cost per
// subscriber at the other end.
package report

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Row is one advertisement on one day.
type Row struct {
	Platform     string `json:"platform"`
	Date         string `json:"date"`
	CampaignID   string `json:"campaign_id"`
	CampaignName string `json:"campaign_name,omitempty"`
	AdGroupID    string `json:"ad_group_id,omitempty"`
	AdID         string `json:"ad_id"`
	AdName       string `json:"ad_name,omitempty"`
	SpendCents   int64  `json:"spend_cents"`
	Currency     string `json:"currency"`
	Impressions  int64  `json:"impressions"`
	Clicks       int64  `json:"clicks"`
	Conversions  int64  `json:"conversions"`
}

// Money is Apple's {amount, currency} pair, where the amount is a decimal
// *string* rather than a number. That is a good decision on Apple's part and
// the reason this type exists: parsing it through float64 and rounding once,
// here, is the only place precision is at risk.
type Money struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// Cents parses Apple's decimal string without going through a float.
//
// "12.55" is 1255 cents. Splitting on the decimal point and padding is exact
// for every value Apple can send, whereas float64 arithmetic on money is only
// usually exact, and "usually" accumulates.
func (m Money) Cents() (int64, error) {
	raw := strings.TrimSpace(m.Amount)
	if raw == "" {
		return 0, nil
	}

	negative := strings.HasPrefix(raw, "-")
	raw = strings.TrimPrefix(raw, "-")

	whole, frac, _ := strings.Cut(raw, ".")

	units, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse amount %q: %w", m.Amount, err)
	}

	// Apple reports two decimal places. Anything longer is rounded rather than
	// truncated, because truncation biases every row the same way.
	cents := int64(0)
	if frac != "" {
		padded := (frac + "000")[:3]

		thousandths, err := strconv.ParseInt(padded, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse amount %q: %w", m.Amount, err)
		}
		cents = int64(math.Round(float64(thousandths) / 10))
	}

	total := units*100 + cents
	if negative {
		total = -total
	}

	return total, nil
}

// appleResponse is the envelope Apple wraps every report in.
//
// Decoded leniently on purpose. This tool has been written against Apple's
// published documentation rather than against a live account, so a field that
// turns out to be named slightly differently should produce a row with a zero
// in it and a visible problem, not a panic. See the README.
type appleResponse struct {
	Data struct {
		ReportingDataResponse struct {
			Row []struct {
				Metadata struct {
					CampaignID   json.Number `json:"campaignId"`
					CampaignName string      `json:"campaignName"`
					AdGroupID    json.Number `json:"adGroupId"`
					AdID         json.Number `json:"adId"`
					AdName       string      `json:"adName"`
				} `json:"metadata"`
				Granularity []struct {
					Date        string `json:"date"`
					Impressions int64  `json:"impressions"`
					Taps        int64  `json:"taps"`
					Installs    int64  `json:"installs"`
					LocalSpend  Money  `json:"localSpend"`
				} `json:"granularity"`
			} `json:"row"`
		} `json:"reportingDataResponse"`
	} `json:"data"`
}

// Rows flattens Apple's nested response into one row per ad per day.
//
// Apple nests the days inside the ad; this family of tools is flat, because a
// flat row is what a database wants and what a diff of two days is readable in.
func Rows(payload []byte) ([]Row, error) {
	var decoded appleResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("decode report: %w", err)
	}

	rows := []Row{}

	for _, entry := range decoded.Data.ReportingDataResponse.Row {
		for _, day := range entry.Granularity {
			cents, err := day.LocalSpend.Cents()
			if err != nil {
				return nil, err
			}

			// Apple reports every day in the range, including the ones an ad
			// did not run. A zero row is noise in a cost table and a divide by
			// zero waiting to happen, so it is dropped rather than stored.
			if cents == 0 && day.Impressions == 0 && day.Taps == 0 {
				continue
			}

			rows = append(rows, Row{
				Platform:     "apple",
				Date:         day.Date,
				CampaignID:   entry.Metadata.CampaignID.String(),
				CampaignName: entry.Metadata.CampaignName,
				AdGroupID:    entry.Metadata.AdGroupID.String(),
				AdID:         entry.Metadata.AdID.String(),
				AdName:       entry.Metadata.AdName,
				SpendCents:   cents,
				Currency:     strings.ToUpper(day.LocalSpend.Currency),
				Impressions:  day.Impressions,

				// Apple counts a tap where the rest of this family counts a
				// click. Same event, different word, and the column it lands
				// in is called clicks everywhere else.
				Clicks:      day.Taps,
				Conversions: day.Installs,
			})
		}
	}

	return rows, nil
}
