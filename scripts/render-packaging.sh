#!/usr/bin/env bash
# Renders a packaging template for a release: scripts/render-packaging.sh <template> <tag> <sums>
#
# Replaces @VERSION@ with the tag without its leading "v" and @SHA256_<OS>_<ARCH>@ (e.g.
# @SHA256_LINUX_AMD64@) with the checksum of go-signal_<version>_<os>_<arch>.tar.gz from the
# SHA256SUMS file. Fails when the template needs a checksum that the file doesn't have.

set -euo pipefail

if [[ $# -ne 3 ]]; then
	echo "usage: $0 <template> <tag> <sha256sums>" >&2
	exit 2
fi

template=$1
version=${2#v}
sums=$3

out=$(sed "s/@VERSION@/$version/g" "$template")

while read -r token; do
	platform=${token#@SHA256_}
	platform=${platform%@}
	os=$(echo "${platform%_*}" | tr '[:upper:]' '[:lower:]')
	arch=$(echo "${platform#*_}" | tr '[:upper:]' '[:lower:]')
	file=go-signal_${version}_${os}_${arch}.tar.gz
	sum=$(awk -v f="$file" '$2 == f || $2 == "*" f { print $1 }' "$sums")
	if [[ -z $sum ]]; then
		echo "no checksum for $file in $sums" >&2
		exit 1
	fi
	out=${out//"$token"/$sum}
done < <(grep -o '@SHA256_[A-Z0-9]*_[A-Z0-9]*@' "$template" | sort -u)

printf '%s\n' "$out"
