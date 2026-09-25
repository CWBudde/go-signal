# go-signal justfile

set shell := ["bash", "-c"]

# Default target
default: build

# Build the binary with version info
build:
    #!/usr/bin/env bash
    VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
    COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
    BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    PKG=github.com/cwbudde/go-signal/cmd
    go build \
        -ldflags "-X ${PKG}.Version=${VERSION} -X ${PKG}.GitCommit=${COMMIT} -X ${PKG}.BuildDate=${BUILD_DATE}" \
        -o bin/go-signal .

# Build the binary without version info (faster for development)
build-dev:
    go build -o bin/go-signal .

# Test that the project can build successfully
test-can-build:
    @just build

# Run the application
run *args:
    go run . {{ args }}

# Run tests
test:
    go test -race -count=1 ./...

# Run tests with coverage
test-coverage:
    go test -coverprofile=coverage.out -covermode=atomic ./...
    go tool cover -html=coverage.out -o coverage.html

# Run golangci-lint
lint:
    golangci-lint run --timeout 5m

# Run golangci-lint with fixes
lint-fix:
    golangci-lint run --timeout 5m --fix

# Format code using treefmt
fmt:
    treefmt --allow-missing-formatter

# Check if code is formatted
fmt-check:
    treefmt --allow-missing-formatter --fail-on-change

# Check if go.mod is tidy
check-tidy:
    @go mod tidy
    @git diff --exit-code go.mod go.sum || { echo "go.mod/go.sum not tidy. Run 'go mod tidy'."; exit 1; }

# Run all checks
check: fmt-check lint test check-tidy

# Fetch the signal-cli reference submodule
reference:
    git submodule update --init --depth 1 reference/signal-cli

# Clean build artifacts
clean:
    rm -rf bin/ dist/ coverage.out coverage.html

# Show help
help:
    @just --list
