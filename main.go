// Command apple-ads-cli drives the Apple Ads Platform API from a terminal.
//
// It exists because there is no maintained CLI for Apple Ads and no PHP client
// at all, and because everything published online targets Campaign Management
// API 5, which Apple sunsets on 26 January 2027.
//
//	apple-ads-cli auth check
//	apple-ads-cli report --from 2026-09-01 --to 2026-09-17 > spend.json
//
// Credentials come from the environment so that nothing secret is ever in a
// shell history or an argv a sibling process can read:
//
//	APPLE_ADS_CLIENT_ID, APPLE_ADS_TEAM_ID, APPLE_ADS_KEY_ID,
//	APPLE_ADS_AD_ACCOUNT_ID, APPLE_ADS_PRIVATE_KEY_FILE
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/mattiasgeniar/apple-ads-cli/internal/api"
	"github.com/mattiasgeniar/apple-ads-cli/internal/auth"
	"github.com/mattiasgeniar/apple-ads-cli/internal/report"
)

// version is stamped at build time by the release workflow:
//
//	go build -ldflags "-X main.version=$(git describe --tags)"
//
// It stays "dev" for anything built from a working tree, which is exactly what
// you want to see in a bug report from a binary somebody compiled themselves.
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()

		return errors.New("no command given")
	}

	switch args[0] {
	case "version", "-v", "--version":
		fmt.Println(version)

		return nil
	case "auth":
		return authCommand(args[1:])
	case "report":
		return reportCommand(args[1:])
	case "campaigns":
		return campaignsCommand()
	case "help", "-h", "--help":
		usage()

		return nil
	default:
		usage()

		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `apple-ads-cli - Apple Ads Platform API 1.0 from the terminal

  auth check                     mint a token and prove the credentials work
  report --from --to             spend per ad per day, JSON on stdout
  campaigns                      every campaign with its status and budget
  version                        print the version and exit

Environment:
  APPLE_ADS_CLIENT_ID          Account Settings > API, signed in as a user with
                               an API role (Apple Ads Advanced)
  APPLE_ADS_TEAM_ID            the same screen; not the same as the client id
  APPLE_ADS_KEY_ID             the id of the uploaded public key
  APPLE_ADS_AD_ACCOUNT_ID      scopes every request
  APPLE_ADS_PRIVATE_KEY_FILE   PEM holding the EC P-256 private key
`)
}

// credentials reads the environment once and says exactly which variable is
// missing, because "invalid_client" from Apple never will.
func credentials() (auth.Credentials, string, error) {
	var missing []string

	get := func(name string) string {
		value := os.Getenv(name)
		if value == "" {
			missing = append(missing, name)
		}

		return value
	}

	clientID := get("APPLE_ADS_CLIENT_ID")
	teamID := get("APPLE_ADS_TEAM_ID")
	keyID := get("APPLE_ADS_KEY_ID")
	account := get("APPLE_ADS_AD_ACCOUNT_ID")
	keyFile := get("APPLE_ADS_PRIVATE_KEY_FILE")

	if len(missing) > 0 {
		return auth.Credentials{}, "", fmt.Errorf("missing environment: %v", missing)
	}

	pemBytes, err := os.ReadFile(keyFile)
	if err != nil {
		return auth.Credentials{}, "", fmt.Errorf("read %s: %w", keyFile, err)
	}

	key, err := auth.ParsePrivateKey(pemBytes)
	if err != nil {
		return auth.Credentials{}, "", err
	}

	return auth.Credentials{
		ClientID:   clientID,
		TeamID:     teamID,
		KeyID:      keyID,
		PrivateKey: key,
	}, account, nil
}

func authCommand(args []string) error {
	if len(args) == 0 || args[0] != "check" {
		return errors.New("usage: apple-ads-cli auth check")
	}

	creds, _, err := credentials()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	source := &auth.Source{Creds: creds}

	token, err := source.Get(ctx)
	if err != nil {
		return err
	}

	// The token itself is never printed. It is a bearer credential and this is
	// a command people run while screen-sharing.
	fmt.Printf("ok: got a token, %d characters, valid for about an hour\n", len(token))

	return nil
}

func reportCommand(args []string) error {
	flags := flag.NewFlagSet("report", flag.ContinueOnError)
	from := flags.String("from", "", "first day, YYYY-MM-DD")
	to := flags.String("to", "", "last day, YYYY-MM-DD")
	timeZone := flags.String("timezone", "UTC", "UTC or ORTZ")

	if err := flags.Parse(args); err != nil {
		return err
	}

	if *from == "" || *to == "" {
		return errors.New("--from and --to are both required")
	}

	creds, account, err := credentials()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client := &api.Client{
		AdAccountID: account,
		Tokens:      &auth.Source{Creds: creds},
	}

	rows, err := Spend(ctx, client, *from, *to, *timeZone)
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")

	return encoder.Encode(rows)
}

// Spend pulls ad-level daily spend for every app campaign in the account.
//
// Platform API 1.0 refuses an ads report without a campaignId filter, so this
// lists the campaigns first and asks for one report per campaign. The
// campaign list is also the only place the campaign's name comes from: ad
// metadata carries the id but not the name.
//
// Exported so the end-to-end test can drive it against a fake Apple without
// going through argv and the environment.
func Spend(ctx context.Context, client *api.Client, from, to, timeZone string) ([]report.Row, error) {
	var campaigns struct {
		Result []struct {
			ID                 json.Number `json:"id"`
			Name               string      `json:"name"`
			PromotedObjectType string      `json:"promotedObjectType"`
		} `json:"result"`
	}

	listing := map[string]any{"pagination": map[string]int{"offset": 0, "pageSize": 1000}}
	if err := client.Post(ctx, "/campaigns/query", listing, &campaigns); err != nil {
		return nil, err
	}

	timeRange := map[string]string{"start": from, "end": to, "timeZone": timeZone}

	// Apple rejects a granularity on a one-day range and answers with
	// totalMetrics only, so a single day is asked for without one.
	singleDay := ""
	if from == to {
		singleDay = from
	} else {
		timeRange["granularity"] = "DAILY"
	}

	rows := []report.Row{}
	trace := os.Getenv("APPLE_ADS_DEBUG") != ""

	for _, campaign := range campaigns.Result {
		// Brand campaigns on Apple Maps answer on a different report path
		// and are not something this account runs. App campaigns come back
		// as APPSTORE_APP, which is not the APPS the reports are filed under.
		if campaign.PromotedObjectType == "BUSINESS_BRAND" {
			continue
		}

		body := map[string]any{
			"timeRange": timeRange,
			"filters": []map[string]string{
				{"field": "campaignId", "operator": "EQUALS", "value": campaign.ID.String()},
			},
			"pagination": map[string]int{"offset": 0, "pageSize": 1000},
		}

		var raw json.RawMessage
		if err := client.Post(ctx, "/reports/apps/ads/query", body, &raw); err != nil {
			return nil, err
		}

		// The response shapes here come from Apple's documentation. Seeing
		// the raw answer is the quickest way to tell "no spend" from "a
		// field was renamed", which both look like an empty list.
		if trace {
			fmt.Fprintf(os.Stderr, "campaign %s (%s): %s\n", campaign.ID, campaign.Name, raw)
		}

		page, err := report.Rows(raw, campaign.Name, singleDay)
		if err != nil {
			return nil, err
		}

		rows = append(rows, page...)
	}

	return rows, nil
}

// Campaign is the slice of Apple's campaign object worth watching: whether it
// is serving, why not, and what it may spend.
type Campaign struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	SystemStatus   string   `json:"system_status"`
	LimitedBy      []string `json:"limited_by"`
	DailyBudget    string   `json:"daily_budget"`
	Currency       string   `json:"currency"`
	StartTime      string   `json:"start_time"`
	EndTime        string   `json:"end_time"`
	PromotedObject string   `json:"promoted_object"`
}

// Campaigns lists every campaign in the ad account.
//
// A report only has rows for what was spent, so a campaign that is running
// and buying nothing is invisible in it. This is where that campaign shows up.
func Campaigns(ctx context.Context, client *api.Client) ([]Campaign, error) {
	var listing struct {
		Result []struct {
			ID                          json.Number `json:"id"`
			Name                        string      `json:"name"`
			Status                      string      `json:"status"`
			SystemStatus                string      `json:"systemStatus"`
			SystemStatusLimitingReasons []string    `json:"systemStatusLimitingReasons"`
			StartTime                   string      `json:"startTime"`
			EndTime                     string      `json:"endTime"`
			PromotedObjectType          string      `json:"promotedObjectType"`
			DailyBudget                 struct {
				Value report.Money `json:"value"`
			} `json:"dailyBudget"`
		} `json:"result"`
	}

	body := map[string]any{"pagination": map[string]int{"offset": 0, "pageSize": 1000}}
	if err := client.Post(ctx, "/campaigns/query", body, &listing); err != nil {
		return nil, err
	}

	campaigns := []Campaign{}

	for _, c := range listing.Result {
		limited := c.SystemStatusLimitingReasons
		if limited == nil {
			limited = []string{}
		}

		campaigns = append(campaigns, Campaign{
			ID:             c.ID.String(),
			Name:           c.Name,
			Status:         c.Status,
			SystemStatus:   c.SystemStatus,
			LimitedBy:      limited,
			DailyBudget:    c.DailyBudget.Value.Amount,
			Currency:       c.DailyBudget.Value.Currency,
			StartTime:      c.StartTime,
			EndTime:        c.EndTime,
			PromotedObject: c.PromotedObjectType,
		})
	}

	return campaigns, nil
}

func campaignsCommand() error {
	creds, account, err := credentials()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	client := &api.Client{
		AdAccountID: account,
		Tokens:      &auth.Source{Creds: creds},
	}

	campaigns, err := Campaigns(ctx, client)
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")

	return encoder.Encode(campaigns)
}
