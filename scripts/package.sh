#!/usr/bin/env bash
# Packs a release archive: scripts/package.sh <version> <os> <arch>
#
# Takes $DIST/<os>_<arch>/go-signal and the man pages and completions from dist/docs (`just
# docs-gen`) and writes $DIST/$NAME_<version>_<os>_<arch>.tar.gz (.zip with go-signal.exe for
# windows). DIST defaults to dist, NAME to go-signal. A leading "v" is dropped from the version.

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

dist=${DIST:-dist}
exe=go-signal
[[ $os == windows ]] && exe=go-signal.exe
name=${NAME:-go-signal}_${version}_${os}_${arch}
stage=$dist/stage/$name

rm -rf "$stage"
mkdir -p "$stage"
cp "$dist/${os}_${arch}/$exe" LICENSE README.md "$stage/"
cp -r dist/docs/man dist/docs/completions "$stage/"
if [[ $os == linux ]]; then
	mkdir -p "$stage/contrib"
	cp -r contrib/systemd "$stage/contrib/"
fi

# Stable archive: sorted names, fixed owner and the commit's timestamp.
mtime=$(git log -1 --format=%ct 2>/dev/null || date +%s)
if [[ $os == windows ]]; then
	find "$stage" -exec touch -d "@$mtime" {} +
	rm -f "$dist/$name.zip"
	(cd "$dist/stage" && find "$name" | LC_ALL=C sort | zip -qX "../$name.zip" -@)
	rm -rf "$stage"
	echo "$dist/$name.zip"
	exit 0
fi
tar_flags=(--owner=0 --group=0 --numeric-owner --sort=name --mtime="@$mtime")
tar=tar
if [[ $os == darwin ]] && command -v gtar >/dev/null; then
	tar=gtar
elif ! tar --version 2>/dev/null | grep -q GNU; then
	tar_flags=() # bsdtar (macOS without gtar)
fi
"$tar" -C "$dist/stage" "${tar_flags[@]}" -czf "$dist/$name.tar.gz" "$name"
rm -rf "$stage"
echo "$dist/$name.tar.gz"
