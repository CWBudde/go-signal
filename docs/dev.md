# Development

## Prerequisites

- Go (version from `go.mod`)
- [just](https://github.com/casey/just)

That is enough for the default, pure-Go backend: `just build`, or `go build -tags libsignal_go .`
in a fresh clone without submodules. The cgo backend, the tests that compare the two and
`just check` also need:

- Rust via [rustup](https://rustup.rs). The libsignal submodule pins the toolchain in its
  `rust-toolchain` file, so rustup installs the right version on first build.
- A C toolchain for CGO (gcc or clang), plus `cmake` and `protoc` for libsignal's build

## libsignal (cgo backend)

go-signal talks to Signal through `signalmeow`. Without the `libsignal_go` tag, its `libsignalgo`
bindings link against `libsignal_ffi.a` (CGO). That static library is built from the
`third_party/libsignal` submodule:

```sh
git submodule update --init --depth 1 third_party/libsignal
just libsignal   # cargo build of libsignal-ffi, copied to third_party/lib/
just build-cgo
```

The justfile exports `CGO_ENABLED=1` and `CGO_LDFLAGS=-L third_party/lib`. When you run `go`
directly instead of through `just`, set `CGO_LDFLAGS` yourself. `just libsignal` records the
submodule SHA it built next to the library and skips the cargo build while it still matches.

`CGO_ENABLED=0 go test ./...` works without the library for the pure-Go packages.

## Pure-Go backend (libsignal_go)

The `libsignal_go` build tag builds go-signal without cgo, Rust or `libsignal_ffi.a` (PLAN.md Phases
7–10). Every libsignalgo API is implemented in pure Go, and the integration suite passes with it
against Signal's servers. It is the default: `just build` and the release binaries use it, and
`go-signal version` prints the backend (`backend: libsignal_go` or `cgo`). The cgo backend stays
available (`just build-cgo`) and tested (`just test`, `just test-diff`).

The [Phase 10.2 timing and secret-lifetime review](constant-time-review.md) records the
timing findings and the zeroization posture. The backend tag used to be `purego`, which also
disables the stdlib's AES assembly and selects a variable-time table implementation (CT-02).
`libsignal_go` leaves Go's hardware AES in place; building with `-tags purego` now fails on
purpose (`purego_tag.go`). On CPUs without AES instructions Go still uses the table
implementation in both backends, so go-signal logs a warning at startup and `doctor` reports a
`cpu` warning. `just check-aes-asm` asserts that the release targets select the AES assembly.
Shim `Destroy` methods do not guarantee erasure.

```sh
just build           # CGO_ENABLED=0 go build -tags libsignal_go -> bin/go-signal
just check-purego    # vet, golangci-lint and tests of the purego build
just test-fork       # the pinned forks' tests: libsignal-go with its vectors, the purego shim,
                     # and signalmeow's zkgroup paths (scripts/test-zkgroup-integration.sh)
just test-diff       # cgo: purego against libsignal on the same inputs, and the shim's cgo side
                     # plus on-disk account state across backend switches and zkgroup integration
just build-release <os> <arch>   # cross-compiled release archive in dist/
```

How it fits together:

- go.mod requires the fork
  [`cwbudde/mautrix-signal`](https://github.com/cwbudde/mautrix-signal) (branch `purego`, tags
  `vX.YYMM.Z-purego.N`) under its own module path `github.com/cwbudde/mautrix-signal`, not as a
  `replace` of `go.mau.fi/mautrix-signal`: a `replace` would make `go install` fail. Apart from
  the module path, the fork adapts `pkg/libsignalgo`: every cgo file builds with
  `!libsignal_go`, and `x_purego.go` twins implement the same API on top of
  [`cwbudde/libsignal-go`](https://github.com/cwbudde/libsignal-go). The fork's `PUREGO.md` and
  `internal/stubgen` (stub generator and API parity check) describe the details. The cgo build uses upstream libsignal. The fork also corrects
  the CGO endorsement wrapper to use the combined result supplied by Rust. It also
  exposes narrowly scoped invite preview and single-attempt joining in signalmeow,
  with opt-in request/response log redaction for credential-bearing operations.
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
go mod edit -replace github.com/cwbudde/mautrix-signal=../mautrix-signal
# in ../mautrix-signal/go.mod, for libsignal-go changes:
#   go mod edit -replace github.com/cwbudde/libsignal-go=../libsignal-go
```

When the change is done, commit and tag the fork, drop the replace
(`go mod edit -dropreplace github.com/cwbudde/mautrix-signal`) and `go get` the new tag.

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
injects tests into signalmeow only for that invocation without modifying the pinned
module. It checks encrypted group attributes and member profile keys,
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

The procedure for a mautrix-signal bump (rebase the fork, port the drift, re-pin libsignal-go,
move the submodule) is in [maintenance.md](maintenance.md).

## Coverage

Run `just test-coverage` followed by `just coverage-report` to measure combined statement
coverage across the repository. The profile includes application code exercised by command
and MCP tests; it excludes the `signaltest` fake. Read the merged total in
`coverage-results.md`, since each test binary's printed percentage covers only its own run.
Aim for at least 80% overall, with behavior and failure-path tests for new features. The
production integration suite is opt-in and is not needed for this measurement.

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
| `Group`      | the test group is fetched, a group message is sent to all members, and the peer's delivery receipt arrives        |

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

| Variable                   | Meaning                                                             |
| -------------------------- | ------------------------------------------------------------------- |
| `GOSIGNAL_IT_DATA_DIR`     | data dir of the test account (the suite skips without it)           |
| `GOSIGNAL_IT_ACCOUNT`      | the account in it, by number or ACI; empty selects the first        |
| `GOSIGNAL_IT_PEER`         | the peer's number (required)                                        |
| `GOSIGNAL_IT_GROUP`        | the test group's ID or master key (`go-signal groups list`)         |
| `GOSIGNAL_IT_CREATE_GROUP` | `1` to create a reusable test group (one-time setup only)           |
| `GOSIGNAL_IT_RENAME_GROUP` | `1` to rename the dedicated test group and restore its title        |
| `GOSIGNAL_IT_EDIT`         | `1` to send and edit fresh self, direct and optional group messages |
| `GOSIGNAL_IT_LINK`         | `1` to run `TestIntegrationLink` too                                |
| `GOSIGNAL_IT_TIMEOUT`      | how long to wait for the queue and delivery receipts (default 2m)   |
| `GOSIGNAL_IT_LOG`          | log level of the client (default `warn`)                            |

`just test-integration` runs the suite with the cgo backend and then with `libsignal_go` on the
same account, which also checks that each backend picks up the other's sessions. The peer gets
one set of messages from each run. With `GOSIGNAL_IT_LINK=1` there are two QR codes to scan.
Have the test account's phone ready: the provisioning connection can expire after about
60 seconds without a scan, even though the test's overall timeout is longer.

To create the group from the linked test account, set `GOSIGNAL_IT_DATA_DIR` and
`GOSIGNAL_IT_PEER` first, then run:

```sh
GOSIGNAL_IT_CREATE_GROUP=1 CGO_ENABLED=0 go test -count=1 -v -timeout 5m \
  -tags integration,libsignal_go -run '^TestIntegrationCreateGroup$' ./internal/signal/
```

This uses the public `Client.CreateGroup` operation to create **go-signal integration test**
with the linked account as administrator and the peer as a full member, with invite links
disabled. Both accounts need available profile keys and credentials for the full-membership
assertion; send a message from the peer first to share its key. Creation sends a group
update to the peer. The test prints `GOSIGNAL_IT_GROUP`; export that value and leave
`GOSIGNAL_IT_CREATE_GROUP` unset for subsequent runs. The group is retained for reuse on both
backends. The Group step requires the peer to be a full member and online for its receipt.

If creation fails after printing a group ID, inspect that group with `groups show` before
retrying: the server may have created it even if the member notification failed. The setup
test refuses to create another group while `GOSIGNAL_IT_GROUP` is set.

The CLI exposes the same operation as `groups create <title> --member <recipient>` (repeat
`--member` as needed). The facade permits pending invitations when peer profile credentials
are unavailable, but this fixture requires full membership for subsequent group-send tests.
To check the invitation path manually, use a dedicated peer that has not shared its profile
key, inspect the `pending` output, accept on its phone, then refresh with `groups show <id>`.

To verify member addition manually, create a disposable self-only group on the test account
with `groups create "Member addition test"`, then use its printed ID with
`groups add-members <id> <peer>`. Check the returned revision and full or pending membership
with `groups show <id>`. Repeat the addition and confirm the revision is unchanged. For an
invitation, use a dedicated peer that has not shared its profile key, accept on its phone,
then refresh the group. Check administrator approval with a disposable group's join request;
ordinary members must not be able to approve it, even when they can invite new users.
Repeat on both backends using separate disposable groups. Remove the peer with
`groups remove-members <id> <peer>`, then leave the self-only group with `groups leave <id> --yes`.
These mutations and phone checks remain opt-in; ordinary tests never contact Signal.

To verify renaming, set `GOSIGNAL_IT_DATA_DIR`, `GOSIGNAL_IT_PEER` and `GOSIGNAL_IT_GROUP`, then run:

```sh
GOSIGNAL_IT_RENAME_GROUP=1 CGO_ENABLED=0 go test -count=1 -v -timeout 5m \
  -tags integration,libsignal_go -run '^TestIntegrationRenameGroup$' ./internal/signal/
```

The test requires the two-member fixture above, checks the server's title and revision, checks
that an unchanged title sends no update, and restores and verifies the original title in cleanup.
Members receive two group updates. For cgo, use `CGO_ENABLED=1`, `-tags integration` and
`CGO_LDFLAGS="-L $PWD/third_party/lib"`. This check is opt-in separately from ordinary group sends.

`TestIntegrationEdit` is separately opt-in with `GOSIGNAL_IT_EDIT=1` and the same test account,
peer and optional group settings. Run it with `-run '^TestIntegrationEdit$'` and either backend's
integration tags. It sends an original text and one edit per chat, checks self sync sending,
and waits for the peer's delivery receipts for the direct/group originals and edits. Receipts
verify transport; inspect the peer's phone separately to confirm the corrected text renders.

### Group-settings live check

This manual check is separately opt-in and requires a disposable linked account, an online
peer phone and disposable groups. Ordinary tests use offline fixtures. Run it once with
`just build` (pure Go) and once with `just build-cgo`, using separate disposable groups and
explicit `--data-dir` and `--account` values for every command.

1. Create a group containing the peer and fetch it with `groups show <id> -o json`. Record its
   description, timer, announcement mode, permissions and revision. Confirm full membership.
2. As administrator, run `groups update <id> --description "Settings test" --timer 86400
--announcements-only --edit-permission admins --add-member-permission admins` as one command.
   Fetch it again and confirm all five settings changed with one revision increment. Inspect
   the peer's phone for the settings and group-change notification.
3. Repeat the same update and confirm the revision does not increase. Then explicitly clear
   the description, disable the timer and announcement mode using `--description= --timer 0
--announcements-only=false`. Confirm omitted permissions remain unchanged.
4. In a second group where the test account is an ordinary member, prepare permissions on the
   administrator's phone. Verify description/timer updates succeed when members may edit
   information and fail when only administrators may edit it, including with announcement mode
   enabled. Administrator-only flags must fail even if their supplied values already match.
   A mixed description/permission update must fail without changing any field or revision.
5. Restore the recorded settings through the administrator account and confirm fresh server
   state and phone state. Remove the peer and leave the disposable groups when finished.

If an error reports an accepted or uncertain patch, inspect `groups show <id>` before retrying.
An accepted patch whose notification fails may only log that failure; phone observation is
required separately from the server-state checks. Leave live acceptance open until both
backends and the peer-phone observations have been checked.

### Group-avatar live check

This manual check is separately opt-in. Use a disposable linked account, an online peer phone
and disposable groups, with explicit `--data-dir` and `--account` on every command. Run once
with `just build` (pure Go) and once with `just build-cgo`, using separate groups. Ordinary
tests use offline fixtures; completing those tests does not establish phone rendering.

1. Create a group with the peer and fetch `groups show <id> -o json`. Save its description,
   permissions, revision and avatar image separately. An opaque `avatarPath` cannot restore
   an avatar: keep the original local image or use a disposable group that starts without one.
2. Prepare a small PNG and JPEG (each at most 2 MiB and 2048 pixels in each dimension).
   Run `groups update <id> --avatar test.png --description "Avatar test"`. Fetch again and
   confirm a nonempty `avatarPath`, the new description and one revision increment. Inspect
   the peer's phone for the image and group-change notification. Replace with the JPEG and
   confirm the same behavior. Images are uploaded unchanged; no resize or crop is performed.
3. Set the same file again and confirm a new upload and revision. Update only the description
   and confirm the avatar path is preserved. Run `--remove-avatar`, verify the path is absent
   and the phone avatar clears; repeat removal and confirm no revision increment.
4. In another group where the test account is an ordinary member, use the administrator's
   phone to allow and forbid members from editing group information. Verify set and removal
   succeed only when allowed. A mixed avatar/administrator-only permission update must fail
   without changing any field; changing permissions in that request cannot authorize itself.
   Invalid/truncated images, oversized files and mutually exclusive flags must fail locally.
5. Restore the recorded description and permissions, and re-upload the original image or
   remove the test avatar. Confirm fresh server and peer-phone state, then clean up the groups.

If an error reports an accepted or uncertain patch, inspect `groups show <id>` before retrying.
Upload errors submit no group patch. A successful upload followed by a conflict or failed patch
can leave unused encrypted ciphertext on the CDN. Notification failures after acceptance may
only be logged, so verify phone observations separately. Leave live acceptance open until both
backends and these observations have been checked.

### Group-role live check

This manual check is separately opt-in. Use a disposable linked account, an online peer
phone and a disposable group, with explicit `--data-dir` and `--account` for every command.
Run once with `just build` (pure Go) and once with `just build-cgo` using separate groups.

1. Create a group with the peer as a full member and the test account as administrator.
   Record `groups show <id> -o json`, including roles and revision.
2. Run `groups promote <id> <peer>` and confirm a fresh server fetch shows both accounts as
   administrators with one revision increment. Check the peer's phone for its role and
   group-change notification. Repeat with the peer's number and ACI together; confirm no
   further revision increment.
3. Run `groups demote <id> <peer>` and check the server and phone show the peer as an ordinary
   member. A batch containing a full member and an invited, requesting or absent user must
   fail without changing any role or revision. Repeat with the test account as an ordinary
   member (roles set on the peer's phone); even an unchanged request must fail permission checks.
4. With both accounts administrators again, run `groups demote <id> self`. Confirm the
   selected account's group-level role is `member` and the peer remains administrator.
   Restore the test account's role on the peer's phone before continuing.
5. Attempt to demote all administrators together, and separately the only administrator of
   a self-only group. Both must fail without a patch. Restore the original roles and verify
   fresh server and phone state before removing the peer and leaving the disposable groups.

Inspect `groups show <id>` before retrying accepted or uncertain failures. Notification
failures after acceptance are logged; phone observation is required independently of server
state. Leave live acceptance open until both backends and phone observations are verified.

### Group-ban live check

This manual check is separately opt-in. Use disposable linked accounts, an online peer phone
and disposable groups, with explicit `--data-dir` and `--account` for every command. Run once
with `just build` (pure Go) and once with `just build-cgo` using separate groups. Enable a
group link with `groups link update <id> --state enabled` or on the administrator's phone;
joining can use `groups join` or the phone. Record the original membership, bans and revision with
`groups show <id> -o json`.

1. As a full administrator, run `groups ban <id> <peer-number> <peer-ACI>`. Verify one revision
   increment, the peer removed from `members` and one ACI entry in `banned` with a ban time.
   Check the peer's phone for removal. Try the group link on that phone; joining or requesting
   must be denied. Repeat the ban and confirm the revision and ban time stay unchanged.
2. Run `groups unban <id> <peer>`. Confirm the ban disappears but the peer remains absent.
   Repeat and check there is no revision increment. Verify the phone can join or request again
   using the link, then restore membership and approve the request as needed.
3. Prepare disposable ACI invitations and join requests on the phones. Ban those targets and
   verify invitations are revoked and requests rejected in the same revision as the ban.
   Check peer-phone state. Separately ban an absent user and confirm no membership is added.
4. With an ordinary member as the selected account, attempt both commands, including requests
   whose desired bans already match. They must fail without changing bans, membership or
   revision. A batch containing a valid target and self (number, ACI or `self`) must fail
   atomically. Check duplicate targets and preservation of unrelated bans/settings.
5. If PNI bans are available in the fixture, verify they are displayed and preserved through
   ACI mutations. These commands cannot target PNI bans or revoke PNI-only invitations.
6. Restore original bans and membership from the administrator account and verify fresh server
   and phone state. Unbanning does not restore membership; re-add or re-invite users explicitly.
   Remove the test peers and leave the disposable groups when finished.

Inspect `groups show <id>` before retrying accepted or uncertain failures. Notification
failures after acceptance are logged; phone observations and link-joining attempts must be
checked independently of server state. Leave live acceptance open until both backends and
those observations have been verified. Ordinary tests use offline fixtures only.

### Group-link live check

This manual check is separately opt-in. Use disposable linked accounts, an online peer phone
and disposable groups, with explicit `--data-dir` and `--account` for every command. Run once
with `just build` (pure Go) and once with `just build-cgo`, using separate groups. Resetting
invalidates the previous invite URL permanently; use disposable links for these checks.

1. Create a group with the test account as administrator and the peer as a full member.
   Record `groups link show <id> -o json` and `groups show <id> -o json`. Confirm the initial
   link is disabled and the link document omits `url`. Ordinary group output must contain
   neither invite URL nor password/master key.
2. Enable the link with `groups link update <id> --state enabled`. Confirm a fresh link fetch
   reports an active URL and exactly one revision increment. Check the administrator and
   peer phones for the link setting and group-change notification. Repeat the command and
   confirm no further increment. A full ordinary member may show the active link but may
   not update it, even with the currently selected state.
3. Disable the link, then re-enable it. Confirm the disabled result omits `url`, the phone
   rejects joining through it while disabled, and the re-enabled URL matches the earlier one.
   Set `enabled-with-approval`; use another disposable phone account to request joining.
   Confirm approval is required and that `groups add-members <id> <requester>` approves it.
4. Run `groups link update <id> --reset`. Confirm one revision increment, a different URL,
   unchanged access mode, and rejection of the old link on the phone. Also check a combined
   `--state enabled --reset` change uses one revision. Disable, then reset without state;
   confirm the result remains disabled and has no URL. Re-enable and check the newly active
   URL differs from the previously recorded one.
5. Check invited, requesting and removed accounts cannot show the link. Confirm updates by
   an ordinary member fail without changing state, password or revision. Verify invalid
   states and an empty update fail locally. Inspect peer-phone state independently of the
   server response.
6. Restore the original access mode, remove disposable peers and leave the test groups.
   A reset cannot restore an old URL; do not record this as password restoration.

Inspect `groups link show <id>` before retrying accepted or uncertain errors. Notification
failures after acceptance are logged. Leave live acceptance open until both backends and the
phone observations, old-link rejection, joining and approval have been verified. Ordinary
tests use offline fixtures only.

### Group join live check

This is separately opt-in production acceptance work; it has not been run as part
of implementation. Use two disposable accounts with linked CLI devices and their
phones, plus disposable groups. Run each scenario with both `just build-cgo` and
`just build` (pure-Go), selecting the joining account explicitly. Record backend,
server revision and phone observations without recording invite links or credentials.
Restore membership, bans and link settings afterwards; never use a production group.

1. On the administrator's phone, create an open-link group. Obtain its link with
   `groups link show`. On the other account run `groups join '<link>' -o json`. Verify
   `member`, acceptance/change/verification, ordinary member role, fresh state on
   both devices, and peer notification. Repeat: no change and no revision increment.
2. Create a group requiring approval. Join from the second account and verify
   `requesting`, a single pending request on the administrator's phone, and no
   claim of full membership. Repeat: an already-requested no-op. `groups show`
   may be inaccessible for the requester. Approve from the administrator, verify
   phone membership, then join again and verify an already-member no-op.
3. With a nonmember account, test a disabled link, an old link after reset, and an
   account banned by the administrator. Verify refusals
   contain no link secrets and create no membership/request. Unban and check that
   joining becomes possible. A full member with a retained key remains a no-op
   even when its link is disabled.
4. Prepare a known ACI invitation on the phone. Verify CLI joining reports the
   invitation-acceptance requirement without submitting a new request; accept
   through the phone. PNI invitations, CLI acceptance and cancellation are deferred.
5. Exercise a concurrent change between preview and submission where practical.
   Verify a conflict is not retried. For an accepted change followed by a fetch,
   storage or notification failure, inspect the phone/group before manually
   retrying; distinguish accepted outcomes from transport uncertainty. If another
   device removes the joiner before fresh verification, ensure output does not
   claim current membership. Do not simulate faults by changing production keys.
6. Reopen the same disposable account with the other backend. Check retained keys
   allow inspection only when membership permits it, no account gains another
   account's retained group, and ordinary group output contains no invite secrets.
   Restore the original group settings, membership and bans, and record results.

Keep live joining acceptance unchecked until both backend runs and peer-phone
observations are documented. Offline cryptographic/transport tests remain the
repeatable evidence for malformed, oversized, tampered and ambiguous responses.

### Own-profile live check

Profile mutation checks are manual and separately opt-in. Use a dedicated disposable account
linked to go-signal, with its phone online. Ordinary tests use encrypted fixtures and never
change a live profile. Do not use a personal or production account for this procedure.

1. Build the pure-Go backend with `just build`; after completing its checks and restoration,
   rebuild with `just build-cgo` for the second run. Both write `bin/go-signal`. Use explicit
   `--data-dir` and `--account` flags for the disposable account; keep other clients from
   editing the profile during the check.
2. Save `profile show -o json` before changing anything. Also record the avatar, payment
   address, phone-number-sharing preference and badge visibility/order from the phone.
   These values are preserved internally but are not all exposed by `profile show`.
3. Set a multiword given name and family name, then set about text and an emoji. Check a
   fresh `profile show` and the phone's profile screen. Repeat the same update and confirm
   the result says `Profile unchanged` (`changed:false`, `accepted:false` in JSON).
4. Clear about with `--about=""`, then test a family-only name with `--given-name=""`.
   Confirm omitted fields stay unchanged, and check avatar, payment, privacy and badges on
   the phone. Wait for the other devices to refresh their profiles.
5. Restore all four original text fields using explicit flags, including empty values.
   Verify restoration with a fresh server read and on the phone. Complete restoration
   before testing the second backend; repeat the same checks and restoration with it.

If an update reports acceptance or an unknown outcome, inspect `profile show` and the phone
before deciding whether another write is needed. A profiles-v2 account is refused; do not
try to work around that refusal. V1 has no conditional write to prevent concurrent edits.

After an accepted update, go-signal verifies a fresh raw server profile, refreshes signalmeow's
cache, persists verified local display data and sends other devices a `LOCAL_PROFILE` notice.
The pinned backend does not process incoming `LOCAL_PROFILE` notices and this feature does
not write remote storage records. Later storage syncs may therefore show older display text
elsewhere. A failed cache refresh can also leave signalmeow's cache stale; `profile show`
bypasses it. Canceling a blocked refresh reconnects the anonymous websocket; canceling a
blocked self-notification reconnects the authenticated websocket. These reconnects can
briefly interrupt other requests on the same client. The facade limits its own
HTTP responses to 1 MiB; the dependency's forced-cache reader has no equivalent bound.

### Sticker live check

Sticker send/receive acceptance remains separately opt-in. Use a dedicated linked test
account/data dir, its online phone, an online peer and a disposable test group. Stop other
receivers for that data dir. Obtain an existing Signal pack share link and a valid numeric
sticker ID; `0` is a valid ID. No live sticker checks run in ordinary tests or CI.

1. Build with `just build`. Use explicit `--data-dir` and `--account` for every command.
   Send the sticker to `self`, the peer and the disposable group using
   `send <recipient> --sticker-pack '<link>' --sticker-id <id>` (or `--group <id>`).
   Check per-recipient results and inspect the phone/peer for the actual sticker, its emoji
   and device sync. Delivery results alone do not prove correct image rendering.
2. Send the same sticker from the peer's phone to the test account. Run
   `receive -o json --download-attachments <private-dir>`. Verify `sticker.image.path`,
   image contents and animation when applicable, while confirming ordinary attachment
   numbering and key-free JSON. Send another sticker and receive without the flag;
   metadata should appear without a local file, path or download error.
3. Repeat with a static WebP and an available animated PNG/APNG or GIF pack. File bytes
   are preserved; go-signal performs no conversion. Missing images are reported per item,
   without stopping receive, and have no permanent-pack fallback.
4. Rebuild with `just build-cgo` and repeat on the same dedicated account. Record backend,
   pack ID/sticker ID, actual phone rendering, sync and downloaded file evidence. Do not
   record pack keys in the roadmap. Leave live acceptance open if either backend or phone
   checks were not performed.

Outgoing pack fetching accepts only Signal share links, then requests fixed Signal CDN paths;
it never contacts the link's supplied host. Manifest and image encrypted responses are limited
to 1 MiB and 100 MiB respectively, verified before upload. Pack installation, caching, MCP
sticker tools and uploading new packs are deferred.

The real websocket cache fixture synchronizes its cleanup to avoid a known race in the pinned
dependency: `connectLoop` clears a captured request channel while the handler can still read
it. This fixture ordering leaves production dependency code unchanged; passing race tests
do not establish that the dependency's ordinary websocket shutdown is free of that race.

### Poll live check

Poll acceptance is separately opt-in and never part of ordinary tests or CI. Use a dedicated
linked account/data dir, its online phone, an online peer and a disposable test group. Use
explicit `--data-dir` and `--account` on every command; stop other receivers for that data dir.

1. Build with `just build` (pure Go). Create a multiple-choice poll and a single-choice poll
   using `polls create --group <id> --question <text> --option <text> --option <text>` with
   `--single-choice` for the second. Record creator ACI/timestamp from JSON and delivery per
   member. Verify the phone's question, option order and selection policy.
2. Vote on a peer-created group poll using `polls vote --group <id> --target <peer-aci>:<ts>
--vote-count 1 --option 0`. Change selections with a higher counter, then use `--clear`
   with another higher counter. Verify selection and withdrawal on the peer's phone and
   sync to the linked account's phone. Coordinate counters with other-device activity.
3. Close a CLI-created poll using `polls close --group <id> --target <ts>`. Verify the phone
   treats it as closed. Send a new creation, vote, withdrawal and closure from the peer's
   phone while `receive -o json` runs; verify typed fields and target identity. Inspect
   own-device transcripts for all three operations too.
4. Collect another peer poll through daemon/MCP receiving, including votes and closure.
   Stop the server, run `polls show` with the canonical creator ACI, and compare retained
   tally/closure with observed phone activity. Repeat with a small scan limit: missing
   creation must omit tally and completeness must remain unknown. The old ordinary receive
   events are not expected in this inbox. Delivery receipts alone do not prove rendering.
5. Rebuild with `just build-cgo` and repeat with fresh polls in the disposable group. Record
   backend, poll identity, phone results, sync and received-state evidence. Only then tick
   the separately open roadmap live-acceptance item.

Poll sends are group-only. Direct-chat sending, inferred counters and durable poll projections
are deferred. Tests cover exact payload construction and retained observations on both
backends offline; they do not establish phone acceptance.

## CI

`.github/workflows/tests.yaml` runs these jobs:

- `test-unit`: `CGO_ENABLED=0` build and tests without the purego tag. No Rust and no libsignal,
  so it is fast. It runs without `-race`, because the race detector needs cgo.
- `test-purego`: `CGO_ENABLED=0 -tags libsignal_go` build, then `just check-purego` and
  `just test-fork`.
- `build` (`build.yaml`): cross-compiles and packages the release binaries for linux, darwin and
  windows on amd64 and arm64 on one runner (`just build-release`), smoke-runs the linux/amd64 one
  directly and in the scratch container image (`just smoke-image`), and keeps the archives as the
  `release` artifact. `release.yaml` calls the same workflow for a tag.
- `test-cgo`: restores `third_party/lib` from the Actions cache, keyed on the libsignal submodule
  commit and the runner OS/arch. On a miss it installs `protoc`, `clang`, `cmake` and the pinned
  Rust toolchain, then builds the library. Either way it then runs `just check-libsignal`,
  `just build-cgo` and `just test`. Its second job, `differential`, restores the same cache after it
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
3. `release.yaml` builds the binaries with `build.yaml` and attaches them:
   `go-signal_<version>_<os>_<arch>.tar.gz` for linux and darwin, `.zip` for windows, each on
   amd64 and arm64, `install.sh` (the README's `curl … | sh`, from `scripts/install.sh`), plus
   `SHA256SUMS` and a build provenance attestation. It also pushes the
   container image and updates the Homebrew tap and the AUR package. All of them are pure-Go
   (`libsignal_go`) builds; releases need no Rust.

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

### Release build

The release binaries are pure Go, cross-compiled with `CGO_ENABLED=0 -tags libsignal_go -trimpath`,
so the linux ones are fully static and run on any distribution and in a `scratch` container:

```sh
just build-release linux amd64   # dist/linux_amd64/go-signal and dist/go-signal_<version>_linux_amd64.tar.gz
just smoke-image                 # ldd check, then `version` and `account show` in the scratch image
```

`just docs-gen` writes the man pages and completions (`scripts/gendocs`) that `scripts/package.sh`
puts in the archives. The static musl build of the cgo backend (Phase 6.2, `just build-static`) was
retired when the default flipped (PLAN.md 10.3); the git history has it.

## Pin live check

This procedure is separately opt-in and contacts Signal production. Use disposable linked
accounts, an online peer phone and a disposable group. Ordinary offline suites do not run it.
Perform it with both the pure-Go and cgo binaries, recording app versions and each outcome.

1. Send fresh direct, group and Note to Self messages; record author ACIs and sent timestamps.
2. Pin each with `pins add` using `--duration 86400`; verify phone pin rendering and other-device
   sync. Repeat with `--forever` on a different target. Verify `pins remove` removes each pin.
3. Use a short positive duration to observe expiry. Receipt time can differ on each client;
   delivery alone is not acceptance. Confirm target deletion and disappearing-message expiry
   remove phone pins even for forever mode.
4. In the disposable group, verify admin and permitted full-member pin/unpin, restricted
   ordinary-member failure and absent/pending-member failure. When sending to a direct chat
   and a forbidden group together, verify the command sends to neither.
5. While daemon/MCP receiving runs, pin and unpin from the peer/another own device. Stop the
   receiver, then inspect `pins list --chat ... -o json`. Compare retained targets, operations,
   receipt-based expiry, deletion flags and scan metadata; completeness must remain unknown.
   Also inspect ordinary `receive -o json` typed pin/unpin controls.
6. Remove all pins made by the procedure and restore any permission/timer changes. Record
   backend, commands, transport results, phone results and cleanup. Leave PLAN.md live checks
   open until both backend runs and phone observations are complete.
