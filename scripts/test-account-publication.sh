#!/usr/bin/env bash
# End a publishing process after each durable step with one build (cgo or purego), then recover
# and check the data dir with the other build, in both directions.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT
CGO_ENABLED=1 go test -race -c -o "$test_dir/cgo" ./internal/store
CGO_ENABLED=0 go test -tags libsignal_go -c -o "$test_dir/purego" ./internal/store
crash_exit=17
steps=(allocated built marked intent renamed synced registered retiring)
helper() {
	local backend=$1 action=$2 data=$3 step=$4 status=0
	GOSIGNAL_TEST_REGISTRY_DIR=$data GOSIGNAL_TEST_REGISTRY_ACTION=$action GOSIGNAL_TEST_PUBLISH_STEP=$step \
		"$test_dir/$backend" -test.run '^TestRegistryHelperProcess$' -test.v >"$test_dir/step.log" 2>&1 || status=$?
	local want=0
	if [[ $action == crash ]]; then
		want=$crash_exit
	fi
	if [[ $status -ne $want ]] || { [[ $action == check ]] && ! grep -q -- '--- PASS: TestRegistryHelperProcess' "$test_dir/step.log"; }; then
		cat "$test_dir/step.log"
		printf '%s %s after %s: exit %d, want %d\n' "$backend" "$action" "$step" "$status" "$want" >&2
		exit 1
	fi
}
runs=0
for first in cgo purego; do
	second=purego
	if [[ $first == purego ]]; then
		second=cgo
	fi
	for step in "${steps[@]}"; do
		data="$test_dir/$first-$step"
		mkdir -p "$data"
		helper "$first" crash "$data" "$step"
		helper "$second" check "$data" "$step"
		runs=$((runs + 1))
	done
	printf 'crash with %s, recover with %s: %d steps\n' "$first" "$second" "${#steps[@]}"
done
printf 'account publication: %d crash/recovery runs\n' "$runs"
