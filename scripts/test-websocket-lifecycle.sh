#!/usr/bin/env bash
# Opt-in failing contract probes for the pinned fork; see docs/websocket-lifecycle.md.
# Test helpers are added only to a disposable copy, never the module cache or fork.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
export GOWORK=off
go mod download github.com/cwbudde/mautrix-signal
module_dir=$(go list -m -f '{{.Dir}}' github.com/cwbudde/mautrix-signal)
test_dir=$(mktemp -d)
trap 'chmod -R u+w "$test_dir"; rm -rf "$test_dir"' EXIT
cp -r "$module_dir" "$test_dir/mautrix-signal"
chmod -R u+w "$test_dir/mautrix-signal"
cp "$root/scripts/testdata/websocket_lifecycle_test.go" "$test_dir/mautrix-signal/pkg/signalmeow/web/"
cp "$root/scripts/testdata/websocket_lifecycle_export_test.go" "$test_dir/mautrix-signal/pkg/signalmeow/web/"
cd "$test_dir/mautrix-signal"
go test -count=1 -timeout 30s -run '^TestLifecycle' "$@" ./pkg/signalmeow/web
