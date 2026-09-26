#!/bin/sh
# Builds a fully static go-signal (musl) for the architecture of the machine it runs on.
#
# Runs inside a golang:<version>-alpine container with the repository mounted at /src; see
# `just build-static`, which also passes VERSION, COMMIT, BUILD_DATE, HOST_UID and HOST_GID.
#
# 1. libsignal_ffi.a for musl goes to third_party/lib-musl/<arch>/. As with `just libsignal`, a
#    SHA stamp next to it skips the cargo build while the submodule commit is unchanged.
# 2. go-signal is linked with -extldflags -static to dist/linux_<arch>/go-signal and checked
#    for being static.
#
# Rust, cargo and Go caches live under third_party/.musl/ (gitignored), so that repeated local
# runs don't download everything again and nothing root-owned lands in the user's own caches.
# The leading dot keeps the Go module cache out of `go ... ./...`.

set -eu

cd /src

case "$(uname -m)" in
x86_64) arch=amd64 ;;
aarch64) arch=arm64 ;;
*)
	echo "unsupported architecture: $(uname -m)" >&2
	exit 1
	;;
esac

cache=/src/third_party/.musl
lib=/src/third_party/lib-musl/$arch
out=/src/dist/linux_$arch

export CARGO_HOME=$cache/cargo
export RUSTUP_HOME=$cache/rustup
export CARGO_TARGET_DIR=$cache/target-$arch
export GOMODCACHE=$cache/gomod
export GOCACHE=$cache/gocache-$arch
export PATH="$CARGO_HOME/bin:$PATH"

# Hand everything we created back to the caller, also when a step fails.
cleanup() {
	if [ -n "${HOST_UID:-}" ]; then
		chown -R "$HOST_UID:${HOST_GID:-$HOST_UID}" "$cache" /src/third_party/lib-musl /src/dist 2>/dev/null || true
	fi
}
trap cleanup EXIT

apk add --no-cache build-base cmake clang-dev protobuf-dev perl linux-headers zlib-static file git

# git refuses to work in a repository owned by another user (the bind mount).
git config --global --add safe.directory '*'

sha=$(git -C third_party/libsignal rev-parse HEAD)
if [ -f "$lib/libsignal_ffi.a" ] && [ "$(cat "$lib/libsignal_ffi.sha" 2>/dev/null)" = "$sha" ]; then
	echo "libsignal_ffi.a ($arch, musl) is up to date ($sha)"
else
	if ! command -v rustup >/dev/null; then
		apk add --no-cache rustup
		rustup-init -y --no-modify-path --profile minimal --default-toolchain none
	fi
	# Proc-macros are dylibs, which musl's default crt-static rules out on a musl host. The
	# setting doesn't matter for the staticlib itself: it links no libc.
	(cd third_party/libsignal && RUSTFLAGS="-C target-feature=-crt-static" cargo build -p libsignal-ffi --release)
	mkdir -p "$lib"
	cp "$CARGO_TARGET_DIR/release/libsignal_ffi.a" "$lib/"
	echo "$sha" >"$lib/libsignal_ffi.sha"
fi

pkg=github.com/cwbudde/go-signal/cmd
mkdir -p "$out"
CGO_ENABLED=1 CGO_LDFLAGS="-L$lib" go build \
	-trimpath -buildvcs=false \
	-tags netgo,osusergo,timetzdata,sqlite_omit_load_extension \
	-ldflags "-s -w -linkmode external -extldflags -static \
		-X $pkg.Version=${VERSION:-dev} -X $pkg.GitCommit=${COMMIT:-unknown} -X $pkg.BuildDate=${BUILD_DATE:-unknown}" \
	-o "$out/go-signal" .

file "$out/go-signal"
if ! file "$out/go-signal" | grep -q 'statically linked'; then
	echo "go-signal is not statically linked" >&2
	exit 1
fi
"$out/go-signal" version
