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

## Pure-Go backend (libsignal_go)

The `libsignal_go` build tag builds go-signal without cgo, Rust or `libsignal_ffi.a` (PLAN.md Phases
7–10). Every libsignalgo API is implemented in pure Go; what is still open is live acceptance
against Signal's servers and the hardening of Phase 10. The cgo build stays the default.

The [Phase 10.2 timing and secret-lifetime review](constant-time-review.md) records the
timing findings and the zeroization posture. The backend tag used to be `purego`, which also
disables the stdlib's AES assembly and selects a variable-time table implementation (CT-02).
`libsignal_go` leaves Go's hardware AES in place; building with `-tags purego` now fails on
purpose (`purego_tag.go`). On CPUs without AES instructions Go still uses the table
implementation in both backends, so go-signal logs a warning at startup and `doctor` reports a
`cpu` warning. `just check-aes-asm` asserts that the release targets select the AES assembly.
Shim `Destroy` methods do not guarantee erasure.

```sh
just build-purego    # CGO_ENABLED=0 go build -tags libsignal_go -> bin/go-signal-purego
just check-purego    # vet, golangci-lint and tests of the purego build
just test-fork       # the pinned forks' tests: libsignal-go with its vectors, the purego shim,
                     # and signalmeow's zkgroup paths (scripts/test-zkgroup-integration.sh)
just test-diff       # cgo: purego against libsignal on the same inputs, and the shim's cgo side
                     # plus on-disk account state across backend switches and zkgroup integration
just build-purego-release <os> <arch>   # cross-compiled archive in dist/purego/
```

How it fits together:

- go.mod replaces `go.mau.fi/mautrix-signal` with the fork
  [`cwbudde/mautrix-signal`](https://github.com/cwbudde/mautrix-signal) (branch `purego`, tags
  `vX.YYMM.Z-purego.N`). The fork changes only `pkg/libsignalgo`: every cgo file builds with
  `!libsignal_go`, and `x_purego.go` twins implement the same API on top of
  [`cwbudde/libsignal-go`](https://github.com/cwbudde/libsignal-go). The fork's `PUREGO.md` and
  `internal/stubgen` (stub generator and API parity check) describe the details. The cgo build uses upstream libsignal. The fork also corrects
  the CGO endorsement wrapper to use the combined result supplied by Rust.
- `cwbudde/libsignal-go` is a fork of `GoCodeAlone/libsignal-go` whose Rust compat harness is
  pinned to the libsignal tag libsignalgo expects (its `decisions/0007-cwbudde-fork-policy.md`).
  Fork releases are tagged `vX.Y.Z-cw.N`.
- In go-signal, files that need libsignal through cgo are `cgo && !libsignal_go` (`libsignal.go`,
  `hpke.go`, `username_cgo.go`, `internal/store/sqlite_cgo.go`), and their purego counterparts
  are `libsignal_go`. The real client and the store build with `cgo || libsignal_go`; `meow_nocgo.go` is
  `!cgo && !libsignal_go`. Purego builds use `modernc.org/sqlite` instead of `mattn/go-sqlite3`, with
  the same connection options (`TestConnectionPragmas` checks both).
- Tests that need real libsignal are `cgo && !libsignal_go`. `purego_diff_test.go` (cgo) runs the
  cgo code and the pure-Go code on the same inputs and requires identical results.

`just test-diff` also runs `scripts/test-backend-switch.sh`. It builds separate CGO and purego
store test binaries and alternates them over temporary account databases, starting with each
backend. The test continues prekey/session and sender-key exchanges, checks persisted ACI/PNI
identity keys, consumes one-time prekeys, recovers skipped group keys and rejects replays.
Skipped session keys are exercised too. Tampered messages must leave persisted session,
identity, prekey and sender-key records unchanged, and the original message must still decrypt.
It uses generated accounts and needs no Signal credentials or server access. The ordinary
store suite runs the same scenario as `TestProtocolStateSurvivesReopen` within one backend.

`scripts/test-cdsi-integration.sh` completes the purego CDSI shim's hybrid handshake offline
against the recorded enclave key and tests transport boundaries, tampering, replay rejection,
nonce recovery and state transitions. It copies both pinned forks to a temporary workspace and
adds a helper under `attest/` solely to enable the old fixture's evaluation-number-12 exception
inside one test scope. Production rejection is checked before and after that scope; the module
cache, working checkouts and published APIs remain unchanged. No fork release is needed.
`just test-fork` runs it without cgo; `just test-diff` runs it with the race detector (still using
the purego shim). CGO handshake completion and live CDSI lookup remain acceptance work.

To work on the forks locally, point go.mod at the checkouts temporarily and don't commit it:

```sh
go mod edit -replace go.mau.fi/mautrix-signal=../mautrix-signal
# in ../mautrix-signal/go.mod, for libsignal-go changes:
#   go mod edit -replace github.com/cwbudde/libsignal-go=../libsignal-go
```

When the change is done, commit and tag the fork, then set the replace to the new tag.

The zkgroup integration test (Phases 8.3–8.4) runs signalmeow's group and profile code
against both builds of the shim. It overlays test files into signalmeow's sources, which Go
refuses for the module cache, so without a workspace it tests a temporary copy of the pinned
fork (that is how `just test-fork`, `just test-diff` and CI run it). To test a mautrix-signal
checkout instead, run it in a workspace, from the go-signal directory:

```sh
work_dir=$(mktemp -d)
(cd "$work_dir" && go work init "$OLDPWD" "$OLDPWD/../mautrix-signal")
export GOWORK="$work_dir/go.work"
CGO_ENABLED=0 scripts/test-zkgroup-integration.sh -tags libsignal_go
CGO_LDFLAGS="-L $PWD/third_party/lib" scripts/test-zkgroup-integration.sh
```

The integration script requires Python 3 to write Go's temporary overlay JSON. It
injects tests into signalmeow only for that invocation, leaving the fork restricted
to `pkg/libsignalgo`. It checks encrypted group attributes and member profile keys,
returning a group despite invalid endorsements, and profile URL/access-key
construction and decryption over a localhost WebSocket. It also verifies endorsement
cache insertion and expiry, sends an encrypted group text through the actual
multi-recipient sender, authenticates the exact recipient set in `Group-Send-Token`,
and decrypts the message at both recipients. Unexpected per-recipient fallback
requests fail the test; an empty local-device sync is allowed. Test stores provide
pre-existing session metadata and an already-distributed sender key. The shim
fixture supplies test server parameters; no account or Signal server is accessed.
The WebSocket test currently detects an upstream shutdown race under `-race` in
`web/signalwebsocket.go` (`incomingRequestChan` is cleared while the handler goroutine
reads it); the ordinary CGO/purego integration runs and go-signal's race suite pass.

The offline tests don't substitute for live group, profile and send checks; those are in the
integration suite (see "Integration tests").

### Upgrading signalmeow and libsignal

The submodule must sit at exactly the tag that `libsignalgo` was generated against
(`pkg/libsignalgo/signalversion/version.go` in mautrix-signal). `just check-libsignal` and the
`internal/signal` tests fail when the two differ.

1. Bump mautrix-signal to a release tag, not a pseudo-version of `main`. go.mod requires
   `go.mau.fi/mautrix-signal@vX.YYMM.Z` and replaces it with the purego fork, so the fork moves
   first: rebase its `purego` branch onto the new upstream tag, run
   `go run ./pkg/libsignalgo/internal/stubgen -gen` and `-check`, port whatever changed, and tag
   `vX.YYMM.Z-purego.1`. Then:
   `go mod edit -require go.mau.fi/mautrix-signal@vX.YYMM.Z -replace go.mau.fi/mautrix-signal=github.com/cwbudde/mautrix-signal@vX.YYMM.Z-purego.1 && go mod tidy`
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
5. If the libsignal tag moved, re-pin `cwbudde/libsignal-go`'s compat harness to it
   (`scripts/update-upstream-pin.sh vA.B.C` plus the manual steps in its ADR 0007), port the
   drift, tag a new `-cw.N` release and bump it in the mautrix fork.

## Integration tests

`internal/signal/integration_test.go` (`-tags integration`) runs the real client against Signal's
**production** servers with a dedicated test account. It is opt-in and never runs in CI; `just
check-purego` only vets and lints it. signalmeow is hard-wired to production (hosts, zkgroup server
parameters, CDSI enclave), so there is no staging variant.

`TestIntegration` connects the test account once and runs these steps in order:

| Step         | What it checks                                                                                                    |
| ------------ | ----------------------------------------------------------------------------------------------------------------- |
| `Receive`    | the queued messages are received and decrypted, up to the queue-empty marker                                      |
| `CDSI`       | contact discovery for the peer's number (the enclave handshake, bypassing the cache) agrees with `Resolve`        |
| `Profile`    | our own profile, and the peer's if we have their profile key, is fetched and decrypted, with a zkgroup credential |
| `NoteToSelf` | a sync transcript to our other devices                                                                            |
| `Direct`     | a 1:1 message to the peer, which must come back with the peer phone's delivery receipt                            |
| `Group`      | the test group is fetched and a group message (group send endorsements) is sent to all members                    |

A decryption failure for a message sent during the run fails it too. `TestIntegrationLink` links
a new device into a temporary data dir (scan the QR code it prints), connects, lists the devices,
sends a note to self and unlinks it again.

Setup:

- A test account linked with go-signal into its own data dir (`go-signal --data-dir DIR link`).
  The suite acks the queued messages and uses the account's sessions, so don't point it at an
  account you use, and stop anything else connected with that data dir (`mcp serve`, `receive`).
- A second Signal account (the peer) whose phone is online, so that its delivery receipts arrive.
  It must be discoverable by number. For `Profile/Peer`, message the test account from it once.
- Optionally a group with the test account and the peer in it.

| Variable               | Meaning                                                           |
| ---------------------- | ----------------------------------------------------------------- |
| `GOSIGNAL_IT_DATA_DIR` | data dir of the test account (the suite skips without it)         |
| `GOSIGNAL_IT_ACCOUNT`  | the account in it, by number or ACI; empty selects the first      |
| `GOSIGNAL_IT_PEER`     | the peer's number (required)                                      |
| `GOSIGNAL_IT_GROUP`    | the test group's ID or master key (`go-signal groups list`)       |
| `GOSIGNAL_IT_LINK`     | `1` to run `TestIntegrationLink` too                              |
| `GOSIGNAL_IT_TIMEOUT`  | how long to wait for the queue and delivery receipts (default 2m) |
| `GOSIGNAL_IT_LOG`      | log level of the client (default `warn`)                          |

`just test-integration` runs the suite with the cgo backend and then with `libsignal_go` on the
same account, which also checks that each backend picks up the other's sessions. The peer gets
one set of messages from each run. With `GOSIGNAL_IT_LINK=1` there are two QR codes to scan.

## CI

`.github/workflows/tests.yaml` runs these jobs:

- `test-unit`: `CGO_ENABLED=0` build and tests without the purego tag. No Rust and no libsignal,
  so it is fast. It runs without `-race`, because the race detector needs cgo.
- `test-purego`: `CGO_ENABLED=0 -tags libsignal_go` build, then `just check-purego` and
  `just test-fork`.
- `build-purego`: cross-compiles and packages the purego binaries for linux, darwin and windows
  on amd64 and arm64 on one runner (`just build-purego-release`), smoke-runs the linux/amd64 one
  and keeps the archives as the `purego` artifact.
- `test-cgo`: restores `third_party/lib` from the Actions cache, keyed on the libsignal submodule
  commit and the runner OS/arch. On a miss it installs `protoc`, `clang`, `cmake` and the pinned
  Rust toolchain, then builds the library. Either way it then runs `just check-libsignal`,
  `just build` and `just test`. Its second job, `differential`, restores the same cache after it
  and runs `just test-diff`.
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
   Its `purego` job builds the pure-Go archives (`go-signal-purego_*`) as a workflow artifact only;
   they are not attached to the release until PLAN.md 10.3 flips the default.

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
