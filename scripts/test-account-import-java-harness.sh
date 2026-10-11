#!/usr/bin/env bash
# Negative integration checks for the real Java fixture wrapper. No accounts or servers.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
wrapper="$repo_root/scripts/generate-account-import-fixtures.sh"
provenance="$repo_root/internal/signal/accountimport/testdata/java-0.103.0/provenance.json"
if [[ $# != 0 ]]; then
	[[ $# == 2 && $1 == --provenance ]] || {
		printf 'usage: test-account-import-java-harness.sh [--provenance <file>]\n' >&2
		exit 1
	}
	provenance=$2
fi
test_root=$(mktemp -d "${TMPDIR:-/tmp}/gosignal-java-wrapper.XXXXXXXX")
trap 'rm -rf "$test_root"' EXIT

expect_failure() {
	local label=$1 diagnostic=$2
	shift 2
	if "$@" >"$test_root/output" 2>&1; then
		printf 'FAIL: %s unexpectedly succeeded\n' "$label" >&2
		exit 1
	fi
	if ! grep -Fq -- "$diagnostic" "$test_root/output"; then
		printf 'FAIL: %s did not report %s\n' "$label" "$diagnostic" >&2
		cat "$test_root/output" >&2
		exit 1
	fi
	printf 'PASS: %s\n' "$label"
}

# Removing fail-closed tool checks must make these tests fail before any output exists.
expect_failure 'missing Java' 'Java 25 JDK required' env \
	GOSIGNAL_IMPORT_JAVA_HOME="$test_root/missing-java" \
	bash "$wrapper" "$test_root/missing-java-output"
test ! -e "$test_root/missing-java-output"

expect_failure 'missing Maven' 'Maven required' env \
	GOSIGNAL_IMPORT_MAVEN="$test_root/missing-maven" \
	bash "$wrapper" "$test_root/missing-maven-output"
test ! -e "$test_root/missing-maven-output"

mkdir "$test_root/existing"
printf 'keep me\n' >"$test_root/existing/sentinel"
expect_failure 'existing output' 'output path already exists' \
	bash "$wrapper" "$test_root/existing"
test "$(cat "$test_root/existing/sentinel")" = 'keep me'

ln -s "$test_root/absent" "$test_root/dangling"
expect_failure 'dangling output symlink' 'output path already exists' \
	bash "$wrapper" "$test_root/dangling"
test -L "$test_root/dangling"

# Corrupt only a disposable cache, never the verified developer dependency.
bad_cache="$test_root/bad-cache"
mkdir -p "$bad_cache/org/signal/libsignal-client/0.103.0"
printf 'deliberately corrupt jar\n' >"$bad_cache/org/signal/libsignal-client/0.103.0/libsignal-client-0.103.0.jar"
expect_failure 'artifact checksum mismatch' 'libsignal artifact checksum mismatch' env \
	GOSIGNAL_IMPORT_MAVEN_CACHE="$bad_cache" \
	bash "$wrapper" "$test_root/checksum-output"
test ! -e "$test_root/checksum-output"

# A compact JAR carrying an unverified JNI resource must fail before generation.
verified_cache=${GOSIGNAL_IMPORT_MAVEN_CACHE:-$repo_root/scripts/account-import-java/target/m2}
verified_jar="$verified_cache/org/signal/libsignal-client/0.103.0/libsignal-client-0.103.0.jar"
[[ -f $verified_jar ]] || {
	printf 'FAIL: negative checks require the verified cached libsignal jar\n' >&2
	exit 1
}
python3 - "$verified_jar" "$bad_cache/org/signal/libsignal-client/0.103.0/libsignal-client-0.103.0.jar" <<'PY'
import sys, zipfile
with zipfile.ZipFile(sys.argv[1]) as original, zipfile.ZipFile(sys.argv[2], 'w') as jar:
    # Keep a real class entry without duplicating the 156 MB native bundle.
    name = next(name for name in original.namelist() if name.endswith('.class'))
    jar.writestr(name, original.read(name))
    jar.writestr('libsignal_jni_testing_amd64.so', b'deliberately corrupt JNI library')
PY
expect_failure 'bundled JNI tampering' 'libsignal artifact checksum mismatch' env \
	GOSIGNAL_IMPORT_MAVEN_CACHE="$bad_cache" \
	bash "$wrapper" "$test_root/jni-output"
test ! -e "$test_root/jni-output"

expect_failure 'external JNI override' 'external Java/JNI overrides are refused' env \
	JAVA_TOOL_OPTIONS='-Djava.library.path=/unverified-native-code' \
	bash "$wrapper" "$test_root/override-output"
test ! -e "$test_root/override-output"

# Real Java marker validation must fail before a destination is created. Compile
# and run the real self-test first; corrupt only a private provenance copy.
bash "$wrapper" --self-test
java_home=${GOSIGNAL_IMPORT_JAVA_HOME:-${JAVA_HOME:-}}
if [[ -z $java_home ]]; then
	java_home=$(python3 -c 'import pathlib,shutil; print(pathlib.Path(shutil.which("java")).resolve().parent.parent)')
fi
python3 - "$repo_root" "$java_home/bin/java" "$provenance" "$test_root" <<'PY'
import hashlib, json, pathlib, subprocess, sys
repo, java, provenance, root = map(pathlib.Path, sys.argv[1:])
harness = repo/'scripts/account-import-java'
classpath = str(harness/'target/classes')+':'+str(harness/'target/test-classes')+':'+(harness/'target/runtime-classpath.txt').read_text().strip()
original = json.loads(provenance.read_text())
def class_hashes():
    return {str(p):hashlib.sha256(p.read_bytes()).hexdigest() for p in (harness/'target').rglob('*.class')}
before = class_hashes()
assert before, 'Java classes were not compiled'
for field, value in [('registry',3), ('accountJSON',12), ('sqlite',32), ('javaLibsignal','0.104.0')]:
    changed = json.loads(json.dumps(original))
    changed['supportedMarkers'][field] = value
    private = root/('bad-'+field+'.json')
    private.write_text(json.dumps(changed)+'\n')
    output = root/('bad-'+field+'-output')
    args = [str(java), '-ea', '--enable-native-access=ALL-UNNAMED',
            '-Djava.io.tmpdir='+str(root), '-Djava.library.path='+str(root/'empty-native'),
            '-Dgosignal.import.provenance='+str(private), '-cp', classpath,
            'org.gosignal.accountimport.ImportFixtures', 'generate', str(output)]
    run = subprocess.run(args, capture_output=True, text=True)
    assert run.returncode != 0 and 'unsupported source format markers' in run.stderr and not output.exists(), field
    print('PASS: unsupported '+field+' marker; no output created')
assert class_hashes() == before, 'marker checks modified compiled classes'
PY

# A second build must not remove classes from a still-running harness. Exercise
# the real wrapper twice concurrently, including both compilation and JNI work.
python3 - "$repo_root" "$wrapper" "$test_root" <<'PY'
import hashlib, pathlib, subprocess, sys, time
repo, wrapper, root = map(pathlib.Path, sys.argv[1:])
harness = repo/'scripts/account-import-java'
def hashes(directory, suffix):
    return {str(p.relative_to(directory)):hashlib.sha256(p.read_bytes()).hexdigest()
            for p in directory.rglob('*'+suffix)}
sources = hashes(harness/'src', '.java')
classes = hashes(harness/'target/classes', '.class')
assert classes and 'org/gosignal/accountimport/ImportFixtures$Seed.class' in classes
deadline = time.monotonic()+240
runs = []
try:
    for index in range(2):
        log = (root/('concurrent-self-test-'+str(index)+'.log')).open('w')
        runs.append((subprocess.Popen(['bash', str(wrapper), '--self-test'], stdout=log, stderr=subprocess.STDOUT), log))
    for process, log in runs:
        assert process.wait(timeout=max(1, deadline-time.monotonic())) == 0, 'concurrent real Java self-test failed; see '+log.name
finally:
    for process, log in runs:
        if process.poll() is None:
            process.kill()
            process.wait()
        log.close()
assert hashes(harness/'src', '.java') == sources, 'concurrent wrappers changed Java sources'
assert hashes(harness/'target/classes', '.class') == classes, 'concurrent wrappers lost or changed compiled classes'
print('PASS: simultaneous real Java self-tests; both passed and classes survived')
PY

printf 'Java harness wrapper negative checks passed.\n'
