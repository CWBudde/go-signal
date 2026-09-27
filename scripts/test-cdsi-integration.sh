#!/usr/bin/env bash
# Exercise the purego CDSI shim with the recorded enclave key. The expired-fixture
# exception is exposed only in disposable fork copies, never in a shipped module.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
shim_dir=$(go list -m -f '{{.Dir}}' go.mau.fi/mautrix-signal)
crypto_dir=$(go list -m -f '{{.Dir}}' github.com/cwbudde/libsignal-go)
test_dir=$(mktemp -d)
trap 'chmod -R u+w "$test_dir"; rm -rf "$test_dir"' EXIT
cp -R "$shim_dir" "$test_dir/mautrix-signal"
cp -R "$crypto_dir" "$test_dir/libsignal-go"
chmod -R u+w "$test_dir"
mkdir -p "$test_dir/libsignal-go/attest/shimtest"
cp scripts/testdata/cdsi_testhook.go "$test_dir/libsignal-go/attest/shimtest/testhook.go"
cp scripts/testdata/cdsi_integration_test.go "$test_dir/mautrix-signal/pkg/libsignalgo/"
cp "$test_dir/libsignal-go/attest/dcap/testdata/cds2_test.privatekey" \
	"$test_dir/mautrix-signal/pkg/libsignalgo/testdata/"
(cd "$test_dir" && GOWORK=off go work init "$root" "$test_dir/mautrix-signal" "$test_dir/libsignal-go")
GOWORK="$test_dir/go.work" go test -tags purego "$@" -count=1 \
	-run '^TestCDS(IIntegration|2ClientState)' go.mau.fi/mautrix-signal/pkg/libsignalgo
