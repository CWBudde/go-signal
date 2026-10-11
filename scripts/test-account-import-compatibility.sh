#!/usr/bin/env bash
# Offline Java-created protocol records, reopened across both Go backends.
# --java additionally requires real Java continuation; the default needs no Java.
set -euo pipefail
umask 077

fail() {
	printf 'account-import compatibility: %s\n' "$*" >&2
	exit 1
}
java_mode=0
case "$#:${1:-}" in
0:) ;;
1:--java) java_mode=1 ;;
*) fail 'usage: test-account-import-compatibility.sh [--java]' ;;
esac

command -v python3 >/dev/null || fail 'Python 3 required for fixture provenance'
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
corpus="$repo_root/internal/signal/accountimport/testdata/java-0.103.0"
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT
mkdir "$test_dir/tmp"
export TMPDIR="$test_dir/tmp"

# Generate the complete expected ledger before executing any protocol actions.
# Hash checking is required even when Java is not requested.
python3 - "$corpus" "$java_mode" "$test_dir/expected.tsv" <<'PY'
import hashlib, json, pathlib, re, sys

root, java_mode, output = pathlib.Path(sys.argv[1]), int(sys.argv[2]), pathlib.Path(sys.argv[3])
def fail(message):
    sys.exit('account-import compatibility: ' + message)
def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            fail('duplicate JSON key in fixture metadata')
        result[key] = value
    return result
try:
    provenance = json.loads((root / 'provenance.json').read_text(), object_pairs_hook=unique_object)
    markers = {'registry': 2, 'accountJSON': 11, 'sqlite': 31, 'javaLibsignal': '0.103.0'}
    if provenance.get('formatVersion') != 1 or provenance.get('supportedMarkers') != markers:
        fail('unsupported fixture provenance markers')
    hashes = provenance.get('fixtureSHA256', {})
    files = {'corpus.json', 'accounts.json', 'account.json', 'schema.sql', 'rows.sql'}
    if not isinstance(hashes, dict) or set(hashes) != files:
        fail('incomplete fixture checksum manifest')
    for name, expected in hashes.items():
        if not isinstance(expected, str) or not re.fullmatch('[0-9a-f]{64}', expected):
            fail('invalid fixture checksum')
        with (root / name).open('rb') as stream:
            actual = hashlib.file_digest(stream, 'sha256').hexdigest()
        if actual != expected:
            fail('fixture checksum mismatch: ' + name)
    corpus = json.loads((root / 'corpus.json').read_text(), object_pairs_hook=unique_object)
    if corpus.get('version') != 1:
        fail('unsupported corpus version')
    scenarios = corpus.get('scenarios')
    kinds = {'ordinary', 'last-resort', 'current-skipped', 'archived-only', 'archived-current', 'group-skipped'}
    expected_scenarios = {(namespace, kind) for namespace in ('ACI', 'PNI') for kind in kinds}
    if not isinstance(scenarios, list) or len(scenarios) != len(expected_scenarios):
        fail('incomplete scenario manifest')
    seen, ids, rows = set(), set(), []
    for scenario in scenarios:
        identity, namespace, kind = (scenario.get(field) for field in ('id', 'namespace', 'kind'))
        if not isinstance(identity, str) or not re.fullmatch('[a-z][a-z0-9-]*', identity) or identity in ids:
            fail('invalid or duplicate scenario ID')
        pair = (namespace, kind)
        if pair not in expected_scenarios or pair in seen:
            fail('unsupported or duplicate scenario namespace/kind')
        seen.add(pair)
        ids.add(identity)
        for first, second in (('native', 'purego'), ('purego', 'native')):
            def step(action, backend, leg='-'):
                rows.append((identity, namespace, first, action, backend, leg))
            step('init', first)
            step('advance', second)
            step('advance', first)
            step('advance', second)
            if java_mode:
                # The cloned Go peer and the retained Java peer must advance in
                # independent databases: their ratchet randomness is different.
                step('init', first, 'setup')
                group = kind == 'group-skipped'
                for leg in ('distribution', 'group-message') if group else ('reply', 'fresh-initiation'):
                    step('export-java', second, leg)
                    step('continue-java', 'java', leg)
                    step('consume-java', first, leg)
    if seen != expected_scenarios:
        fail('missing required scenario namespace/kind')
    header = ('scenario', 'namespace', 'startingorder', 'action', 'backend', 'javaLeg')
    output.write_text('\n'.join('\t'.join(row) for row in (header, *rows)) + '\n')
except (OSError, ValueError, TypeError, AttributeError) as error:
    fail('invalid or missing fixture metadata: ' + type(error).__name__)
PY

command -v go >/dev/null || fail 'Go required for both protocol backends'
ledger=${GOSIGNAL_IMPORT_COMPAT_LEDGER:-}
if [[ -z $ledger ]]; then
	# Keep this small evidence file when temporary binaries and databases are removed.
	ledger=$(mktemp "${test_dir%/*}/gosignal-import-compat-ledger.XXXXXXXX")
else
	mkdir -p "$(dirname "$ledger")"
fi
ledger="$(cd "$(dirname "$ledger")" && pwd)/$(basename "$ledger")"
printf 'scenario\tnamespace\tstartingorder\taction\tbackend\tjavaLeg\n' >"$ledger"
printf 'Account-import action ledger: %s\n' "$ledger"

java_wrapper="$repo_root/scripts/generate-account-import-fixtures.sh"
if [[ $java_mode == 1 ]]; then
	[[ -x $java_wrapper ]] || fail 'Java fixture wrapper required'
	if ! "$java_wrapper" --self-test >"$test_dir/java-self-test.log" 2>&1; then
		cat "$test_dir/java-self-test.log" >&2
		fail 'verified Java harness self-test failed'
	fi
fi

# Explicit tags keep an inherited GOFLAGS backend tag from selecting the pure-Go
# shim in the native binary. Both binaries run in separate processes per step.
CGO_ENABLED=1 CGO_LDFLAGS="-L $repo_root/third_party/lib ${CGO_LDFLAGS:-}" \
	go test -tags '' -race -c -o "$test_dir/native" ./internal/signal/accountimport
CGO_ENABLED=0 go test -tags libsignal_go -c -o "$test_dir/purego" ./internal/signal/accountimport

# Compiled Go tests retain the package's usual relative testdata paths.
cd "$repo_root/internal/signal/accountimport"

while IFS=$'\t' read -r scenario namespace first action backend java_leg; do
	[[ $scenario != scenario ]] || continue
	exchange="$test_dir/$scenario/$first-first"
	if [[ $java_leg != - ]]; then
		exchange="$test_dir/$scenario/$first-java"
	fi
	if [[ $action == init ]]; then
		printf 'Testing %s (%s), starting with %s\n' "$scenario" "$namespace" "$first"
		mkdir -p "$exchange"
	fi
	if [[ $backend == java ]]; then
		if ! "$java_wrapper" --continue "$exchange/java" >"$test_dir/action.log" 2>&1; then
			cat "$test_dir/action.log" >&2
			fail "$scenario/$first/$java_leg Java continuation failed"
		fi
	else
		if ! GOSIGNAL_IMPORT_COMPAT_DIR="$exchange" GOSIGNAL_IMPORT_COMPAT_ACTION="$action" \
			GOSIGNAL_IMPORT_COMPAT_SCENARIO="$scenario" "$test_dir/$backend" \
			-test.run '^TestJavaAccountImportBackendStep$' -test.v -test.count=1 -test.timeout=90s \
			>"$test_dir/action.log" 2>&1; then
			cat "$test_dir/action.log" >&2
			fail "$scenario/$first/$action/$backend failed"
		fi
		# A zero exit status with a skipped or missing test is not acceptance evidence.
		if ! grep -Eq '^--- PASS: TestJavaAccountImportBackendStep( |$)' "$test_dir/action.log"; then
			cat "$test_dir/action.log" >&2
			fail "$scenario/$first/$action/$backend subprocess test did not pass"
		fi
	fi
	printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$scenario" "$namespace" "$first" "$action" "$backend" "$java_leg" >>"$ledger"
done <"$test_dir/expected.tsv"

cmp -s "$test_dir/expected.tsv" "$ledger" || fail 'completed action ledger differs from the required scenario/backend/Java legs'
actions=$(($(wc -l <"$ledger") - 1))
java_legs=$((48 * java_mode))
printf 'Passed 12 scenarios in both starting orders: %s actions, %s Java legs. Ledger: %s\n' "$actions" "$java_legs" "$ledger"
