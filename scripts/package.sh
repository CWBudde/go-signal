#!/usr/bin/env bash
# Packs a release archive: scripts/package.sh <version> <os> <arch>
#
# Takes dist/<os>_<arch>/go-signal and the man pages and completions from dist/docs (`just
# docs-gen`) and writes dist/go-signal_<version>_<os>_<arch>.tar.gz. A leading "v" is dropped from
# the version.

set -euo pipefail

if [[ $# -ne 3 ]]; then
	echo "usage: $0 <version> <os> <arch>" >&2
	exit 2
fi

version=${1#v}
os=$2
arch=$3

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

name=go-signal_${version}_${os}_${arch}
stage=dist/stage/$name

rm -rf "$stage"
mkdir -p "$stage"
cp "dist/${os}_${arch}/go-signal" LICENSE README.md "$stage/"
cp -r dist/docs/man dist/docs/completions "$stage/"
if [[ $os == linux ]]; then
	mkdir -p "$stage/contrib"
	cp -r contrib/systemd "$stage/contrib/"
fi

# Stable archive: sorted names, fixed owner and the commit's timestamp.
mtime=$(git log -1 --format=%ct 2>/dev/null || date +%s)
tar_flags=(--owner=0 --group=0 --numeric-owner --sort=name --mtime="@$mtime")
tar=tar
if [[ $os == darwin ]] && command -v gtar >/dev/null; then
	tar=gtar
elif ! tar --version 2>/dev/null | grep -q GNU; then
	tar_flags=() # bsdtar (macOS without gtar)
fi
"$tar" -C dist/stage "${tar_flags[@]}" -czf "dist/$name.tar.gz" "$name"
rm -rf "$stage"
echo "dist/$name.tar.gz"
