# apple-ads-cli

[![CI](https://github.com/mattiasgeniar/apple-ads-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/mattiasgeniar/apple-ads-cli/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/mattiasgeniar/apple-ads-cli.svg)](https://pkg.go.dev/github.com/mattiasgeniar/apple-ads-cli)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Drive the **Apple Ads Platform API 1.0** from a terminal. No dependencies, one
static binary, JSON on stdout.

```sh
apple-ads-cli auth check
apple-ads-cli report --from 2026-09-01 --to 2026-09-17 > spend.json
```

## Why this exists

Apple has four official client libraries (Swift, Python, Java, Node) and no CLI.
There is no PHP client at all. And almost every example, SDK and blog post you
will find targets **Campaign Management API 5**, which Apple sunsets on
**26 January 2027**:

| | Old | Current |
|---|---|---|
| Base URL | `https://api.searchads.apple.com/api/v5/` | `https://api.ads.apple.com/v1/` |
| Scope header | `X-AP-Context: orgId=…` | `X-AP-Context: adAccountId=…` |
| Status | sunset 2027-01-26 | Platform API 1.0, shipped Aug 2026 |

So prior art that looks helpful is usually pointed at the wrong API. This is
written against 1.0.

## Install

```sh
go install github.com/mattiasgeniar/apple-ads-cli@latest
```

Or build it: `go build -o apple-ads-cli .`

## Credentials

Apple's auth is the friendliest of any ad platform: no browser, no refresh
token, no consent screen that expires overnight. Generate a key pair once,
upload the public half, and a signed assertion is the whole credential.

```sh
openssl ecparam -genkey -name prime256v1 -noout -out private-key.pem
openssl ec -in private-key.pem -pubout -out public-key.pem
```

Paste `public-key.pem` into **Account Settings → API**, here:

**https://app-ads.apple.com/cm/app/settings/api**

Two things that cost people an afternoon before they ever get that far:

- **The API section only exists in Apple Ads _Advanced_.** `ads.apple.com` is the
  marketing site and Basic has no API at all, so there is nothing to find there.
  Apple's own walkthrough starts "choose Sign In > Advanced and log in as an
  account administrator".
- **Your user needs an API role.** An account admin grants it under
  Account Settings → User Management → Invite Users, in the User Access and Role
  section. Without it the API section does not appear even on Advanced.

After you save, the three ids appear **as a code block above the public key
field**, not on a separate screen. Note that `clientId` and `teamId` can
legitimately be the same value — Apple's own sample has them identical — so if
they match, that is not a copy-paste mistake.

Everything is read from the environment, never from argv, so nothing secret
lands in a shell history or in a `ps` listing:

```sh
export APPLE_ADS_CLIENT_ID=SEARCHADS.xxxxxxxx-…
export APPLE_ADS_TEAM_ID=SEARCHADS.xxxxxxxx-…     # not the same as the client id
export APPLE_ADS_KEY_ID=xxxxxxxx-…
export APPLE_ADS_AD_ACCOUNT_ID=1234567
export APPLE_ADS_PRIVATE_KEY_FILE=./private-key.pem
```

`auth check` mints a token and prints nothing but a length. Run it first.

## Output

`report` prints one row per ad per day, in a shape deliberately shared with the
other CLIs in this family so a consumer never has to know which platform a row
came from:

```json
[
  {
    "platform": "apple",
    "date": "2026-09-17",
    "campaign_id": "1544512",
    "campaign_name": "brand-defence",
    "ad_group_id": "99",
    "ad_id": "778812",
    "ad_name": "hero-en",
    "spend_cents": 32255,
    "currency": "EUR",
    "impressions": 3980,
    "clicks": 201,
    "conversions": 54
  }
]
```

Money is **integer cents**, parsed from Apple's decimal string without going
through a float. A cent of drift per row, in one direction, across a month of
daily rows per ad, is a visibly wrong cost per subscriber at the far end.

Apple counts a **tap** where the rest of the world counts a click, and an
**install** where the rest counts a conversion. Both are renamed here so the
columns line up across platforms.

## Things that will bite you

- **`sub` is the client id and `iss` is the team id.** They are different
  values, they look alike, and swapping them fails with `invalid_client`, which
  is also what Apple says for every other credential mistake.
- **`aud` is `https://appleid.apple.com`**, even though every later request goes
  to `api.ads.apple.com`.
- **ES256 signatures must be raw `R||S`**, not ASN.1. Go's `ecdsa.Sign` gives
  you the integers; encoding them with `asn1` produces a signature Apple
  rejects. This is the most common way a hand-rolled ES256 signer is wrong.
- **Never set age or gender on an ad group** if you care about attribution.
  Apple's AdServices API returns `attribution=false` for any ad group that uses
  them, so demographic targeting silently destroys your own measurement.
- **Rate limits are headers, not guesses.** Apple publishes `RateLimit-Limit`,
  `RateLimit-Remaining` and `RateLimit-Reset` on every response and asks you to
  slow down *before* being throttled. This client does, at fewer than 5
  remaining, and backs off doubling to 16 seconds on a 429.

## What is verified, and what is not

Being honest about this, because it is a young tool:

**Verified by tests** (`go test ./...`, 28 test functions and 44 cases, no network):

- ES256 assertion signing, checked by verifying the signature against the public
  key, and the raw 64-byte `R||S` encoding.
- The `sub`/`iss`/`aud`/`kid` mapping.
- Both openssl key encodings (SEC1 and PKCS#8) load.
- Token exchange: form fields, caching for the token's lifetime, re-minting
  within a minute of expiry, and error reporting.
- Request scoping (`X-AP-Context: adAccountId=`), 429 retry, `Retry-After`
  precedence, proactive throttling, and that a 400 is not retried.
- Decimal-string money parsing, including that 100 rows of `0.07` sum to exactly
  700 cents.
- 64-bit ids survive as strings rather than losing precision through float64.

**Not verified against a live Apple account.** I do not have credentials yet.
The request and response shapes come from Apple's published documentation, so
treat the field names in `internal/report/spend.go` as the most likely thing to
be wrong. The decoder is deliberately lenient: a renamed field gives you a zero
and a visible problem, not a panic.

**Not built yet:** pagination past Apple's 5000-row page (the tool errors rather
than silently truncating), campaign/ad-group/ad mutation, keyword management,
and the search-term report.

## Piping it somewhere

The row shape is stable and boring on purpose, so the usual thing to do with it
is pipe it straight into whatever holds your cost data:

```sh
apple-ads-cli report --from 2026-09-01 --to 2026-09-17 \
  | your-importer --platform=apple
```

`jq` works on it directly too:

```sh
apple-ads-cli report --from 2026-09-01 --to 2026-09-17 \
  | jq -r '.[] | [.date, .campaign_name, .ad_id, .spend_cents] | @tsv'
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Bug reports from anybody with a live
Apple Ads account are especially welcome, for the reason in the section above.

Security policy, and what this tool does with your private key, is in
[SECURITY.md](SECURITY.md).

## Licence

MIT. See [LICENSE](LICENSE).
