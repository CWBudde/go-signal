# Development

## Prerequisites

- Go (version from `go.mod`)
- Rust via [rustup](https://rustup.rs). The libsignal submodule pins the toolchain in its
  `rust-toolchain` file, so rustup installs the right version on first build.
- A C toolchain for CGO (gcc or clang), plus `cmake` and `protoc` for libsignal's build
- [just](https://github.com/casey/just)

## libsignal

go-signal talks to Signal through `signalmeow`, whose `libsignalgo` bindings link against
`libsignal_ffi.a` (CGO). That static library is built from the `third_party/libsignal` submodule:

```sh
git submodule update --init --depth 1 third_party/libsignal
just libsignal   # cargo build of libsignal-ffi, copied to third_party/lib/
just build
```

The justfile exports `CGO_ENABLED=1` and `CGO_LDFLAGS=-L third_party/lib`. When you run `go`
directly instead of through `just`, set `CGO_LDFLAGS` yourself. `just libsignal` records the
submodule SHA it built next to the library and skips the cargo build while it still matches.

`CGO_ENABLED=0 go test ./...` works without the library for the pure-Go packages.

### Upgrading signalmeow and libsignal

The submodule must sit at exactly the tag that `libsignalgo` was generated against
(`pkg/libsignalgo/signalversion/version.go` in mautrix-signal). `just check-libsignal` and the
`internal/signal` tests fail when the two differ.

1. Bump mautrix-signal to a release tag, not a pseudo-version of `main`:
   `go get go.mau.fi/mautrix-signal@vX.YYMM.Z && go mod tidy`
2. Read the libsignal version it expects: `go run . version` prints it as `libsignal:`.
3. Move the submodule to that tag:
   ```sh
   git -C third_party/libsignal fetch --depth 1 origin tag vA.B.C
   git -C third_party/libsignal checkout vA.B.C
   git add third_party/libsignal
   ```
   Stage the submodule before running any `just` recipe. `check-libsignal` runs
   `git submodule update`, which resets an unstaged checkout to the recorded commit.
4. `just check-libsignal && just libsignal && just test`

## CI

`.github/workflows/tests.yaml` runs these jobs:

- `test-unit`: `CGO_ENABLED=0` build and tests. No Rust and no libsignal, so it is fast. It runs
  without `-race`, because the race detector needs cgo.
- `test-cgo`: restores `third_party/lib` from the Actions cache, keyed on the libsignal submodule
  commit and the runner OS/arch. On a miss it installs `protoc`, `clang`, `cmake` and the pinned
  Rust toolchain, then builds the library. Either way it then runs `just check-libsignal`,
  `just build` and `just test`.
- `test-lint`, `test-format`.

Bumping the submodule changes the cache key, so the first CI run after an upgrade does a full
Rust build.
