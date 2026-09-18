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
  version                        print the version and exit

Environment:
  APPLE_ADS_CLIENT_ID          Account Settings > API, on Apple Ads *Advanced*
                               https://app-ads.apple.com/cm/app/settings/api
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

// Spend pulls one page of ad-level daily spend.
//
// Exported so the end-to-end test can drive it against a fake Apple without
// going through argv and the environment.
func Spend(ctx context.Context, client *api.Client, from, to, timeZone string) ([]report.Row, error) {
	body := map[string]any{
		"timeRange": map[string]string{
			"start":       from,
			"end":         to,
			"timeZone":    timeZone,
			"granularity": "DAILY",
		},
		// Apple caps a page at 5000. Asking for the cap keeps the common case
		// to one request; anything larger needs the pagination loop this does
		// not yet have, and the count below says so out loud rather than
		// silently reporting a truncated month.
		"pagination": map[string]int{"offset": 0, "limit": 5000},
	}

	var raw json.RawMessage
	if err := client.Post(ctx, "/reports/apps/ads/query", body, &raw); err != nil {
		return nil, err
	}

	rows, err := report.Rows(raw)
	if err != nil {
		return nil, err
	}

	if len(rows) >= 5000 {
		return rows, errors.New("hit the 5000-row page limit: narrow the date range, this tool does not paginate yet")
	}

	return rows, nil
}
