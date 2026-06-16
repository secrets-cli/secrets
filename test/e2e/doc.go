// Package e2e holds black-box end-to-end tests: they build the vars binary and
// run it as a subprocess. The tests are behind the `integration` build tag, so
// the default `go test ./...` skips them; run them with `just test-integration`.
//
// This file (untagged) keeps the package non-empty so `go build`/`go vet ./...`
// don't error when the tag is off.
package e2e
