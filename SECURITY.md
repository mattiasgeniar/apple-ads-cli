# Security

## Reporting

Please report anything sensitive privately through
[GitHub's private vulnerability reporting](https://github.com/mattiasgeniar/apple-ads-cli/security/advisories/new)
rather than opening a public issue.

## What this tool holds

An **EC P-256 private key** that is, by itself, full API access to an Apple Ads
account. Treat it exactly as you would an SSH key.

Deliberate choices that follow from that:

- **Credentials are read from the environment, never from command-line
  arguments.** An argv is visible to every other process on the machine through
  `ps`, and it lands in shell history.
- **The private key is read from a file path**, so the key material itself never
  passes through an environment variable either.
- **`auth check` prints the token's length, never the token.** It is a bearer
  credential and that is a command people run while screen-sharing.
- **Assertions are signed with a one-hour lifetime by default.** Apple permits
  180 days; a 180-day bearer credential is not a good default.
- **`.gitignore` covers `*.pem`, `*.p8` and `.env`** so a key left in the working
  directory cannot be committed by an absent-minded `git add -A`.
- **No dependencies.** There is no third-party code in this binary that could
  read the key.

## What it does not do

It does not write the token to disk, cache it between runs, or send anything
anywhere except `appleid.apple.com` and `api.ads.apple.com`.
