# vars — development workflow

# Default recipe: show help
help:
    @just --list --unsorted

# Check dev toolchain
[group('dev')]
setup:
    #!/usr/bin/env bash
    set -euo pipefail
    command -v go >/dev/null || { echo "ERROR: Go not found"; exit 1; }
    command -v git >/dev/null || echo "WARNING: git not found — versioning/sync will be unavailable"
    command -v ssh-keygen >/dev/null || echo "WARNING: ssh-keygen not found — break-glass recovery needs it"
    command -v staticcheck >/dev/null || { echo "Installing staticcheck..."; go install honnef.co/go/tools/cmd/staticcheck@latest; }
    echo "Toolchain ready."

# Format Go source code
[group('dev')]
fmt:
    go fmt ./...

# Run go vet
[group('dev')]
vet:
    go vet ./...

# Run staticcheck linter
[group('dev')]
lint:
    staticcheck ./...

# Pre-commit quality gate: vet + lint + test
[group('dev')]
check: vet lint test

# Run unit tests
[group('test')]
test:
    go test -timeout 300s ./...

# Run unit tests with verbose output
[group('test')]
test-v:
    go test -v -timeout 300s ./...

# Run integration tests (requires built binary)
[group('test')]
test-integration: build
    go test -tags integration -v ./...

# Run unit tests with race detector
[group('test')]
test-race:
    go test -race -timeout 300s ./...

# Run all tests (unit + integration)
[group('test')]
test-all: test test-integration

# Generate test coverage report
[group('test')]
coverage:
    go test -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out
    @echo "HTML report: go tool cover -html=coverage.out"

# Version from git tag, or "dev"
version := `git describe --tags --always --dirty 2>/dev/null || echo dev`
ldflags := "-s -w -X github.com/vars-cli/vars/cmd.Version=" + version

# Build the binary
[group('build')]
build:
    go build -ldflags '{{ldflags}}' -o vars .

# Install to GOPATH/bin
[group('build')]
install:
    go install -ldflags '{{ldflags}}' .

# Cross-compile for all supported platforms
[group('build')]
cross-compile:
    GOOS=darwin GOARCH=arm64 go build -ldflags '{{ldflags}}' -o dist/vars-darwin-arm64 .
    GOOS=darwin GOARCH=amd64 go build -ldflags '{{ldflags}}' -o dist/vars-darwin-amd64 .
    GOOS=linux  GOARCH=amd64 go build -ldflags '{{ldflags}}' -o dist/vars-linux-amd64  .
    GOOS=linux  GOARCH=arm64 go build -ldflags '{{ldflags}}' -o dist/vars-linux-arm64  .

# Quick end-to-end smoke test against a temp store
[group('test')]
smoke: build
    bash scripts/smoke.sh ./vars

# Dry-run goreleaser
[group('release')]
release-dry:
    goreleaser release --snapshot --clean

# Preview the Homebrew formula from a snapshot build (no push)
[group('release')]
formula-preview: release-dry
    VERSION={{version}} ./scripts/update-formula.sh
