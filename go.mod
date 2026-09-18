// The module path is the repo's fetch path, because `go get` resolves a module
// over the network by exactly this string. A name that does not match the
// remote cannot be fetched at all, only `replace`d locally.
module github.com/mattiasgeniar/apple-ads-cli

go 1.26
