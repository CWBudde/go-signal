#!/usr/bin/env bash
# Synthetic protocol harness; exchanges are offline and release builds need no Java.
set -euo pipefail
umask 077

fail() {
	printf 'account-import fixtures: %s\n' "$*" >&2
	exit 1
}
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
harness="$repo_root/scripts/account-import-java"
action=generate
destination=
case "${1:-}" in
--self-test)
	[[ $# == 1 ]] || fail 'usage: generate-account-import-fixtures.sh --self-test'
	action=self-test
	;;
--continue)
	[[ $# == 2 ]] || fail 'usage: generate-account-import-fixtures.sh --continue <exchange-dir>'
	action='continue'
	destination=$2
	[[ -d $destination ]] || fail 'exchange directory does not exist'
	;;
-* | '') fail 'usage: generate-account-import-fixtures.sh <new-output-dir>' ;;
*)
	[[ $# == 1 ]] || fail 'usage: generate-account-import-fixtures.sh <new-output-dir>'
	destination=$1
	[[ ! -e $destination && ! -L $destination ]] || fail 'output path already exists'
	;;
esac

# JVM option environment variables can inject a different JNI library or agent.
for option in JAVA_TOOL_OPTIONS _JAVA_OPTIONS JDK_JAVA_OPTIONS MAVEN_OPTS MAVEN_ARGS LD_PRELOAD DYLD_INSERT_LIBRARIES; do
	[[ -z ${!option:-} ]] || fail 'external Java/JNI overrides are refused'
done
command -v python3 >/dev/null || fail 'Python 3 required for checksum provenance'

java_home=${GOSIGNAL_IMPORT_JAVA_HOME:-${JAVA_HOME:-}}
if [[ -z $java_home ]]; then
	java_command=$(command -v java || true)
	[[ -n $java_command ]] || fail 'Java 25 JDK required; set GOSIGNAL_IMPORT_JAVA_HOME'
	java_home=$(python3 -c 'import pathlib,sys; print(pathlib.Path(sys.argv[1]).resolve().parent.parent)' "$java_command")
fi
[[ -x $java_home/bin/java && -x $java_home/bin/javac ]] || fail 'Java 25 JDK required; set GOSIGNAL_IMPORT_JAVA_HOME'
java_version=$("$java_home/bin/java" -version 2>&1)
javac_version=$("$java_home/bin/javac" -version 2>&1)
[[ $java_version =~ version\ \"25([.\"]|$) && $javac_version =~ javac\ 25([.]|$) ]] || fail 'Java 25 JDK required'
maven=${GOSIGNAL_IMPORT_MAVEN:-$(command -v mvn || true)}
[[ -n $maven && -x $maven ]] || fail 'Maven required; set GOSIGNAL_IMPORT_MAVEN'

cache=${GOSIGNAL_IMPORT_MAVEN_CACHE:-$harness/target/m2}
mkdir -p "$cache" "$harness/target"
command -v flock >/dev/null || fail 'flock required for isolated harness builds'
# Maven and target/classes belong to this harness. Hold the lock through Java
# execution so another invocation cannot delete classes that are still loading.
exec 9>"$harness/target/.harness.lock"
flock 9
cache=$(cd "$cache" && pwd)
runtime=$(mktemp -d "$harness/target/runtime.XXXXXXXX")
trap 'rm -rf "$runtime"' EXIT
mkdir "$runtime/tmp" "$runtime/user-home" "$runtime/empty-native"
export JAVA_HOME="$java_home"
export MAVEN_OPTS="-Duser.home=$runtime/user-home -Djava.io.tmpdir=$runtime/tmp"
unset CLASSPATH LD_LIBRARY_PATH DYLD_LIBRARY_PATH
maven_version=$("$maven" -version 2>&1)

# Check an already cached artifact before Maven can consume or replace it.
verify_signal() {
	python3 - "$cache" "$runtime/jni.json" <<'PY'
import hashlib, json, pathlib, sys, zipfile
cache, report = map(pathlib.Path, sys.argv[1:])
helper = cache / 'com/github/turasa/util-jvm/2.15.3_unofficial_154'
for name, checksum in {
    'util-jvm-2.15.3_unofficial_154.jar': 'a81582ba785e84b5cd8071a061056cb9198057aa053d8798c16a4776c6c1a2ce',
    'util-jvm-2.15.3_unofficial_154.pom': '6cf1925dd84abcbbb83815f517f13dffa136884455ebe54984e9e1974ab32583',
    'util-jvm-2.15.3_unofficial_154-sources.jar': '839cd619646031c7d78838d1753dbfd6d76f57c36f80ebebf0e22c476f99ecfd',
}.items():
    path = helper / name
    if path.exists():
        with path.open('rb') as stream:
            if hashlib.file_digest(stream, 'sha256').hexdigest() != checksum:
                sys.exit('account-import fixtures: UUID helper artifact checksum mismatch')
jar = cache / 'org/signal/libsignal-client/0.103.0/libsignal-client-0.103.0.jar'
if not jar.exists():
    sys.exit(0)
expected = 'f8608205de269abb88d8ffed4c01cf4bc17b71a54f04703fabb566d160a79deb'
if hashlib.file_digest(jar.open('rb'), 'sha256').hexdigest() != expected:
    sys.exit('account-import fixtures: libsignal artifact checksum mismatch')
native = {
    'libsignal_jni_aarch64.dylib': '883713070051d4d8a4dbe525caa75413910669e9f992a8930ab76789488b2cb0',
    'libsignal_jni_amd64.dylib': 'edfb375d4c33b83d6c2beb74165a9d6260d06b19777ad366220dde3e51df59aa',
    'libsignal_jni_amd64.so': 'e0f4e783c25d0eea3252d9da36dd1dc9752c075dd5c10d1d3f30d9798346050b',
    'libsignal_jni_testing_aarch64.dylib': '8c871590822f35b90c1f2573e564ad2d9d2e9bce8f92e81f3917816784f87143',
    'libsignal_jni_testing_amd64.dylib': '8680a862f96e0dff643f68980d129b79c2d29106c4348a179886dbdc5c23650b',
    'libsignal_jni_testing_amd64.so': '7c4c1c68bc0d9441c03fa7a4b7566d7c0e1a194d093c857d4fbc93504146f582',
    'signal_jni_amd64.dll': 'aac934ee61a5e01c3757d54f0207f3aca5a48fb4bb50a0ebf7f6ece1a341a71d',
    'signal_jni_testing_amd64.dll': '7cafbc987d96e271095d6baf3b2c824ffbddfe9f1d2d084ef2e53e942c90aa8c',
}
with zipfile.ZipFile(jar) as archive:
    for resource, checksum in native.items():
        with archive.open(resource) as stream:
            if hashlib.file_digest(stream, 'sha256').hexdigest() != checksum:
                sys.exit('account-import fixtures: embedded JNI checksum mismatch')
report.write_text(json.dumps([{'resource': r, 'sha256': h} for r, h in native.items()], indent=2)+'\n')
PY
}
verify_signal

# Rebuild classes to prevent deleted/renamed sources surviving incremental compilation.
rm -rf "$harness/target/classes" "$harness/target/test-classes"
"$maven" --batch-mode --no-transfer-progress -f "$harness/pom.xml" \
	"-Dmaven.repo.local=$cache" \
	org.apache.maven.plugins:maven-compiler-plugin:3.14.0:compile \
	org.apache.maven.plugins:maven-compiler-plugin:3.14.0:testCompile \
	org.apache.maven.plugins:maven-dependency-plugin:3.8.1:build-classpath \
	"-Dmdep.outputFile=$harness/target/runtime-classpath.txt" -Dmdep.regenerateFile=true >"$runtime/maven.log" 2>&1 || {
	cat "$runtime/maven.log" >&2
	fail 'Java harness compilation/dependency resolution failed'
}
verify_signal
[[ -f $runtime/jni.json ]] || fail 'verified libsignal 0.103.0 artifact is missing'

python3 - "$repo_root" "$cache" "$runtime" "$java_version" "$javac_version" "$maven_version" <<'PY'
import hashlib, json, pathlib, shutil, subprocess, sys
repo, cache, runtime = map(pathlib.Path, sys.argv[1:4])
harness = repo / 'scripts/account-import-java'
def sha(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()
artifacts = []
for path in sorted(cache.rglob('*')):
    if path.suffix not in ('.jar', '.pom') or not path.is_file():
        continue
    parts = path.relative_to(cache).parts
    if len(parts) < 4:
        continue
    artifacts.append({'coordinate': ':'.join(('.'.join(parts[:-3]), parts[-3], parts[-2])),
                      'path': str(path.relative_to(cache)), 'sha256': sha(path)})
sources = [harness/'pom.xml', repo/'scripts/generate-account-import-fixtures.sh']
sources += sorted((harness/'src').rglob('*.java'))
sources += sorted(p for p in (harness/'src/main/resources').rglob('*') if p.is_file())
resources = harness/'src/main/resources'
if resources.exists():
    shutil.copytree(resources, harness/'target/classes', dirs_exist_ok=True)
def revision(directory):
    return subprocess.check_output(['git', '-C', str(directory), 'rev-parse', 'HEAD'], text=True).strip()
provenance = {'formatVersion': 1, 'artifacts': artifacts,
              'artifactManifestScope': 'All JAR/POM files in the private Maven cache after resolution, including previously resolved tooling.',
              'embeddedJNI': json.loads((runtime/'jni.json').read_text()),
              'toolchain': dict(zip(('java', 'javac', 'maven'), sys.argv[4:])),
              'generatorSources': [{'path': str(p.relative_to(repo)), 'sha256': sha(p)} for p in sources],
              'parentRevision': revision(repo), 'sourceReferenceRevision': revision(repo/'reference/signal-cli'),
              'supportedMarkers': {'registry': 2, 'accountJSON': 11, 'sqlite': 31, 'javaLibsignal': '0.103.0'}}
(runtime/'provenance.json').write_text(json.dumps(provenance, indent=2)+'\n')
PY

classpath="$harness/target/classes:$harness/target/test-classes:$(cat "$harness/target/runtime-classpath.txt")"
arguments=("$action")
if [[ -n $destination ]]; then arguments+=("$destination"); fi
"$java_home/bin/java" -ea --enable-native-access=ALL-UNNAMED \
	"-Duser.home=$runtime/user-home" "-Djava.io.tmpdir=$runtime/tmp" \
	"-Djava.library.path=$runtime/empty-native" \
	"-Dgosignal.import.provenance=$runtime/provenance.json" \
	-cp "$classpath" org.gosignal.accountimport.ImportFixtures "${arguments[@]}"
