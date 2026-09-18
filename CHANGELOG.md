# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project uses
[semantic versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `auth check`: mint an access token and confirm the credentials work.
- `report --from --to`: ad-level daily spend as JSON on stdout.
- ES256 client-assertion signing, with raw `R||S` signatures rather than ASN.1.
- Token caching for the token's lifetime, re-minting within a minute of expiry.
- Rate-limit handling: proactive throttling below 5 remaining, and doubling
  backoff to 16 seconds on a 429, with `Retry-After` taking precedence.
- Decimal-string money parsing that never passes through a float.

### Known limitations

- **Not verified against a live Apple Ads account.** Field names in
  `internal/report/spend.go` are documentation-derived.
- No pagination past Apple's 5000-row page; the tool errors rather than
  silently truncating.
- No campaign, ad group, ad or keyword mutation yet.
