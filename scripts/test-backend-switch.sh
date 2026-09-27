#!/usr/bin/env bash
# Continue persisted sessions across the real cgo and purego builds in both directions.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT
CGO_ENABLED=1 go test -race -c -o "$test_dir/cgo" ./internal/store
CGO_ENABLED=0 go test -tags purego -c -o "$test_dir/purego" ./internal/store
for first in cgo purego; do
	second=purego
	if [[ $first == purego ]]; then
		second=cgo
	fi
	printf 'Testing %s -> %s database switching\n' "$first" "$second"
	export GOSIGNAL_TEST_BACKEND_DIR="$test_dir/$first-first"
	GOSIGNAL_TEST_BACKEND_ACTION=init "$test_dir/$first" -test.run '^TestBackendSwitchStep$' -test.v
	for backend in "$second" "$first" "$second"; do
		GOSIGNAL_TEST_BACKEND_ACTION=advance "$test_dir/$backend" -test.run '^TestBackendSwitchStep$' -test.v
	done
done
