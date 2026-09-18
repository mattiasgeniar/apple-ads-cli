# Contributing

Bug reports and pull requests are welcome.

## The one thing worth knowing first

**Most of this tool has never run against a live Apple Ads account.** The
request and response shapes come from Apple's published documentation, not from
observed traffic. If you have an account and something does not work, that
report is the single most valuable contribution you can make — please include:

- the command you ran,
- the error, and
- the response body with your ids replaced by `X`.

## Running the tests

```sh
go test ./...          # everything, no network
go test -race ./...    # what CI runs
gofmt -l .             # must print nothing
go vet ./...
```

There are no dependencies and there should stay none. Everything here is
standard library, which is why the binary is small, the build is fast and the
supply chain is empty. A pull request that adds a dependency needs to argue for
it.

## Conventions

- **Comments explain why, never what.** The code says what it does; a comment
  earns its place by saying what is not obvious from reading it, usually a
  platform quirk or the failure mode some line is preventing.
- **Money is integer cents.** Never a float, anywhere, for any reason.
- **Tests are named as sentences** describing the behaviour they pin down, and
  they test behaviour rather than implementation.
- Errors get context: `fmt.Errorf("parse amount %q: %w", raw, err)`.

## Adding an endpoint

Apple's Platform API is consistent: reads are `POST /{entity}/query` with
`filters`/`sorting`/`pagination`, writes are partial `PUT`. `internal/api` has
the transport, the auth and the rate-limit handling, so a new endpoint is
usually a request struct, a response struct and a test against `httptest`.
