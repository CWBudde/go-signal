# Maintenance

## Bumping mautrix-signal (signalmeow) and libsignal

go-signal depends on three pinned pieces that have to move together:

- signalmeow, from the fork [`cwbudde/mautrix-signal`](https://github.com/cwbudde/mautrix-signal)
  of upstream `go.mau.fi/mautrix-signal` (branch `purego`, tags `vX.YYMM.Z-purego.N`). The fork
  has its own module path, `github.com/cwbudde/mautrix-signal`, so that go-signal needs no
  `replace` and `go install github.com/cwbudde/go-signal@latest` works. Its main adaptation
  is `pkg/libsignalgo`: every cgo file gets a `libsignal_go` twin on top of libsignal-go
  (its `PUREGO.md`). Since `v0.2609.0-purego.11`, it also deliberately extends
  signalmeow with bounded invite previews, one-shot joining and signed-response
  binding, plus opt-in websocket request/response logging redaction for credentials.
  `v0.2609.0-purego.14` adds authenticated password-free join-request previews and
  exact signed own-ACI request cancellation in one PATCH, without generic mutation
  retries, full-state reads, profile credentials, member notification or device sync.
  `v0.2609.0-purego.15` preserves owned contact-sync timer metadata with field presence,
  stamps group timer seconds from the same retrieved state as the GroupV2 context, clears
  direct timer versions on group messages and propagates sent-transcript/contact-sync
  handler failures to acknowledgement. Incomplete contact attachment framing is rejected.
  `v0.2609.0-purego.16` additionally handles an absent optional contact-avatar MIME type
  with content detection, preserving avatar bytes and paired timer metadata.
- [`cwbudde/libsignal-go`](https://github.com/cwbudde/libsignal-go) (tags `vX.Y.Z-cw.N`), the
  pure-Go libsignal that the default backend runs on. Its Rust compat harness is pinned to the
  libsignal tag libsignalgo was generated against (its `decisions/0007-cwbudde-fork-policy.md`).
- The `third_party/libsignal` submodule, which the cgo backend links. It must sit at exactly the
  tag libsignalgo was generated against (`pkg/libsignalgo/signalversion/version.go`);
  `just check-libsignal` and the `internal/signal` tests fail when the two differ.

Always move to an upstream release tag, never a pseudo-version of `main`. Commit to the forks'
release branches and push; don't open PRs.

The current disappearing-message release is the immutable
[`v0.2609.0-purego.16`](https://github.com/cwbudde/mautrix-signal/tree/v0.2609.0-purego.16)
tag at [commit `622eab1`](https://github.com/cwbudde/mautrix-signal/commit/622eab11d06f82755445d371faf33d876c686e34).
The downloaded module's Origin and production source match that commit. Both pure-Go CI
runs passed ([37183497231](https://github.com/cwbudde/mautrix-signal/actions/runs/37183497231),
[37183497248](https://github.com/cwbudde/mautrix-signal/actions/runs/37183497248)).
The broad Go CI runs
([37183497235](https://github.com/cwbudde/mautrix-signal/actions/runs/37183497235),
[37183497226](https://github.com/cwbudde/mautrix-signal/actions/runs/37183497226))
failed pre-commit formatting on six libsignalgo files, all byte-identical to the `.14`
baseline `9cd9cbd`. Local whole-fork lint likewise retains 40 default-backend and 42
pure-Go inherited findings; the broad Matrix bridge pure-Go build still fails on
`sqlite3.Error`/`sqlite3.ErrCorrupt`. Its cgo build also needs the external `olm/olm.h`
header, unavailable in the local validation environment. These are dependency
limitations, not passing whole-fork checks.

Affected fork tests passed on both backends. Contact transaction rollback is exercised
with the real cgo database; the pure-Go counterpart uses a controlled transaction fixture
because the upstream dbutil SQLite dialect handling prevents that real-store fixture there.
Phone acceptance remains unrun; offline tests do not verify rendering or expiry.

### 1. Rebase the mautrix fork

In a `cwbudde/mautrix-signal` checkout:

The commit that renames the module path (`build!: module path github.com/cwbudde/mautrix-signal`)
is not rebased but redone: drop it, rebase the rest, then rename again on top, so that the rename
covers the new upstream files too. Fork commits made after the rename already use the new path;
where they touch upstream files, their conflicts are only in import lines.

```sh
git fetch upstream --tags
git switch purego
# the new upstream tag; the sequence editor drops the rename commit
GIT_SEQUENCE_EDITOR="sed -i '/build!: module path github.com.cwbudde.mautrix-signal/d'" \
  git rebase -i vX.YYMM.Z
git grep -l go.mau.fi/mautrix-signal | xargs sed -i 's#go\.mau\.fi/mautrix-signal#github.com/cwbudde/mautrix-signal#g'
gofmt -w $(git diff --name-only | grep '\.go$')
git commit -am 'build!: module path github.com/cwbudde/mautrix-signal'
go run ./pkg/libsignalgo/internal/stubgen -gen         # stubs for new cgo files
go run ./pkg/libsignalgo/internal/stubgen -check       # exported API parity of the two builds
```

Resolve shim conflicts in `pkg/libsignalgo`. Take upstream's side elsewhere except
for the deliberate membership extension (`groups_join*.go`, `groups_accept*.go`, `groups_cancel_request*.go`,
shared `groups_membership_http.go`, scoped websocket logging policy/tests and
pure-Go CI coverage): preserve or port those changes until upstream offers
equivalent behavior. Retain strict fresh invitation reads, own ACI/PNI promotion
and signed/fresh-state verification. Preserve cancellation's key/group/revision/own-ACI
binding, sole-action and epoch checks, bounded password-free GET/PATCH transport,
accepted-but-unverified outcomes and fresh-preview-only no-op evidence. Server
403/404 must remain errors. Run the offline tests on both backends after
rebasing and confirm no mutation retries or credential logging are introduced. Keep
the disappearing-message extensions as well: ACI-bound contact timer metadata must own its
pointer values, retain zero/presence and emit only after successful contact storage; malformed
or truncated contact framing, downloads, decoding and transactions must fail without partial
lists or acknowledgements. Preserve sent-message/edit handler failure propagation. Group
ordinary and edit content must use one retrieved revision for context and seconds, including
zero, and clear the direct timer version. Exercise all these boundaries on both backends;
retain the real cgo rollback test and record the controlled pure-Go fixture limitation. Keep
the fork's fixes that upstream doesn't have yet (the cgo clock fix in `message.go`,
`prekeybundle.go` and `sessionrecord.go`, the combined endorsement result; see `PUREGO.md`).

Read `pkg/libsignalgo/signalversion/version.go`: that is the libsignal tag `vA.B.C` everything
else follows. Note whether it moved.

### 2. Port the drift

`-check` lists new or changed exported API. For each difference:

- a new cgo file got a generated stub (`x_purego.go`) returning `ErrNotImplemented`: implement it
  by hand on libsignal-go (the generator leaves hand-written files alone once its marker is gone);
- a changed signature in a hand-written `x_purego.go`: port the change;
- new libsignal behaviour behind an unchanged API (error codes, serialized forms): compare with
  the Rust bridge in the new libsignal tag.

If the port needs something libsignal-go doesn't have, add it there first (step 3).

Then run the shim's tests in both builds, including `TestCrossBackend` (regenerate its
fixtures with `LIBSIGNALGO_WRITE_FIXTURE=1` only when the serialized forms changed on purpose),
`TestZKGroupAPI`, `TestGroupSendEndorsementShim` and `TestUnacknowledgedSessionClock`:

```sh
CGO_ENABLED=0 go test -tags libsignal_go ./pkg/libsignalgo/...
go test ./pkg/libsignalgo/...          # cgo; needs libsignal_ffi.a for vA.B.C
```

### 3. Re-pin libsignal-go (only if the libsignal tag moved)

In a `cwbudde/libsignal-go` checkout:

```sh
scripts/update-upstream-pin.sh vA.B.C
```

It bumps the tag mentions, updates the lockfile, regenerates the vectors and runs the vector,
report and interop tests. By hand (ADR 0007): set `rust-toolchain.toml` to the libsignal tag's
`rust-toolchain`, and the direct `spqr` and `libcrux-ml-kem` pins in
`compat/rust-harness/Cargo.toml` to what the tag's workspace `Cargo.toml` pulls in. Port what
the regenerated vectors or the interop tests show changed, run `scripts/fuzz.sh` briefly, tag
the next `-cw.N` and push. Then pin it in the mautrix fork
(`go get github.com/cwbudde/libsignal-go@vX.Y.Z-cw.N && go mod tidy`) and rerun step 2's tests.

### 4. Tag the mautrix fork

Tag `vX.YYMM.Z-purego.1` on `purego` and push the branch and the tag. The fork's CI
(`.github/workflows/purego.yml`) runs the stub check and the purego build and tests.

### 5. Bump go-signal

```sh
go get github.com/cwbudde/mautrix-signal@vX.YYMM.Z-purego.1
go mod tidy
```

go.mod must not get a `replace` (it breaks `go install`).

Move the submodule to the libsignal tag (`go run -tags libsignal_go . version` prints it as
`libsignal:`):

```sh
git -C third_party/libsignal fetch --depth 1 origin tag vA.B.C
git -C third_party/libsignal checkout vA.B.C
git add third_party/libsignal
```

Stage the submodule before running any `just` recipe: `check-libsignal` runs
`git submodule update`, which resets an unstaged checkout to the recorded commit.

Fix whatever signalmeow's API changes broke in `internal/signal` (the facade keeps them there).

### 6. Test

```sh
just check-libsignal && just libsignal   # cgo library for the new tag (Rust build)
just check                               # fmt, lint, cgo tests, tidy
just check-purego                        # the default backend: vet, lint, tests, AES assembly
just test-fork                           # the pinned forks' own tests
just test-diff                           # cgo vs pure Go, backend switch, zkgroup integration
just test-integration                    # live, on the test account (docs/dev.md, "Integration tests")
```

The live suite is the last word on a bump: it is the only test against Signal's servers. Run it
before tagging a release that contains the bump.

Bumping the submodule changes the CI cache key, so the first `test-cgo` run afterwards does a
full Rust build. The release workflow doesn't build Rust at all.
