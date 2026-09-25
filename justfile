# go-signal justfile

set shell := ["bash", "-c"]

# libsignal_ffi.a is built from the third_party/libsignal submodule (see `just libsignal`).

libsignal_lib := justfile_directory() / "third_party/lib"
export CGO_ENABLED := "1"
export CGO_LDFLAGS := "-L" + libsignal_lib + " " + env("CGO_LDFLAGS", "-g -O2")

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
check: fmt-check lint check-libsignal test check-tidy

# Fetch the signal-cli reference submodule
reference:
    git submodule update --init --depth 1 reference/signal-cli

# Build libsignal_ffi.a from the third_party/libsignal submodule into third_party/lib
libsignal: check-libsignal
    #!/usr/bin/env bash
    set -euo pipefail
    sha=$(git -C third_party/libsignal rev-parse HEAD)
    stamp={{ libsignal_lib }}/libsignal_ffi.sha
    if [[ -f {{ libsignal_lib }}/libsignal_ffi.a && "$(cat "$stamp" 2>/dev/null)" == "$sha" ]]; then
        echo "libsignal_ffi.a is up to date ($sha)"
        exit 0
    fi
    # Run from the submodule so rustup picks up its pinned rust-toolchain.
    (cd third_party/libsignal && cargo build -p libsignal-ffi --release)
    mkdir -p {{ libsignal_lib }}
    cp third_party/libsignal/target/release/libsignal_ffi.a {{ libsignal_lib }}/
    echo "$sha" > "$stamp"

# Fail if the libsignal submodule differs from the version libsignalgo was generated against
check-libsignal:
    #!/usr/bin/env bash
    set -euo pipefail
    git submodule update --init --depth 1 third_party/libsignal
    dir=$(go list -m -f '{{{{.Dir}}' go.mau.fi/mautrix-signal)
    want=$(grep -o 'v[0-9][0-9.]*' "$dir/pkg/libsignalgo/signalversion/version.go")
    lib="git -C third_party/libsignal"
    # Shallow clones carry no tags, so fetch the expected one and compare commits.
    $lib rev-parse -q --verify "refs/tags/$want" >/dev/null || $lib fetch -q --depth 1 origin tag "$want"
    if [[ "$($lib rev-parse HEAD)" != "$($lib rev-parse "$want^{commit}")" ]]; then
        echo "libsignal version mismatch: submodule is $($lib describe --tags --always), libsignalgo expects $want" >&2
        exit 1
    fi
    echo "libsignal submodule matches libsignalgo ($want)"

# Clean build artifacts
clean:
    rm -rf bin/ dist/ coverage.out coverage.html

# Show help
help:
    @just --list
