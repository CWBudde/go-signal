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

# Build the binary with version info (pure-Go libsignal_go backend: no cgo, no Rust)
build:
    CGO_ENABLED=0 go build -tags libsignal_go -ldflags "{{ version_ldflags }}" -o bin/go-signal .

# Build with the cgo backend (libsignal_ffi.a from `just libsignal`, needs Rust and a C toolchain)
build-cgo:
    go build -ldflags "{{ version_ldflags }}" -o bin/go-signal .

# Cross-compiled release binary to dist/<os>_<arch>/, packaged as dist/go-signal_<version>_<os>_<arch>.*
build-release os arch: docs-gen
    CGO_ENABLED=0 GOOS={{ os }} GOARCH={{ arch }} go build -tags libsignal_go -trimpath -ldflags "-s -w {{ version_ldflags }}" -o dist/{{ os }}_{{ arch }}/go-signal{{ if os == "windows" { ".exe" } else { "" } }} .
    ./scripts/package.sh "{{ version }}" {{ os }} {{ arch }}

# Run the linux release binary for this machine's arch in a scratch container (the release image)
smoke-image:
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

# Vet, lint and test the purego build (cgo-only tests are excluded by their build tags;

# vet and lint include the integration suite, which only runs by hand)
check-purego:
    CGO_ENABLED=0 go vet -tags integration,libsignal_go ./...
    CGO_ENABLED=0 golangci-lint run --timeout 5m --build-tags integration,libsignal_go
    CGO_ENABLED=0 go test -tags libsignal_go -count=1 ./...
    just check-aes-asm

# Assert that the purego release targets use the stdlib's AES assembly (CT-02): it must be

# selected with the libsignal_go tag, and dropped with purego (which proves the check can fail).
check-aes-asm:
    #!/usr/bin/env bash
    set -euo pipefail
    for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
        export GOOS=${target%/*} GOARCH=${target#*/}
        with=$(CGO_ENABLED=0 go list -tags libsignal_go -f '{{ "{{" }}.SFiles{{ "}}" }}' crypto/internal/fips140/aes)
        without=$(CGO_ENABLED=0 go list -tags purego -f '{{ "{{" }}.SFiles{{ "}}" }}' crypto/internal/fips140/aes)
        if [[ $with == "[]" || $without != "[]" ]]; then
            echo "$target: AES assembly with libsignal_go: $with, with purego: $without" >&2
            exit 1
        fi
        echo "$target: $with"
    done

# Test the pinned forks: libsignal-go (committed vectors, unit tests) and the purego libsignalgo shim
test-fork:
    CGO_ENABLED=0 go test -count=1 github.com/cwbudde/libsignal-go/...
    CGO_ENABLED=0 go test -tags libsignal_go -count=1 github.com/cwbudde/mautrix-signal/pkg/libsignalgo/...
    CGO_ENABLED=0 scripts/test-cdsi-integration.sh
    CGO_ENABLED=0 scripts/test-zkgroup-integration.sh -tags libsignal_go
    CGO_ENABLED=0 scripts/test-websocket-lifecycle.sh -tags libsignal_go
    CGO_ENABLED=0 go test -tags libsignal_go -count=1 -timeout 30s -run '^(TestKeyCheckLifecycle|TestDeliveryReceiptAfterAck|TestSenderKeyTrust|TestVerifiedSync|TestStorageUpdateHandler)' github.com/cwbudde/mautrix-signal/pkg/signalmeow

# Differential tests (cgo): purego vs libsignal in go-signal, the shim's cgo side, signalmeow's zkgroup paths.

test-diff:
    go test -race -count=1 -run '^TestDiff' ./internal/signal/
    go test -race -count=1 github.com/cwbudde/mautrix-signal/pkg/libsignalgo/...
    scripts/test-backend-switch.sh
    scripts/test-account-import-compatibility.sh
    scripts/test-cdsi-integration.sh -race
    scripts/test-zkgroup-integration.sh -race
    scripts/test-websocket-lifecycle.sh -race
    scripts/test-websocket-lifecycle.sh -tags libsignal_go -race
    go test -race -count=1 -timeout 30s -run '^(TestKeyCheckLifecycle|TestDeliveryReceiptAfterAck|TestSenderKeyTrust|TestVerifiedSync|TestStorageUpdateHandler)' github.com/cwbudde/mautrix-signal/pkg/signalmeow
    go test -tags libsignal_go -race -count=1 -timeout 30s -run '^(TestKeyCheckLifecycle|TestDeliveryReceiptAfterAck|TestSenderKeyTrust|TestVerifiedSync|TestStorageUpdateHandler)' github.com/cwbudde/mautrix-signal/pkg/signalmeow

# Opt-in integration suite against Signal's production servers, cgo then libsignal_go on the same

# account (docs/dev.md, "Integration tests"). Needs GOSIGNAL_IT_DATA_DIR and GOSIGNAL_IT_PEER.
test-integration:
    go test -count=1 -v -timeout 20m -tags integration -run '^TestIntegration' ./internal/signal/
    go test -count=1 -v -timeout 20m -tags integration -run '^TestIntegration' ./cmd/
    CGO_ENABLED=0 go test -count=1 -v -timeout 20m -tags integration,libsignal_go -run '^TestIntegration' ./internal/signal/
    CGO_ENABLED=0 go test -count=1 -v -timeout 20m -tags integration,libsignal_go -run '^TestIntegration' ./cmd/

# Build the binary without version info (faster for development)
build-dev:
    CGO_ENABLED=0 go build -tags libsignal_go -o bin/go-signal .

# Test that the project can build successfully
test-can-build:
    @just build

# Run the application
run *args:
    CGO_ENABLED=0 go run -tags libsignal_go . {{ args }}

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
    go mod download github.com/cwbudde/mautrix-signal
    dir=$(go list -m -f '{{{{.Dir}}' github.com/cwbudde/mautrix-signal)
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

# Show help
help:
    @just --list
