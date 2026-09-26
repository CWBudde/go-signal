# go-signal justfile

set shell := ["bash", "-c"]

# libsignal_ffi.a is built from the third_party/libsignal submodule (see `just libsignal`).

libsignal_lib := justfile_directory() / "third_party/lib"
export CGO_ENABLED := "1"
export CGO_LDFLAGS := "-L" + libsignal_lib + " " + env("CGO_LDFLAGS", "-g -O2")

# Default target
default: build

# Version info for the -X ldflags. Release builds set VERSION to the tag.

version := env("VERSION", `git describe --tags --always --dirty 2>/dev/null || echo dev`)
commit := env("COMMIT", `git rev-parse --short HEAD 2>/dev/null || echo unknown`)
build_date := `date -u +%Y-%m-%dT%H:%M:%SZ`
version_ldflags := "-X github.com/cwbudde/go-signal/cmd.Version=" + version + " -X github.com/cwbudde/go-signal/cmd.GitCommit=" + commit + " -X github.com/cwbudde/go-signal/cmd.BuildDate=" + build_date

# Go image for the static build, matching the go directive in go.mod

go_image := "golang:" + `sed -n 's/^go \([0-9]*\.[0-9]*\).*/\1/p' go.mod` + "-alpine"

# Build the binary with version info
build:
    go build -ldflags "{{ version_ldflags }}" -o bin/go-signal .

# Release build for this OS/arch (glibc/macOS, dynamic) to dist/<os>_<arch>/go-signal
build-release:
    #!/usr/bin/env bash
    set -euo pipefail
    out=dist/$(go env GOOS)_$(go env GOARCH)
    go build -trimpath -ldflags "-s -w {{ version_ldflags }}" -o "$out/go-signal" .
    "$out/go-signal" version

# Fully static linux binary for this machine's arch (musl, in an Alpine container) to dist/linux_<arch>/
build-static:
    git submodule update --init --depth 1 third_party/libsignal
    docker run --rm -v "$PWD:/src" \
        -e VERSION="{{ version }}" -e COMMIT="{{ commit }}" -e BUILD_DATE="{{ build_date }}" \
        -e HOST_UID="$(id -u)" -e HOST_GID="$(id -g)" \
        {{ go_image }} /src/scripts/build-static.sh

# Run the static binary in a scratch container (the release image) as a smoke test
smoke-static:
    #!/usr/bin/env bash
    set -euo pipefail
    arch=$(go env GOARCH)
    # ldd exits non-zero for a static binary.
    ldd_out=$(LC_ALL=C ldd dist/linux_$arch/go-signal 2>&1 || true)
    grep -q "not a dynamic executable" <<<"$ldd_out" || { echo "$ldd_out" >&2; exit 1; }
    docker build --build-arg TARGETARCH=$arch -t go-signal:smoke .
    docker run --rm go-signal:smoke version
    # No account: must fail cleanly with "not linked", not crash.
    if out=$(docker run --rm go-signal:smoke account show 2>&1); then
        echo "account show succeeded without an account" >&2; exit 1
    fi
    echo "$out" | grep -q "no linked account" || { echo "$out" >&2; exit 1; }

# Man pages and shell completions to dist/docs
docs-gen:
    rm -rf dist/docs
    SOURCE_DATE_EPOCH=$(git log -1 --format=%ct) CGO_ENABLED=0 go run ./scripts/gendocs dist/docs

# Pack dist/<os>_<arch>/go-signal with docs into dist/go-signal_<version>_<os>_<arch>.tar.gz
package os arch: docs-gen
    ./scripts/package.sh "{{ version }}" {{ os }} {{ arch }}

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

# Run tests with coverage; -coverpkg lets the cmd tests count towards internal/*, minus the signaltest fake
test-coverage:
    go test -race -count=1 -coverpkg=$(go list ./... | grep -v /signaltest | paste -sd,) -coverprofile=coverage.out -covermode=atomic ./...

# Render coverage.out as coverage.html
coverage-html:
    go tool cover -html=coverage.out -o coverage.html

# Generate coverage-results.md from coverage.out
coverage-report:
    ./scripts/coverage-report.sh

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
    # .Dir is empty until the module is in the module cache (e.g. on a fresh CI runner).
    go mod download go.mau.fi/mautrix-signal
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
    rm -rf bin/ dist/ coverage.out coverage.html coverage-results.md

# Remove the static build's caches and libraries (third_party/.musl, third_party/lib-musl)
clean-static:
    rm -rf third_party/.musl third_party/lib-musl

# Show help
help:
    @just --list
