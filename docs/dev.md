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
  exposes narrowly scoped invite preview, single-attempt joining and pending-request
  cancellation in signalmeow, with opt-in request/response log redaction for
  credential-bearing operations. It preserves contact-sync timer field presence and
  acknowledgement failures, and stamps group timer seconds from the same retrieved state
  used for the group context (see [maintenance](maintenance.md)).
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
CGO_LDFLAGS="-L $PWD/third_party/lib" scripts/test-zkgroup-integration.sh -race
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
The fork fixes the captured incoming-request-channel shutdown race in
`web/signalwebsocket.go`. `just test-diff` runs this integration with `-race`; to
exercise the pure-Go backend with the race detector, enable cgo for instrumentation:

```sh
CGO_ENABLED=1 scripts/test-zkgroup-integration.sh -tags libsignal_go -race
```

These checks cover the observed race and the exercised paths, without establishing
that every websocket shutdown and reconnect path is race-free.

The [websocket lifecycle investigation](websocket-lifecycle.md) records additional
findings and the repaired shutdown contract. `scripts/test-websocket-lifecycle.sh`
runs the fork's offline regressions plus a deterministic late-registration test in
a temporary source copy. It requires Python 3 for test-only barrier injection and
passes on the current pin. `just test-fork` runs it on pure Go; `just test-diff` runs
both backends with `-race`. See the report for commands and fixture limitations.

The pinned fork's `TestKeyCheckLifecyclePNI422` exercises actual receive startup and
PNI prekey rejection with local websocket peers. It verifies logout and worker lifetime
through cleanup failures. `just test-fork` runs it without cgo, and `just test-diff`
runs both backends with `-race`; see the [lifecycle report](websocket-lifecycle.md).

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
a new device into a temporary data dir (scan the QR code it prints), connects, lists the devices
(checking the new device's name and creation time), runs the initial sync and checks that the
peer's contact and the test group (`GOSIGNAL_IT_GROUP`) arrive, sends a note to self and unlinks
it again. When `GOSIGNAL_IT_DATA_DIR` holds the same account, it also sends a note to self with an
attachment from that device and checks that the freshly linked device downloads it byte-identical.

Setup:

- A test account linked with go-signal into its own data dir (`go-signal --data-dir DIR link`).
  The suite acks the queued messages and uses the account's sessions, so don't point it at an
  account you use, and stop anything else connected with that data dir (`mcp serve`, `receive`).
- A second Signal account (the peer) whose phone is online, so that its delivery receipts arrive.
  It must be discoverable by number. For `Profile/Peer`, message the test account from it once.
- Optionally a group with the test account and the peer in it.

[Test account setup](#test-account-setup) describes these fixtures step by step.

| Variable                    | Meaning                                                                              |
| --------------------------- | ------------------------------------------------------------------------------------ |
| `GOSIGNAL_IT_DATA_DIR`      | data dir of the test account (the suite skips without it)                            |
| `GOSIGNAL_IT_ACCOUNT`       | the account in it, by number or ACI; empty selects the first                         |
| `GOSIGNAL_IT_PEER`          | the peer's number (required)                                                         |
| `GOSIGNAL_IT_GROUP`         | the test group's ID or master key (`go-signal groups list`)                          |
| `GOSIGNAL_IT_CREATE_GROUP`  | `1` to create a reusable test group (one-time setup only)                            |
| `GOSIGNAL_IT_RENAME_GROUP`  | `1` to rename the dedicated test group and restore its title                         |
| `GOSIGNAL_IT_EDIT`          | `1` to send and edit fresh self, direct and optional group messages                  |
| `GOSIGNAL_IT_LINK`          | `1` to run `TestIntegrationLink` too                                                 |
| `GOSIGNAL_IT_MESSAGING`     | `1` to run `TestIntegrationMessaging`                                                |
| `GOSIGNAL_IT_REMOTE_UNLINK` | `1` to run `TestIntegrationRemoteUnlink` (remove a device on the phone)              |
| `GOSIGNAL_IT_KEEP_UNLINKED` | `1` to keep the remotely unlinked data dir for `TestIntegrationReceiveUnlinked`      |
| `GOSIGNAL_IT_LEAVE_GROUP`   | a disposable group to leave in `TestIntegrationLeaveGroup` (ID, master key or title) |
| `GOSIGNAL_IT_RECEIVE`       | `1` to run `TestIntegrationReceiveInterrupt` (`./cmd/`)                              |
| `GOSIGNAL_IT_UNLINKED_DIR`  | an unlinked data dir for `TestIntegrationReceiveUnlinked` (`./cmd/`)                 |
| `GOSIGNAL_IT_PEER_DATA_DIR` | data dir of a linked peer account that sends the receive test's message (optional)   |
| `GOSIGNAL_IT_PEER_ACCOUNT`  | the account in `GOSIGNAL_IT_PEER_DATA_DIR`; empty selects the only one               |
| `GOSIGNAL_IT_TIMEOUT`       | how long to wait for the queue and delivery receipts (default 2m)                    |
| `GOSIGNAL_IT_LOG`           | log level of the client (default `warn`)                                             |

`just test-integration` runs the suite with the cgo backend and then with `libsignal_go` on the
same account, which also checks that each backend picks up the other's sessions. For each backend
it runs `^TestIntegration` in `./internal/signal/` and then in `./cmd/`, one package at a time,
so that only one connection uses the account; the environment variables pass through. The peer gets
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

The following tests cover PLAN.md §11.1 and are each opt-in on top of `GOSIGNAL_IT_DATA_DIR`
(and `GOSIGNAL_IT_PEER` where they message the peer). The commands show the pure-Go backend
first and the cgo backend second; run both. `-run` accepts several tests as `'^(TestA|TestB)$'`.

`TestIntegrationMessaging` (`GOSIGNAL_IT_MESSAGING=1`) sends to the peer and, with
`GOSIGNAL_IT_GROUP`, to the test group: a PNG attachment, a reply that quotes it and mentions the
peer, a reaction to it and a remote delete of a fresh message. Every send must succeed; the
attachment, the reply and the deleted message must also come back with the peer's delivery
receipt. For the reaction and the delete the test only logs whether a receipt arrived within 15 s.
It logs every sent timestamp so that you can find the messages on the phone
([Core messaging live check](#core-messaging-live-check)). It also blocks the peer (this needs the
storage service key: run `account sync` first if it is unknown), checks that `Contacts` reports
the block, unblocks again (also in cleanup if the test fails) and checks that the peer is a full
member of the test group.

```sh
GOSIGNAL_IT_MESSAGING=1 CGO_ENABLED=0 go test -count=1 -v -timeout 20m \
  -tags integration,libsignal_go -run '^TestIntegrationMessaging$' ./internal/signal/
GOSIGNAL_IT_MESSAGING=1 CGO_LDFLAGS="-L $PWD/third_party/lib" go test -count=1 -v -timeout 20m \
  -tags integration -run '^TestIntegrationMessaging$' ./internal/signal/
```

`TestIntegrationLink` (`GOSIGNAL_IT_LINK=1`, see above) prints a QR code to scan with the test
account's phone; keep it at hand:

```sh
GOSIGNAL_IT_LINK=1 CGO_ENABLED=0 go test -count=1 -v -timeout 20m \
  -tags integration,libsignal_go -run '^TestIntegrationLink$' ./internal/signal/
GOSIGNAL_IT_LINK=1 CGO_LDFLAGS="-L $PWD/third_party/lib" go test -count=1 -v -timeout 20m \
  -tags integration -run '^TestIntegrationLink$' ./internal/signal/
```

`TestIntegrationRemoteUnlink` (`GOSIGNAL_IT_REMOTE_UNLINK=1`) links a temporary device (scan the
QR code), then asks you to remove that device on the phone (Settings > Linked devices). It expects
the client to notice the logged-out state and `Devices` and `Connect` to fail with
`ErrDeviceUnlinked`. With `GOSIGNAL_IT_KEEP_UNLINKED=1` the temporary data dir is kept and logged
as `GOSIGNAL_IT_UNLINKED_DIR=<dir>` for `TestIntegrationReceiveUnlinked` below; delete it afterwards.

```sh
GOSIGNAL_IT_REMOTE_UNLINK=1 GOSIGNAL_IT_KEEP_UNLINKED=1 CGO_ENABLED=0 go test -count=1 -v \
  -timeout 20m -tags integration,libsignal_go -run '^TestIntegrationRemoteUnlink$' ./internal/signal/
GOSIGNAL_IT_REMOTE_UNLINK=1 GOSIGNAL_IT_KEEP_UNLINKED=1 CGO_LDFLAGS="-L $PWD/third_party/lib" \
  go test -count=1 -v -timeout 20m -tags integration -run '^TestIntegrationRemoteUnlink$' ./internal/signal/
```

`TestIntegrationLeaveGroup` (`GOSIGNAL_IT_LEAVE_GROUP=<group>`) leaves a disposable group that was
created on the peer's phone and checks from fresh server state that the test account is no longer
a member. Like the other `connectLive` tests it needs `GOSIGNAL_IT_PEER`. Each run consumes the group: create a new one on the phone for the other backend. Never
point it at the `GOSIGNAL_IT_GROUP` fixture.

```sh
GOSIGNAL_IT_LEAVE_GROUP='<id-or-title>' CGO_ENABLED=0 go test -count=1 -v -timeout 20m \
  -tags integration,libsignal_go -run '^TestIntegrationLeaveGroup$' ./internal/signal/
GOSIGNAL_IT_LEAVE_GROUP='<id-or-title>' CGO_LDFLAGS="-L $PWD/third_party/lib" go test -count=1 -v \
  -timeout 20m -tags integration -run '^TestIntegrationLeaveGroup$' ./internal/signal/
```

The `./cmd/` integration tests (package `cmd_test`, same tags) build the go-signal binary and run
it as a process. `TestIntegrationReceiveInterrupt` (`GOSIGNAL_IT_RECEIVE=1`) starts
`receive --follow` on the test account, waits for a message to arrive, sends SIGINT and expects
the process to exit normally (code 0; 130 is only for a second, forcing signal) within 1.5 s; a
following one-shot `receive` must not deliver that message again (its ack was flushed). The
message comes from a second linked account in `GOSIGNAL_IT_PEER_DATA_DIR` (sent with the same
binary); without it, the test prints a token to stderr and waits for you to send a message
containing it to the test account from another phone (or to Note to Self from its phone). `TestIntegrationReceiveUnlinked` (`GOSIGNAL_IT_UNLINKED_DIR=<dir>`,
from `TestIntegrationRemoteUnlink` above) runs `receive` on the unlinked data dir and expects exit
code 3 and the `account unlink --yes --local-only` cleanup hint.

```sh
GOSIGNAL_IT_RECEIVE=1 GOSIGNAL_IT_UNLINKED_DIR=<dir> CGO_ENABLED=0 go test -count=1 -v \
  -timeout 20m -tags integration,libsignal_go -run '^TestIntegration' ./cmd/
GOSIGNAL_IT_RECEIVE=1 GOSIGNAL_IT_UNLINKED_DIR=<dir> CGO_LDFLAGS="-L $PWD/third_party/lib" \
  go test -count=1 -v -timeout 20m -tags integration -run '^TestIntegration' ./cmd/
```

Remote unlinking and `receive` on the unlinked dir are two runs: keep the dir from the first, then
pass it to the second. Use the dir from the same backend's run.

### Test account setup

The integration tests and live checks act on a real Signal account and acknowledge, send, block
and leave on its behalf. **Never use your personal account** or a personal peer: use a dedicated,
disposable number and a peer that has agreed to receive test messages.

1. Register a disposable number (prepaid SIM or a second number) with Signal on a spare phone.
   This is the test account's primary device. Set a profile name.
2. Build go-signal (`just build` for pure Go; `just build-cgo` writes the same `bin/go-signal`
   with the cgo backend) and link it into its own data dir. Scan the QR code with the spare
   phone (Settings > Linked devices):

   ```sh
   bin/go-signal --data-dir ~/signal-it link --name go-signal-it
   bin/go-signal --data-dir ~/signal-it account show
   ```

3. From the peer's phone, send the test account a message and accept the message request on the
   spare phone, so that both sides have each other's profile keys. Then run
   `bin/go-signal --data-dir ~/signal-it receive` once.
4. Export the fixture variables (the account is the test number or ACI from `account show`):

   ```sh
   export GOSIGNAL_IT_DATA_DIR=~/signal-it GOSIGNAL_IT_ACCOUNT=+49... GOSIGNAL_IT_PEER=+49...
   ```

5. Create the two-member test group with `TestIntegrationCreateGroup` (above) and export the
   `GOSIGNAL_IT_GROUP` it prints. Confirm with `bin/go-signal --data-dir ~/signal-it groups list`.
6. For `TestIntegrationLeaveGroup` and the group checks below, create disposable groups on the
   peer's phone that include the test account, one per backend run. Receive their group updates
   (`receive`) or run `account sync` before use, and pass the ID from `groups list`.

Stop every other process that uses `~/signal-it` (`mcp serve`, `daemon`, `receive --follow`) before
running tests, and remove leftover linked devices on the spare phone after failed link tests.

### Core messaging live check

This procedure for PLAN.md §11.1 remains **unrun**. It covers what needs eyes on a phone. Use
the [test account setup](#test-account-setup) and run it once with `just build` (pure Go) and
once with `just build-cgo`, recording `bin/go-signal version` for each run. The commands below
abbreviate the fixture as a shell function:

```sh
gs() { bin/go-signal --data-dir "$GOSIGNAL_IT_DATA_DIR" -a "$GOSIGNAL_IT_ACCOUNT" "$@"; }
```

1. **Attachments, quotes and mentions.** Run `TestIntegrationMessaging` (above) and copy the
   logged timestamps. On the peer's phone, find each message by its time: the PNG renders as an
   image (open it, not just the thumbnail), the reply shows the quoted image and highlights the
   mention as the peer's name, the reaction sits on the right message and the deleted message
   shows "This message was deleted". Check the same in the test group.
2. **Received images.** Send a photo from the peer's phone (as a file/document as well, since
   photo sends are recompressed) and export the sent originals from the phone. Then:

   ```sh
   gs receive --download-attachments /tmp/it-dl
   sha256sum /tmp/it-dl/* <exported-originals>
   ```

   The document copy must match byte for byte; the photo must match the file as the phone
   sent it (compare it with the copy saved from the chat on the phone, not the camera original).

3. **Timestamps from plain output.** Have the peer send three short messages. In plain `gs receive`
   output, take the middle one's `timestamp=` and reply and react to it, using the peer's number
   (or ACI from `gs contacts list`) as the author:

   ```sh
   gs send "$GOSIGNAL_IT_PEER" --quote "$GOSIGNAL_IT_PEER:<timestamp>" -m "reply to the middle one"
   gs react "$GOSIGNAL_IT_PEER" --target "$GOSIGNAL_IT_PEER:<timestamp>" --emoji 👍
   ```

   The phone must attach both to the middle message, not its neighbours. Repeat with a message
   the peer sent in the test group (`-g "$GOSIGNAL_IT_GROUP"`, same `--quote`/`--target`).

4. **Edits.** Run `TestIntegrationEdit` and confirm the corrected texts on the phone. Then edit a
   media and a quote message by hand; `--edit` takes the original's sent timestamp from `send`'s
   output, and the replacement must supply the attachment and quote again:

   ```sh
   gs send "$GOSIGNAL_IT_PEER" --attach pic.png -m "caption v1"
   gs send "$GOSIGNAL_IT_PEER" --edit <ts> --attach pic.png -m "caption v2"
   gs send "$GOSIGNAL_IT_PEER" --quote "$GOSIGNAL_IT_PEER:<peer-ts>" -m "reply v1"
   gs send "$GOSIGNAL_IT_PEER" --edit <ts> --quote "$GOSIGNAL_IT_PEER:<peer-ts>" -m "reply v2"
   ```

   The phone must show "Edited", the new caption with the image still present, and the quote
   still attached. Also try an edit without `--attach` and record how the phone renders it.

5. **Reactions and remote deletes.** In the 1:1 chat and in the test group, send a fresh message,
   react to it with `gs react ... --target self:<ts> --emoji ❤️`, take it back with
   `--remove`, and delete another fresh message with `gs delete "$GOSIGNAL_IT_PEER" --target <ts>`
   (group: `gs delete -g "$GOSIGNAL_IT_GROUP" --target <ts>`). Check each on the peer's phone and
   on the spare phone (sync transcripts).
6. **Initial sync after linking.** `TestIntegrationLink` asserts it; also check by hand. Link a
   fresh data dir, then list what arrived:

   ```sh
   bin/go-signal --data-dir /tmp/it-link link --name sync-check
   bin/go-signal --data-dir /tmp/it-link contacts list
   bin/go-signal --data-dir /tmp/it-link groups list
   bin/go-signal --data-dir /tmp/it-link account show
   ```

   The phone's contacts (with names) and groups must be there and `account show` must report the
   last sync. `account sync` repeats it. Remove the device with `account unlink --yes` (step 12).

7. **Blocking.** First block an unrelated disposable number on the spare phone
   (Settings > Privacy > Blocked) as a canary. Then run `gs contacts block "$GOSIGNAL_IT_PEER"`:
   the spare phone's blocked list must show the peer **and still show the canary** (the official
   apps replace their whole list with the one go-signal sends). Messages from the peer must no
   longer arrive on the spare phone. Run `gs contacts unblock "$GOSIGNAL_IT_PEER"`: the peer is
   removed, the canary stays, and `gs contacts list --blocked` agrees after `gs account sync`.
8. **Groups created on the phone.** On the peer's phone, create a disposable group with the test
   account. Run `gs receive`, then `gs groups list` and `gs groups show <id>`: title, members,
   roles and revision must match the phone. Leave with `gs groups leave <id> --yes` and confirm
   the phone shows the test account as having left; `gs groups list` no longer lists it as a
   member. `TestIntegrationLeaveGroup` automates the leave part.
9. **Identity change.** Use a disposable peer; reinstalling loses its history. Have the peer
   delete and reinstall Signal (or re-register the number) and send the test account a message.
   `gs receive` must report `[safety number changed; sending to them is blocked ...]`,
   `gs identities list` must show the peer as `untrusted`, and `gs send "$GOSIGNAL_IT_PEER" -m hi`
   must fail. Compare `gs identities show "$GOSIGNAL_IT_PEER"` with the safety number on the
   spare phone, then run
   `gs identities trust "$GOSIGNAL_IT_PEER" --safety-number <60 digits>`; the send now succeeds.
   Trust state lives in the shared data dir, so the second backend needs another re-registration.
10. **Network drop.** Start `gs receive --follow`. Run `nmcli networking off`, have the peer
    send two messages, wait about a minute, then `nmcli networking on`. receive must reconnect
    on its own (connection events in `-o json`, log warnings) and print both messages exactly
    once; a following one-shot `gs receive` must not print them again.
11. **Ctrl-C and remote unlink.** These are automated: `TestIntegrationReceiveInterrupt` and
    `TestIntegrationRemoteUnlink` plus `TestIntegrationReceiveUnlinked` (above). By hand, press
    Ctrl-C during `gs receive --follow` and check it exits within about a second.
12. **Devices and server-side unlink.** `gs devices list` must show the spare phone as device 1
    and the linked devices with the names given at `link` and plausible creation times (the
    `/tmp/it-link` device from step 6 included). Then run
    `bin/go-signal --data-dir /tmp/it-link account unlink --yes`: the device must disappear from
    the spare phone's Linked devices and from `gs devices list`, and the data dir is emptied.

Restore and clean up: unblock the peer and the canary on the spare phone, take back test
reactions, leave or delete the disposable groups (keep the `GOSIGNAL_IT_GROUP` fixture), remove
stale linked devices on the spare phone, delete `/tmp/it-dl` and `/tmp/it-link`, and re-trust the
peer if a step left it untrusted. Record results below; keep PLAN.md §11.1 open until every row
passes on both backends.

| Item                                      | Backend | Date | Result |
| ----------------------------------------- | ------- | ---- | ------ |
| 1. Attachments, quotes, mentions          | pure Go |      |        |
| 1. Attachments, quotes, mentions          | cgo     |      |        |
| 2. Received images byte-identical         | pure Go |      |        |
| 2. Received images byte-identical         | cgo     |      |        |
| 3. Replies/reactions via plain timestamps | pure Go |      |        |
| 3. Replies/reactions via plain timestamps | cgo     |      |        |
| 4. Edits incl. media and quote            | pure Go |      |        |
| 4. Edits incl. media and quote            | cgo     |      |        |
| 5. Reactions and remote deletes           | pure Go |      |        |
| 5. Reactions and remote deletes           | cgo     |      |        |
| 6. Initial sync after linking             | pure Go |      |        |
| 6. Initial sync after linking             | cgo     |      |        |
| 7. Block/unblock on the phone             | pure Go |      |        |
| 7. Block/unblock on the phone             | cgo     |      |        |
| 8. Groups list/show/leave                 | pure Go |      |        |
| 8. Groups list/show/leave                 | cgo     |      |        |
| 9. Identity change                        | pure Go |      |        |
| 9. Identity change                        | cgo     |      |        |
| 10. Network drop recovery                 | pure Go |      |        |
| 10. Network drop recovery                 | cgo     |      |        |
| 11. Ctrl-C and remote unlink (automated)  | pure Go |      |        |
| 11. Ctrl-C and remote unlink (automated)  | cgo     |      |        |
| 12. Devices list and `account unlink`     | pure Go |      |        |
| 12. Devices list and `account unlink`     | cgo     |      |        |

### Link QR refresh live check

This check remains **unrun** and requires a disposable phone account. Repeat on both
backends using a fresh data dir and the initial sync disabled:

```sh
go-signal --data-dir DIR link --name qr-refresh-test --sync-timeout 0
```

1. Leave the first QR unscanned. Confirm a refresh notice and a different URI/code appear
   about every 45 seconds, and that waiting through several codes does not end the command.
2. After more than two minutes, scan the newest code. Confirm one successful link and
   exactly one new device on the phone. Remove that disposable device afterwards.
3. Repeat and scan just before a refresh. Confirm it either finishes the submitted link
   or displays a new code to scan; it must not register two devices.
4. Start again without scanning and press Ctrl-C. Confirm prompt exit and no new account
   in the data dir or linked device on the phone.

Offline fork tests exercise the real websocket/protobuf/encrypted-envelope exchange with
fake time, including refreshes, fresh keys/addresses, cancellation, caller deadlines,
transport/protocol failures and acknowledgement timeout after a submitted envelope.
Registration errors are terminal: only the idle scan wait is retried. These tests do not
establish the production server's expiry timing or phone acceptance.

### Disappearing-messages live check

This acceptance procedure remains **unrun**. Use only a disposable linked account, two
consenting peer phones and disposable groups. Run every step separately with `just build`
(pure Go) and `just build-cgo`, recording the binary's `version` output. Use explicit
`--data-dir` and `--account` values on every command and separate test fixtures for each
backend. Offline tests establish persistence and protobuf fields; phone display and expiry
require these observations.

1. On the phones, set direct chat A to 30 seconds and chat B to 5 minutes (or two different
   supported durations). Run `sync` and `receive`; have both peers send a text so their
   settings are observed. Send to both recipients in one invocation:
   `send <peer-A> <peer-B> -m "Timer acceptance"`. Record each printed timestamp and verify
   each phone shows its own duration. Follow each phone's expiry behavior and record when
   it removes the message; do not infer an expiry-start rule from transport success.
2. Disable A's timer on its phone, then run `receive` or `sync` to learn the update. Send
   another message and verify it stays untimed while B still expires. To check stale updates,
   keep a disposable linked device offline before the disable, queue a message with the
   older timer there, then reconnect it after go-signal learns the disable. Receive that
   delayed message, send again, and confirm the newer disabled setting remains effective.
   If the clients will not send the old queued metadata, record this live case as unverified;
   deterministic offline tests cover version ordering.
3. Exit go-signal completely and send again using the same data directory. Verify both chat
   settings survive the restart. Change Note to Self's timer on the linked phone, receive its
   sync transcript, then `send self -m "Self timer acceptance"`. Check the phone's duration
   and expiry, and repeat after disabling that timer and refreshing it.
4. Edit a timed direct message using `send <peer-B> --edit <timestamp> -m "Edited timer"`.
   Repeat with Note to Self. Verify the replacement renders and carries the chat's current
   timer; observe the phone's expiry separately. If changing a timer between the original
   and edit, refresh through `receive` or `sync` before sending the edit.
5. In a disposable group with a peer, change the timer on the phone, receive its group update
   and fetch `groups show <id>`. Send a group message and edit it, checking phone duration
   and expiry against that group state. Repeat after changing the duration and after disabling
   it. A group setting must not overwrite either direct-chat timer.
6. Restore changed settings, remove peers from disposable groups and leave the groups. Record
   observations, backend, durations, refresh steps and any unverified cases. Keep roadmap
   phone acceptance open until both backend runs and all required phone checks are complete.

The receiving clients manage expiry. This procedure does not expect local inbox deletion,
attachment cleanup or a synchronous remote timer refresh before every send.

### Mention and quote live check

Offline mention conversion, rendering, quote encoding and inbox persistence are covered by
unit and command golden tests. This phone acceptance procedure remains unrun. Repeat it with
disposable accounts on both the cgo and `libsignal_go` binaries.

1. In a disposable group, send a phone message mentioning a known contact and yourself, with
   an emoji before the mentions. Receive it in plain output: verify the names and `@me` appear
   at the correct positions. Capture another message with `receive -o json` and verify its
   raw placeholders and UTF-16 `mentions` offsets.
2. Reply on the phone to a message containing mentions. Verify the received quote's plain
   names and JSON `quote.mentions`. Repeat with an edit containing mentions and with messages
   sent from your own phone, which arrive as sync transcripts.
3. Send a reply using `--quote <author>:<timestamp>` and
   `--quote-text '😀 @{@username.discriminator}'`. Confirm the mention target and quoted text
   on the peer's phone, including the fallback quote when the original message is unavailable.
4. Receive into the daemon or MCP inbox, restart it, then list the stored message and edit.
   Confirm their raw text, mention metadata and known names survive. Check an unknown contact
   falls back to an identifier in plain output.

Record the backend, commands, timestamps and peer-phone observations. Leave the Phase 12 phone
acceptance item open until both backends pass; this procedure alone is not evidence of a run.

### Text-style live check

Offline tests cover all five style encodings, overlapping mention/style ranges, UTF-16
offsets with non-ASCII text, edits, direct/group requests, invalid ranges before connecting,
and plain/JSON command output. Phone rendering has not been verified. Repeat this procedure
with disposable accounts and both `just build` (pure Go) and `just build-cgo` binaries.

1. Send `-m '😀 hello' --style 3:5:bold --style 3:2:italic` to the peer and to `self`.
   Confirm that the whole word is bold and its first two letters are also italic, with
   the emoji unchanged. Check the sync transcript on your own phone.
2. Send `-m '😀 @{self} café' --style 3:1:bold --style 5:4:monospace` in a disposable
   group. Confirm the correct mention and styles. Each mention is one unit in the rewritten
   body; offsets are not measured in the original CLI placeholder text or the displayed name.
3. Send `-m 'secret crossed code'` with `--style 0:6:spoiler`,
   `--style 7:7:strikethrough` and `--style 15:4:monospace`. Confirm spoiler conceal/reveal,
   strikethrough and monospace.
   Repeat using `--stdin` and with an attachment.
4. Edit one of your messages with new text and new style ranges. Confirm replacement styles
   on the peer phone. Previous styles are not automatically retained.
5. Try `-m '😀' --style 1:1:bold`, an out-of-bounds range and an unsupported style.
   Confirm that each fails without sending a message.

Record commands, message timestamps, backend and phone observations. Keep phone acceptance
open until both backends pass. Receive/inbox output currently preserves text and mentions,
but does not expose styles or apply terminal formatting.

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
   invitation-acceptance requirement without submitting a new request; use
   `groups accept <known-group>` or the phone. For ACI/PNI acceptance, follow
   [the invitation live check](#group-invitation-live-check). Pending requests use
   [the cancellation live check](#group-join-request-cancellation-live-check).
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

### Group join-request cancellation live check

Cancellation checks are manual and separately opt-in. Use two disposable accounts:
a group administrator and a requester linked to go-signal, both with online phones.
Use a disposable approval-required group, explicit `--data-dir`/`--account` flags
and no concurrent receiver for the requester data dir. Ordinary tests and CI make
no live Signal calls. Keep live cancellation acceptance unchecked until both
backend runs and peer-phone observations are recorded.

1. Run `just build` for pure Go; complete all observations and restoration before
   running `just build-cgo` and repeating the procedure. Both write `bin/go-signal`.
   Record the backend/version and initial group settings and membership.
2. From the requester, use `groups join '<approval-link>'` and confirm the
   administrator sees one pending request. Run
   `groups cancel-request '<known-group-id>' --yes -o json` on that selected account.
   Confirm `changed`, `accepted`, `verified` are true and the administrator's phone
   shows the request removed. Record the reported revision. The signature proves
   exact deletion at that revision; delayed phone refresh is separate evidence.
3. Repeat cancellation. If a fresh authenticated preview remains accessible and
   shows no pending request, expect false/false/true and no revision increment.
   If access is refused with 403/404, expect an error and empty stdout. Do not
   treat refusal as proof of absence or automatically retry. Inspect on the
   administrator's phone; the requester may be unable to use `groups show`.
4. Request again, then disable the invite link or reset its password on the
   administrator's phone before cancelling via the known group reference.
   Check the pending request can still be cancelled without the old invite password.
   Observe administrator visibility and preserve both accounts' membership.
5. Exercise an approval race where practical: approve the requester between preview
   and submission. Confirm conflicts are not retried and full membership is never
   removed by cancellation. With full membership, a fresh pending=false preview
   permits only a no-op. Prepare an ACI invitation and check cancellation does not
   decline it; invitation acceptance and ordinary leave remain separate operations.
6. On an accepted or uncertain error, record the safe ID/revision evidence and
   inspect the administrator/requester phones before another write. Do not inject
   production-key faults. The command sends no member notification or linked-device
   sync; record other-device refresh behavior without claiming delivery guarantees.
   Cached titles only resolve references; preview titles do not refresh full-state
   or title/left caches. Check another selected account cannot use the requester's
   known key unless it independently knows the group.
7. Restore invite-link settings/password policy, requests, invitations, bans and
   membership from the initial record. Reopen the disposable requester with the
   other backend and repeat all checks and restoration; record outcomes and any
   service refusal or phone-refresh limitations.

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
   without stopping receive; authenticated pack fallback is available for missing/expired images.
4. Rebuild with `just build-cgo` and repeat on the same dedicated account. Record backend,
   pack ID/sticker ID, actual phone rendering, sync and downloaded file evidence. Do not
   record pack keys in the roadmap. Leave live acceptance open if either backend or phone
   checks were not performed.

Outgoing pack fetching accepts only Signal share links, then requests fixed Signal CDN paths;
it never contacts the link's supplied host. Manifest and image encrypted responses are limited
to 1 MiB and 100 MiB respectively, verified before upload. Complete local installations are
bounded to 200 items and 100 MiB of decoded images. Uploading new packs and phone
installation-state sync remain open.

The real websocket cache and reconnect fixtures use ordinary shutdown without
log-hook synchronization. The pinned fork keeps the captured incoming request
channel reference stable while closing the channel during cleanup. The fork
regression and zkgroup integration exercise this fix with the race detector.

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
5. Repeat creation, voting, withdrawal and closure in a direct chat with `--recipient <peer>`
   in place of `--group <id>`. Verify both phones and own-device sync. Also create a `self`
   poll and verify it on the linked phone. Collect direct-chat controls through daemon/MCP,
   then inspect with `polls show --recipient <canonical-chat-aci>` and verify isolation from
   group and other direct-chat events.
6. Omit `--vote-count` for successive selections and `--clear`; confirm JSON counters increase
   across CLI restarts and the phone shows each change. Vote from the linked phone, receive
   its own-device transcript, then confirm the next automatic CLI counter exceeds it. Prune
   the daemon/MCP inbox and confirm the counter still increases. On a disposable poll, send
   an explicit higher counter, then confirm automatic allocation continues above it.
7. Rebuild with `just build-cgo` and repeat with fresh group and direct polls. Record
   backend, poll identity, phone results, sync and received-state evidence. Only then tick
   the separately open roadmap live-acceptance item.

Durable poll projections and MCP/daemon poll operations are implemented; phone acceptance remains open.
Also inspect `polls show --durable` after receiving, pruning the general inbox and reopening
the CLI. Confirm creation/tally/closure persist and repeated receive transcripts do not add
duplicate observations. The default `show` view remains limited to retained inbox history.
Automatic-counter phone acceptance also remains open; local allocation cannot establish unseen other-device state.
The direct-chat/self extension has not been verified with a phone session. Tests cover exact
payload construction and retained observations on both backends offline; they do not establish phone acceptance.

For the separately tracked MCP/daemon poll acceptance, repeat the group/direct/self lifecycle
through `poll_create`, `poll_vote`, `poll_close`, `poll_show` and the `/v1/polls` HTTP routes
while each server receives. Verify both phones, automatic and explicit counters, withdrawal,
closure, and retained/durable results after inbox pruning. Exercise MCP confirmation decline
and accept, denied allowlists and read-only mode; declined/denied votes must not consume a
counter. Exercise daemon missing-token/denied/read-only writes, malformed inputs and the
409 exhaustion response on a disposable poll. For partial group delivery, record member
outcomes before any retry. Repeat on both backends; offline API tests do not prove phone rendering.

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

### Group invitation live check

This is separately opt-in production acceptance work and was not run during
implementation. Use two disposable accounts with linked CLI devices and phones,
plus disposable groups. Run every scenario with both `just build-cgo` and
`just build` (pure-Go), selecting the invitee account explicitly. Synchronize or
receive the group key before accepting; acceptance cannot import an unknown key.
Record only backend, safe group IDs/revisions and phone observations, without
recording master keys, invitation URLs or credentials.

1. Prepare an ACI invitation from the administrator's phone. Confirm fresh admin
   group state shows the invitee as pending, rather than already a full member.
   Run `groups accept '<known-id-or-title>' -o json` as that invitee. Verify all
   three booleans are true, fresh own ACI membership, the offered role, revision
   and phone membership/notifications. Repeat: already-member no-op, with
   `changed` and `accepted` false and no revision increment.
2. Prepare a phone-number invitation before the invitee has shared its profile
   key. Confirm the administrator's fresh group state identifies the pending
   recipient by PNI. If the server creates an ACI invitation or adds a full
   member, the fixture does not satisfy the PNI criterion. Accept as the invitee
   and verify the exact PNI invitation is removed, own ACI is a full ordinary
   member, unrelated invitations remain, and both phones show the result.
   Repeat the no-op check. Leave PNI live acceptance open if no PNI fixture can
   be established.
3. Test a known group with no own invitation, a revoked invitation and a foreign
   invitation. Verify refusal creates no membership. Disable the invite link
   while an own invitation exists: invitation acceptance uses full-state auth,
   without requiring the link password. Check selected-account isolation: an
   account without the known key/invitation must not consume another account's
   invitation or obtain its cached group through a title/key alias.
4. Exercise a concurrent group change between read and submission where
   practical. Verify conflict refusal is not retried. After any accepted or
   uncertain error, inspect `groups show <id>`, the phone or the administrator
   before a manual retry. If another device accepted during a failed follow-up,
   the next fresh invocation must be a no-op. If the invitee is removed before
   verification, output must not claim current membership. Use offline fixtures
   for malformed/tampered/oversized bodies, transport faults and cancellation;
   do not change production cryptographic keys to inject failures.
5. Reopen the same disposable account using the other backend. Verify retained
   key visibility, fresh membership and cached title; a successful fetch clears
   a local left marker. Restore disposable group membership/settings and remove
   temporary peers afterwards.

Keep live invitation acceptance unchecked until the ACI and actual PNI scenarios,
both backend runs and peer-phone observations are documented. Ordinary tests use
offline cryptographic and transport fixtures. Request cancellation and invitation
decline have separate live procedures; global PNI self-membership reporting remains
separate work.

## Viewed receipt live check

This production acceptance check requires two disposable accounts with linked CLI devices and
phones. Run it with both `just build-cgo` and `just build` (pure Go). Offline tests verify
command wiring, sender resolution, timestamp batching and the `VIEWED` wire type; phone
rendering remains unchecked until this procedure is run.

1. Accept the peer's message request on the recipient phone. From the peer phone, send media
   that exposes a viewed/played indicator (for example a voice message). Receive it on the CLI
   and record the sender's ACI and original sent timestamp. Merely receiving, printing or
   downloading it must not trigger a viewed indicator on the peer phone.
2. After viewing/playing the media, run
   `go-signal receipts send-viewed <sender-ACI> --timestamp <original-ms>`. Verify the peer's
   indicator changes for that original message. Command success alone is not acceptance
   evidence. Check that unrelated messages stay unchanged.
3. Send two fresh eligible messages from the same peer. Submit both timestamps with repeated
   `--timestamp`, including a duplicate, and verify both indicators. Repeat with a group
   message: address its individual sender, rather than the group ID.
4. Check receipt failures and an unaccepted message request where practical. The backend may
   skip the latter while reporting success. The pinned backend does not send a viewed-state
   sync to the recipient's other devices; do not expect this command to mark their inbox read.
5. Repeat with fresh messages on the other backend, recording only backend, message timestamps
   and phone observations. Remove disposable peers/groups afterwards.

## Read-receipt setting live check

Use two disposable accounts with an accepted message request and linked CLI devices. Run the
procedure on both `just build-cgo` and `just build` (pure Go). Offline regression tests cover
stored enabled/disabled/unknown settings, empty account records across restart, receive batching
and inbox mark-read. They stop before encryption/network access and do not prove phone behavior.

1. Enable read receipts on the recipient phone, run `go-signal account sync`, and restart the
   CLI. Send a fresh message from the peer, run `receive --send-read-receipts`, and verify the
   peer sees it read and the recipient's linked devices reflect that state.
2. Disable read receipts on the phone, sync and restart again. Use a fresh message: the peer
   must receive no read receipt, while the recipient's other devices should receive read sync.
   Repeat with a group message, checking the original sender's receipt state.
3. With the setting disabled, receive fresh messages through MCP or the daemon, then invoke
   `mark_read` or `POST /v1/mark-read`. Local unread entries must clear without peer receipts;
   `senders` counts submissions even when peer delivery is suppressed. Read-sync failures are
   only logged by the pinned backend, so a successful response alone cannot prove sync.
4. Enable, sync and restart once more to verify the setting changes in both directions. Record
   backend and observations; remove disposable peers/groups afterwards.

The setting is learned from storage-service account records during `account sync` and background
storage refresh. The pinned backend does not apply configuration-sync messages directly. Until
an account record is learned, its existing default permits peer READ receipts. Delivery receipts
and explicit `receipts send-viewed` requests are independent of this READ-only setting policy.

## PNI invitation decline live check

Run with disposable groups and two accounts, first with the pure-Go binary and then
with the cgo binary. Synchronize or receive the group key on the invitee account.

1. Have an administrator invite the other account by its phone-number identity (PNI).
   Inspect `groups show <id> -o json` on the administrator account and confirm that
   the pending entry has the invitee's exact PNI. A full member or ACI-only entry
   does not satisfy this scenario. Record the revision and other pending entries.
2. As the selected invitee, run `groups leave <id> --yes`. Confirm the plain result
   says the invitation was declined. On a fresh invitation repeat with `-o json`
   and verify `left.membership` is `pending` and the returned revision advances.
   If multiple linked accounts exist, select the invitee with `--account`.
3. On the administrator account, fetch fresh group state and verify the exact PNI
   entry is removed and unrelated invitations remain. Check the administrator and
   invitee phones. A repeat decline should fail without changing the group.
4. If the server permits both own ACI and PNI invitations, repeat and verify both
   are removed by one decline. Verify an invitee cannot use `--promote`. Also check
   an ACI invitation and ordinary member/admin leaving on disposable groups.
5. Clean up the groups. Record backend, commands, revisions and phone observations.

List/show/join and leave recognize the selected account’s typed ACI/PNI invitations.
A PNI invitation reports `pending`; full ACI membership takes precedence. Success confirms the group
patch; signalmeow logs member notification failures separately. Keep phone acceptance
open in PLAN.md until both backends and an actual PNI fixture have been verified.

## PNI self-membership live check

Offline tests cover typed identity matching, offered roles, full-member precedence,
selected accounts, list/show plain and JSON output, and the invitation guard in join
on both backends. The following server/phone check remains unrun until a phone session.

Use two disposable Signal accounts, an administrator and an invited account, with the
latter linked to go-signal. Run the procedure with `just build` and `just build-cgo`,
using separate temporary data directories. The accounts must have their group keys
through sync or receive; a cached title alone does not make full state readable.

1. From the administrator, create a disposable group and invite the linked account by
   phone number. Inspect the administrator's `groups show <id> -o json` and verify that
   the invitation actually has the invited account's typed `pni` rather than `aci`.
   An ACI invitation does not exercise this case.
2. With the invited account selected using `--account`, run `groups list` and
   `groups show <id>` in plain and JSON output. Expect `invited` in plain output,
   `membership: "pending"` and the offered `role` in JSON, with the invitation's
   original `pni` retained. Compare the invitation and offered role on the phone.
3. Obtain an enabled group link from the administrator and run `groups join <link>`
   for the same known group. Expect acceptance guidance, empty stdout, and no revision
   or membership change. Run `groups accept <id>` and verify both the CLI and phone
   show full membership; list/show now report the full ACI member's role.
4. When two accounts are linked locally, select the other account and verify it does
   not inherit the invited account's PNI membership or role. If the server refuses
   full-state reads for that account, verify that refusal is preserved.
5. If the server permits simultaneous ACI and PNI invitations, verify that reporting
   prefers the ACI invitation's offered role. Once full ACI membership exists, confirm
   a stale PNI invitation cannot override it. Otherwise record these cases as offline-only.
6. Remove the disposable group and account data after checking the phone. Record the
   backend, actual typed invitation, outputs and server/phone result separately from
   offline checks; leave the PLAN.md phone-acceptance item open until both runs succeed.

## PNI invitation revocation live check

Use disposable administrator and invitee accounts on both the pure-Go and cgo builds,
with separate data directories. Record backend versions, account selection and fresh
`groups show <id> -o json` before each change. These phone checks have not been run.

1. Have the administrator invite the other account by phone number. Confirm its invitation
   appears in `pending` with `pni`, rather than `aci`; an ACI-only fixture does not exercise
   PNI revocation. Record the invitation's PNI from plain or JSON `groups show` output.
2. As that full administrator, run `groups remove-members <id> PNI:<uuid> PNI:<uuid>`.
   Confirm one revision increment, only the matching PNI invitation removed, and unrelated
   invitations, full members, requests and settings preserved. Confirm on both phones
   that the invitation was revoked and the invitee cannot accept it.
3. Re-invite the disposable peer by PNI. Run `groups remove-members <id> <peer-number>`.
   Confirm number resolution supplies its PNI and removes the invitation in one revision.
   If number discovery is unavailable, use the explicit PNI from step 1 and record the
   limitation; do not count that as passing the number-resolution check.
4. When the server permits ACI and PNI invitations for the same peer, revoke using its
   number and confirm both disappear in one change. A bare ACI must leave the separate
   PNI invitation untouched. If the peer is already a full ACI member with a stale PNI
   invitation, explicit PNI revocation must preserve that full membership; number removal
   removes all matching membership and invitations.
5. Select an ordinary member and an invitee with an offered admin role. Each attempt must
   fail without changing revision or group state. A batch with a valid peer plus an absent
   PNI must fail atomically. A batch naming the selected account's own ACI or PNI must
   direct it to `groups leave`, including when another local account is also registered.
6. Check plain and JSON output against the refreshed server state. On a conflict or uncertain
   network failure, inspect fresh state before retrying; do not assume a failed command
   proves no server change occurred. Confirm removal notifications on the phones.
7. Restore invitations and memberships as needed, then remove the test peers and leave the
   disposable groups. Keep phone acceptance open until both backends pass these observations.

## Story reception live check

This is an opt-in production phone session, not part of the offline checks. Run it for both
cgo and pure-Go binaries using separate linked data directories and disposable test accounts.
Story sending from go-signal is still planned and is not covered by this procedure.

1. Have a peer's phone share a text story with the test account. Run `receive -o json` and
   confirm one `story` event has the peer ACI, sender timestamp, reply permission, text card
   and any solid/gradient background. Compare plain output, including Unicode and mentions.
2. Share image and video stories with captions. Confirm their attachment metadata and save
   them with `--download-attachments`; compare downloaded bytes with the original files.
   Share a text card with a link preview and verify its preview image uses the same path.
3. Share a group story in a disposable group. Confirm the group ID/title and retained key;
   check `groups show`. A group story from a blocked member follows the existing group
   message policy, while a direct story from a blocked sender is omitted.
4. Send private and group stories from the test account's phone. Confirm `sync: true`, the
   phone's sent timestamp, group routing and the account's own chat for private transcripts.
   Do not interpret the private transcript chat as its audience.
5. With `--send-read-receipts`, print stories and an ordinary incoming message. Confirm that
   only the ordinary message generates READ receipts and no story VIEWED receipt is sent.
6. Receive stories through `mcp serve` or `daemon serve`, then restart and list retained
   entries. Confirm story text/media metadata survives, remains outside message unread
   counts, and JSON contains no attachment keys, digests or group keys. Entries/files remain
   after phone expiry until explicitly pruned/deleted; this is an event log, not a story feed.
7. Interrupt before a pending story is consumed and reconnect; confirm it is delivered
   again. Consume it, close cleanly and confirm it is acknowledged. Include a failed CDN
   download and verify that a later ordinary message still arrives.

Record server/phone observations and backend versions. Restore the phone's block list and
remove the disposable group, stories, linked devices and saved media. This procedure has not
been run; offline tests do not establish production delivery, rendering or phone expiry.

## Group story sending live check

This procedure is pending; offline transport tests do not establish phone display.
Use disposable accounts and a disposable V2 group, then run with each backend
(`just build` and `just build-cgo`) in separate data directories. Arrange a linked phone
with stories enabled and one other group member before testing.

1. Sync the account, obtain the canonical group ID from `groups list`, then send:

   ```sh
   go-signal stories send --group '<base64-id>' -m 'Group story check'
   go-signal stories send --group '<base64-id>' --attach test-photo.png --no-replies -o json
   go-signal stories send --group '<base64-id>' --attach test-video.mp4
   ```

2. Check that full group members see each story in the group's story feed, the text card
   has default-font white text on black, media is intact, and reply permission follows
   the supplied flag. Compare the returned millisecond timestamp with received story
   events on a separately linked device. Own-phone stories must appear as sent stories,
   without ordinary chat messages or an audience wider than the chosen group.
3. Include a pending invitation and a join request. Neither should receive the story.
   Select another linked account that is not a full member: it must fail before upload.
   A changed peer identity must fail until explicitly trusted. Check a group containing
   only yourself still receives its own-device transcript.
4. Set a short chat disappearing timer, then send a story. Verify the story follows the
   phone's story expiry rather than that timer. Restore the timer afterwards. Text
   mention syntax must remain literal; story sending supports no custom card style,
   preview, caption or private distribution-list audience yet.
5. Queue an ordinary message and a story before running `stories send`. Drain them with
   `receive` afterwards and verify they were not consumed by the send-only command.
   For a controlled peer or transcript submission failure, inspect member outcomes and
   the command's nonzero exit status. Do not automatically retry a partly submitted
   story: resubmission can create duplicates.
6. Remove the test stories on the phone, restore settings and delete/unlink disposable
   devices. Record backend, phone version, timestamps, visible audience and cleanup.

## Private story sending live check

Not yet run. Use disposable linked accounts and repeat with both pure Go and cgo.

1. On the primary phone create a custom story audience containing two disposable peers,
   and configure My Story first as selected contacts, then as all contacts except one peer.
   Run `sync`, then `stories audiences`; compare expanded ACIs and reply settings with the
   phone. Select a second linked account and confirm its own lists are used.
2. Send a text card with `stories send --distribution-list <uuid> -m 'Private story'`,
   then send an image and video. Check the intended peers' story feeds and your own phone's
   sent feed, distribution audience, timestamp and reply controls. Repeat with `--my-story`
   and `--no-replies`; a list with replies disabled must never be enabled by the command.
3. Verify exclusions: self, blocked, hidden, unregistered and My Story excluded peers must
   receive nothing. Change the phone audience, delete a custom list, or remove all members,
   then verify the next invocation uses fresh storage or refuses before media upload.
   An unknown My Story policy must refuse rather than choose all contacts.
4. Exercise a changed peer identity: it must fail until trusted. Arrange a failed peer and
   a failed own-device transcript independently and together; inspect plain/JSON outcomes
   and nonzero exit status. Confirm the transcript's manifest includes the intended peers
   and selected distribution ID. Never rerun a partly successful command without checking
   the phone, because accepted peer stories can be duplicated.
5. Queue ordinary messages and stories, perform a send, then receive them; send-only mode
   must leave both queued. Check literal mention text, independent story expiry, and no
   automatic READ/VIEWED receipts. Test storage unavailable/incomplete: persisted snapshots
   must not become a delivery fallback.
6. Remove the test stories, restore audience/privacy settings, and unlink/delete disposable
   accounts. Record both backend results before checking off phone acceptance in PLAN.md.

### Sticker extras live check

This acceptance check has not been run. Use the disposable accounts and both backends from
the [sticker live check](#sticker-live-check); never record share links or pack keys in PLAN.md.

1. Install a known static pack and an animated pack with `stickers install '<link>'`.
   List in plain and JSON; verify title/author, cover and IDs, including zero. Repeat on a
   second selected account and confirm caches are separate. Phone installation state should
   remain unchanged because installation is local.
2. Restart, block CDN access and reinstall the same link; cached fetching should still work.
   Restore access and send a cached sticker to self/peer/group. Confirm image, emoji,
   animation and normal own-device message sync. Try a wrong key and failed pack/image
   retrieval; the installed cache must remain intact and must not supply bytes for that key.
3. Receive a peer sticker with `--download-attachments`. For a retained message with an
   expired embedded image, call MCP `sticker_get`; verify authenticated pack fallback, actual
   MIME/size/path and no read receipt. Real CDN expiry is required for phone acceptance;
   synthetic expired/integrity/cancellation cases are covered by offline tests.
4. Exercise all four MCP sticker tools. Check read-only list/get, omitted install/send,
   confirmation acceptance/decline and rejected recipients before fetch/upload. Inspect output
   and logs for key omission; confirm downloads preserve existing files and reject non-message
   events. Stop/restart receiving and confirm the private inbox retained enough data for fallback.
5. Repeat with the other backend on the same dedicated account, restore network settings and
   clean up disposable accounts. Record actual evidence before marking acceptance complete.
