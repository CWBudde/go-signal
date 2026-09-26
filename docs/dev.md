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

`release-please.yaml` and `release.yaml` make the releases (see below).

Bumping the submodule changes the cache key, so the first CI run after an upgrade does a full
Rust build.

## Releases

Releases are cut by [release-please](https://github.com/googleapis/release-please) from the
conventional commit messages on `main` (`feat:`, `fix:`, `feat!:` …):

1. `release-please.yaml` keeps a release PR open that bumps `.release-please-manifest.json` and
   `CHANGELOG.md`. Before 1.0, `feat` bumps the minor version and breaking changes do too.
2. Merging the PR tags `vX.Y.Z`, creates the GitHub release and calls `release.yaml` for it.
3. `release.yaml` builds and attaches the binaries: `go-signal_<version>_<os>_<arch>.tar.gz` for
   linux amd64/arm64 (static) and darwin arm64, plus `SHA256SUMS` and a build provenance
   attestation. It also pushes the container image and updates the Homebrew tap and the AUR package.

Pushing a `v*` tag by hand runs `release.yaml` as well and creates the release if it is missing;
"Run workflow" on `release.yaml` rebuilds an existing tag. The binaries are replaced
(`--clobber`), so a rerun is safe.

One-time repository setup:

- Settings → Actions → General: allow GitHub Actions to create and approve pull requests
  (release-please opens its PR with `GITHUB_TOKEN`).
- Optional secret `RELEASE_PLEASE_TOKEN`: a fine-grained PAT (contents and pull requests: write).
  PRs opened with `GITHUB_TOKEN` don't start other workflows, so without it the tests don't run
  on the release PR.
- Optional secret `HOMEBREW_TAP_TOKEN`: a PAT with contents: write on `cwbudde/homebrew-tap`.
  The `homebrew` job renders `packaging/homebrew/go-signal.rb.tmpl` into `Formula/go-signal.rb`.
- Optional secret `AUR_SSH_KEY`: the private SSH key of the AUR account that maintains
  `go-signal-bin`, plus the variables `AUR_USERNAME` and `AUR_EMAIL` for the commit. The `aur` job
  renders `packaging/aur/PKGBUILD.tmpl`.

The `homebrew` and `aur` jobs are skipped while their secret is unset.
`scripts/render-packaging.sh` fills in the version and checksums of both templates.

### Static build

The Linux release binary is fully static: it links musl, libstdc++, zlib and `libsignal_ffi.a`
statically, so it runs on any distribution and in a `scratch` container. `just build-static` builds
it in a `golang:<go version>-alpine` container (Docker) for the arch of the machine it runs on:

```sh
just build-static   # dist/linux_<arch>/go-signal
just smoke-static   # ldd check, then `version` and `account show` in the scratch image
just package linux amd64   # dist/go-signal_<version>_linux_amd64.tar.gz with man pages etc.
```

`scripts/build-static.sh` does the work inside the container. It builds a musl
`libsignal_ffi.a` into `third_party/lib-musl/<arch>/`, which is skipped while the SHA stamp
matches the submodule. It keeps the Rust and Go caches in `third_party/.musl/`
(`just clean-static` removes both). The first build compiles libsignal and takes a while.
Proc-macros need `RUSTFLAGS=-C target-feature=-crt-static` on a musl host, as in
mautrix-signal's own `build-rust.sh`.

`just build-release` is the dynamic release build for the host (used for macOS), and `just docs-gen`
writes the man pages and completions (`scripts/gendocs`) that `just package` puts in the archives.
