#!/usr/bin/env bash
# Allocate prekey IDs across the real cgo and purego builds in both directions, including a
# process that dies right after a committed reservation, and require the exact action ledger.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT
CGO_ENABLED=1 go test -race -c -o "$test_dir/cgo" ./internal/store
CGO_ENABLED=0 go test -tags libsignal_go -c -o "$test_dir/purego" ./internal/store
abort_exit=17
step() {
	local backend=$1 action=$2 status=0
	GOSIGNAL_TEST_ALLOCATOR_ACTION=$action "$test_dir/$backend" \
		-test.run '^TestPreKeyAllocatorBackendStep$' -test.v >"$test_dir/step.log" 2>&1 || status=$?
	local want=0
	if [[ $action == abort ]]; then
		want=$abort_exit
	fi
	if [[ $status -ne $want ]] || { [[ $action != abort ]] && ! grep -q -- '--- PASS: TestPreKeyAllocatorBackendStep' "$test_dir/step.log"; }; then
		cat "$test_dir/step.log"
		printf '%s %s: exit %d, want %d and a passing step\n' "$backend" "$action" "$status" "$want" >&2
		exit 1
	fi
}
for first in cgo purego; do
	second=purego
	if [[ $first == purego ]]; then
		second=cgo
	fi
	printf 'Testing %s -> %s prekey allocation\n' "$first" "$second"
	export GOSIGNAL_TEST_ALLOCATOR_DIR="$test_dir/$first-first"
	mkdir -p "$GOSIGNAL_TEST_ALLOCATOR_DIR"
	expected=("$first init" "$second reserve" "$first abort" "$second reserve" "$first reserve" "$second check")
	for entry in "${expected[@]}"; do
		step $entry
	done
	actual=$(awk '{print $1, $2}' "$GOSIGNAL_TEST_ALLOCATOR_DIR/allocator-ledger")
	if [[ $actual != "$(printf '%s\n' "${expected[@]}")" ]]; then
		printf 'unexpected action ledger:\n%s\n' "$actual" >&2
		exit 1
	fi
	printf '%s\n' "$actual" | sed 's/^/  /'
done
printf 'prekey allocator ledger: %d actions\n' $((2 * ${#expected[@]}))
