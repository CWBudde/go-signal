#!/bin/sh
# Installs the go-signal release binary for this machine (Linux or macOS, amd64 or arm64):
#
#   curl -fsSL https://github.com/cwbudde/go-signal/releases/latest/download/install.sh | sh
#
# Environment:
#   GOSIGNAL_VERSION      release to install, e.g. 0.2.0 (default: the latest)
#   GOSIGNAL_INSTALL_DIR  where to put the binary (default: ~/.local/bin)
#
# Downloads the archive and SHA256SUMS from the GitHub release, checks the checksum and installs
# only the binary. Man pages and completions are in the archive (see the README).

set -eu

repo=https://github.com/cwbudde/go-signal
install_dir=${GOSIGNAL_INSTALL_DIR:-$HOME/.local/bin}

fail() {
	echo "go-signal install: $*" >&2
	exit 1
}

command -v curl >/dev/null 2>&1 || fail "curl is required"

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) fail "unsupported OS $(uname -s); on Windows, download the .zip from $repo/releases" ;;
esac

case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) fail "unsupported CPU $(uname -m)" ;;
esac

version=${GOSIGNAL_VERSION:-}
if [ -z "$version" ]; then
	# releases/latest redirects to releases/tag/vX.Y.Z.
	latest=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$repo/releases/latest") ||
		fail "cannot reach $repo"
	version=${latest##*/}
fi
version=${version#v}
case $version in
[0-9]*) ;;
*) fail "cannot determine the latest release (got '$version')" ;;
esac

name=go-signal_${version}_${os}_${arch}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "Downloading go-signal $version for $os/$arch"
curl -fsSL -o "$tmp/$name.tar.gz" "$repo/releases/download/v$version/$name.tar.gz" ||
	fail "no release archive $name.tar.gz"
curl -fsSL -o "$tmp/SHA256SUMS" "$repo/releases/download/v$version/SHA256SUMS" ||
	fail "no SHA256SUMS in release v$version"

want=$(awk -v f="$name.tar.gz" '$2 == f || $2 == "*" f { print $1 }' "$tmp/SHA256SUMS")
[ -n "$want" ] || fail "$name.tar.gz is not in SHA256SUMS"
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$name.tar.gz" | awk '{ print $1 }')
else
	got=$(shasum -a 256 "$tmp/$name.tar.gz" | awk '{ print $1 }')
fi
[ "$got" = "$want" ] || fail "checksum mismatch for $name.tar.gz"

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
mkdir -p "$install_dir"
# Replace rather than overwrite in place, so that a running go-signal keeps its binary.
cp "$tmp/$name/go-signal" "$install_dir/.go-signal.new"
chmod 755 "$install_dir/.go-signal.new"
mv -f "$install_dir/.go-signal.new" "$install_dir/go-signal"

echo "Installed $install_dir/go-signal"
case ":$PATH:" in
*":$install_dir:"*) "$install_dir/go-signal" version | head -n 1 ;;
*) echo "Add $install_dir to your PATH, e.g.: export PATH=\"$install_dir:\$PATH\"" ;;
esac
