# go-signal — Implementation Plan

Goal: a Signal command-line client in Go, inspired by
[signal-cli](https://github.com/AsamK/signal-cli), that runs **without a Java runtime** and ships
as a single binary. It is **not** a drop-in replacement. The CLI is idiomatic Go/Cobra with its own
command names and JSON schema. signal-cli serves as the reference for protocol behaviour and
features.

The upstream Java code is vendored as a shallow submodule in `reference/signal-cli` (read-only
reference, pinned at `29dcac2`, libsignal `0.102.1`).

Legend: `[x]` done · `[ ]` open

---

## 1. Decisions

| Date       | Decision                                                                                                                                                                 |
| ---------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 2026-09-25 | Crypto and protocol come from `go.mau.fi/mautrix-signal/pkg/signalmeow` + `libsignalgo` (CGO → `libsignal_ffi.a`). See §1.1.                                             |
| 2026-09-25 | License: **AGPL-3.0** (required by signalmeow).                                                                                                                          |
| 2026-09-25 | **No strict drop-in compatibility.** Idiomatic CLI (noun-verb subcommands, kebab-case), with our own documented JSON output.                                             |
| 2026-09-25 | Priority: **plain CLI send/receive** first. The daemon/JSON-RPC is deferred.                                                                                             |
| 2026-09-25 | **Linked device only.** Primary registration (`register`/`verify`) is deferred indefinitely.                                                                             |
| 2026-09-25 | Pinned `go.mau.fi/mautrix-signal v0.2609.0`, which expects **libsignal `v0.102.2`**; `third_party/libsignal` is pinned to that tag.                                      |
| 2026-09-25 | **MCP server** (`go-signal mcp serve`) in the same binary, after contacts/groups and before release. See Phase 5.                                                        |
| 2026-09-25 | Later stage: **pure-Go backend** from a fork of `GoCodeAlone/libsignal-go` plus zkgroup/attestation/HPKE ports, behind a `purego` build tag. See Phases 7–10.            |
| 2026-09-26 | **Blocking** goes out as a complete `SyncMessage.Blocked` to our own devices; the phone applies it and writes the storage service. No storage-service writes of our own. |
| 2026-09-26 | **Identity trust is TOFU**: a changed key blocks sending to that user until `identities trust`; receiving keeps working.                                                 |
| 2026-09-26 | **MCP write tools send to nobody by default**: only `--allow-recipient` entries are allowed; `--allow-recipient '*'` opts in to everyone.                                |
| 2026-09-26 | **`mcp serve --read-only`** drops every tool that sends (`send_message`, `react`, `delete_message`, `mark_read`); `attachment_get` stays (it only writes locally).       |
| 2026-09-26 | **go.mod always replaces `go.mau.fi/mautrix-signal`** with `github.com/cwbudde/mautrix-signal` (tag `vX.YYMM.Z-purego.N`). The cgo build compiles upstream's code.       |
| 2026-09-26 | **Purego builds use `modernc.org/sqlite`** (mattn/go-sqlite3 needs cgo); same connection options as the cgo driver.                                                      |
| 2026-09-26 | `cwbudde/libsignal-go` pins its compat harness to **the libsignal tag libsignalgo expects**, not upstream's latest; fork tags are `vX.Y.Z-cw.N`.                         |

### 1.1 Why signalmeow

signal-cli is a thin CLI on top of **libsignal** (Rust: Signal Protocol, zkgroup, sealed sender,
PQXDH/Kyber, SPQR ratchet, CDSI/SVR attestation) and **signal-service-java** (REST and websocket
client). A pure-Go reimplementation of libsignal would mean a large amount of security-critical
code to write and maintain. signalmeow is actively maintained, runs in production in the mautrix
Signal bridge, and already covers linking, websockets, sealed sender, groups v2, attachments,
storage service, contact discovery and sender keys. Its store is SQL with a SQLite dialect.

Consequences:

- **CGO + Rust toolchain at build time.** `libsignal_ffi.a` is built from `signalapp/libsignal` at
  the exact version `libsignalgo` pins. Rust/cargo is **not installed on this machine yet**. End
  users still get a single binary with no Java, and optionally a fully static musl build.
- **API churn.** signalmeow is built for a bridge. We isolate it behind our own `internal/signal`
  facade so that upstream changes stay in one package.

### 1.2 Spike findings (Phase 1.4)

- **API shape.** `signalmeow.PerformProvisioning(ctx, deviceStore, name, allowBackup)` returns a
  channel: first the URL, then the `DeviceData` (already persisted via `PutDevice`, plus identity
  keys, signed/last-resort prekeys and our profile key). Receiving is
  `NewClient(device, zerolog, handler)` + `StartReceiveLoops(ctx)`, which returns a status channel;
  events arrive on the handler callback as `events.SignalEvent` (`ChatEvent` wrapping a
  `signalpb.DataMessage`/`EditMessage`/`TypingMessage`, `Receipt`, `ReadSelf`, `Call`,
  `DecryptionError`, `ContactList`, `QueueEmpty`, `LoggedOut`, …). Our own sent messages (sync
  transcripts) come through as `ChatEvent`s too.
- **Store.** `store.NewStore(dbutil.Database, logger)` + `Upgrade(ctx)` creates its tables with a
  `signalmeow_version` table (27 migrations at v0.2609.0), so our own tables can sit next to it
  with a separate `dbutil` version table. One DB holds any number of accounts (keyed by ACI).
- **Logging.** signalmeow logs through zerolog from the context (`zerolog.Ctx`) and
  `Client.Log`. `internal/signal.NewZerologBridge` forwards to slog; its info-level chatter is
  demoted to debug. Cancelled contexts are logged by signalmeow at error level (e.g. Ctrl-C during
  `link`), so a real facade needs to filter those.
- **Prekeys.** Linking uploads only signed and last-resort Kyber prekeys; one-time prekeys are
  generated and uploaded by `keyCheckLoop` on the first connect. The bridge connects right after
  linking; we do too since 3.9 (for the initial sync, unless `--sync-timeout 0`).
- **Acks.** The handler's return value decides whether the envelope is acked, and acks go out
  asynchronously, so closing right after the handler returns can lose the ack. The spike sleeps
  1 s; Phase 3.1 needs a proper drain. (Done in 3.1: a keepalive round trip flushes the acks.)
- **Gaps.** No QR refresh (the server drops the provisioning socket after ~60 s; the bridge
  reprovisions every 45 s). The DB file was created 0644 (fixed in Phase 2.2). Linking with
  `allowBackup=false` skips message history transfer.

## 2. Target layout

```
main.go                  -> cmd.Execute()
cmd/                     Cobra commands (link.go, send.go, receive.go, contacts.go, ...)
internal/signal/         facade over signalmeow: Account, Client, events -> our own types
internal/signal/signaltest/  in-memory fake Client for tests (no CGO)
internal/store/          data-dir layout, SQLite (signalmeow store + our own tables)
internal/output/         plain / json renderers
internal/app/            use-case layer shared by the CLI and the MCP server (send, react, list, ...)
internal/mcp/            MCP server: tool/resource definitions, inbox, safety policy
third_party/libsignal/   submodule: signalapp/libsignal at the version libsignalgo expects
reference/signal-cli/    submodule: upstream Java implementation (reference only)
```

Conventions (same as go-sq-tool / ewws-template): Cobra + Viper (flag > `GOSIGNAL_*` env >
`$XDG_CONFIG_HOME/go-signal/config.yaml`), `log/slog` to **stderr** (stdout belongs to command
output), `just` recipes, treefmt (gofumpt, gci, prettier, taplo, yamlfmt), golangci-lint with
`default = 'all'`, reusable GitHub workflows (`tests.yaml` → `test-*`).

## 3. CLI design

Noun-verb subcommands with kebab-case names. Global flags: `-a/--account`, `-o/--output plain|json`,
`-v/--verbose`, `--data-dir`, `--config`, `--log-format`.

```
go-signal link [--name <device-name>] [--sync-timeout 60s]   # print sgnl:// URI + terminal QR, wait for scan, sync
go-signal send <recipient>... -m <text> [--attach <file>]... [--group <id>] [--quote <ts>]
go-signal send --stdin <recipient>             # message body from stdin
go-signal send <recipient>... --edit <timestamp> -m <replacement> [--group <id>]
go-signal receive [--timeout 5s] [--max N] [--follow] [--download-attachments <dir>] [--send-read-receipts]
go-signal react <recipient>... --target <author>:<ts> --emoji 👍 [--remove] [--group <id>]
go-signal delete <recipient>... --target <ts> [--group <id>]   # remote delete of our own message
go-signal contacts list [--blocked] [--query <q>] | show <recipient> | block <recipient>... | unblock <recipient>...
go-signal groups list | show <group> | leave <group> --yes [--promote <member>]...   # <group>: ID, master key or title
go-signal groups create <title> [--member <number|ACI|@username>]...
go-signal groups rename <group> <title>
go-signal groups update <group> [--description <text>] [--timer <seconds>] [--avatar <file> | --remove-avatar]   # combine settings in one patch
go-signal groups join <link>
go-signal groups accept <group>
go-signal groups link show <group>
go-signal groups link update <group> [--state disabled|enabled|enabled-with-approval] [--reset]
go-signal groups add-members <group> <recipient>...
go-signal groups remove-members <group> <recipient>...
go-signal devices list
go-signal identities list [<recipient>] | show <recipient> | trust <recipient> [--safety-number <n>]
go-signal account show | sync [--timeout 60s] | unlink   # unlink = remove local data
go-signal mcp serve [--read-only] [--allow-recipient <r>]...   # MCP server on stdio
go-signal mcp doctor [<mcp serve flags>] [--offline]   # check the MCP setup; exit 0/1/3
go-signal version
```

Recipients are E.164 numbers, ACI UUIDs, or `@username`. JSON output uses its own schema, which is
documented in `docs/json.md` and versioned so that scripts can rely on it.

## 4. Phases

Each phase is split into subphases that can land as separate PRs. A subphase ends with a
**Done when** line: the observable result that closes it. Subphases within a phase are ordered by
dependency; items without a dependency on each other can go in parallel.

### Phase 0 — Scaffold

- [x] Repo, `reference/signal-cli` submodule, go.mod, Cobra/Viper/slog root command, `version`
- [x] justfile, treefmt, golangci-lint, CI (build, unit, lint, format)
- [x] License: AGPL-3.0
- [x] Create GitHub repo (`github.com/cwbudde/go-signal`) and push

### Phase 1 — Build foundation and link spike

#### 1.1 Toolchain and libsignal build

- [x] Install Rust locally (rustup)
- [x] Look up the libsignal version that the pinned `libsignalgo` expects (its `version.go` /
      submodule pointer) and record it in §1
- [x] Add `third_party/libsignal` submodule at exactly that tag
- [x] `just libsignal`: `cargo build --release -p libsignal-ffi`, copy `libsignal_ffi.a` to
      `third_party/lib/`. Skip the build when the `.a` was built from the current submodule HEAD
      (SHA stamp file next to the `.a`).
- [x] Export `CGO_LDFLAGS=-L third_party/lib` (and `CGO_ENABLED=1`) from the justfile so that
      `just build` / `just test` work without manual env setup
- [x] `.gitignore` for `third_party/lib/` and the cargo `target/` dir (`target/` is already
      ignored by the submodule's own `.gitignore`)

**Done when:** a fresh clone builds with `git submodule update --init && just libsignal && just build`.

#### 1.2 signalmeow dependency and version guard

- [x] `go get go.mau.fi/mautrix-signal@<tag>` (pinned tag, not a pseudo-version of `main`)
- [x] Build tag / file that imports `libsignalgo` so the link step is exercised in `just build`
      (`internal/signal/libsignal.go`, `//go:build cgo`)
- [x] Version guard: a test (or `just check-libsignal`) that compares the submodule tag with the
      version `libsignalgo` reports at runtime and fails on mismatch
- [x] Document the upgrade procedure (bump mautrix → bump submodule → rebuild) in `docs/dev.md`

**Done when:** the binary links against `libsignal_ffi.a` and the version guard passes.

#### 1.3 CI with CGO

- [x] Install the Rust toolchain in CI (pinned via `rust-toolchain.toml` from the libsignal
      submodule). `rustup toolchain install` runs inside the submodule, and `just libsignal`
      runs cargo from there so the pin also applies locally.
- [x] Cache `third_party/lib/libsignal_ffi.a` keyed on the submodule SHA + runner OS/arch
      (`git rev-parse HEAD:third_party/libsignal`, no checkout needed for the key)
- [x] Split jobs: pure-Go unit tests (`CGO_ENABLED=0`, fast, against the fake facade) and CGO
      build + tests. `test-unit` runs without `-race` (the race detector needs cgo); `test-cgo`
      runs `just build` + `just test` and replaces `test-can-build`. There is no fake facade yet,
      so for now the pure-Go job covers `cmd/` only.
- [x] Also install `protoc`/`clang` if libsignal's build needs them on the runner (it needs
      `protoc` for prost; `clang`/`cmake` are installed on cache misses as well)
- [x] `check-libsignal` compares commits instead of `git describe`, because the shallow CI
      clone has no tags. It fetches the expected tag when it is missing.

**Done when:** CI is green on a PR, and a cache hit brings the CGO job under ~3 minutes.
(Workflows pass actionlint; not yet verified on GitHub.)

#### 1.4 Link and receive spike

- [x] Minimal `internal/store`: open a SQLite db at a hard-coded path under `--data-dir` and run
      signalmeow's store migrations (`<data-dir>/signal.db`, mattn/go-sqlite3, WAL + FKs +
      `busy_timeout`; dir created 0700)
- [x] `link`: run signalmeow provisioning, print the `sgnl://linkdevice?...` URI and a terminal
      QR code (e.g. `github.com/mdp/qrterminal`), wait for the phone to scan, persist the device
- [x] `receive`: connect the websockets, print every incoming event with `%+v`, exit after the
      first data message or a timeout (`--timeout`, default 1m)
- [x] Write down findings (API shape, surprises, gaps vs. signal-cli) in §1 or a new §1.2 (see
      §1.2; to be completed after the phone test)

**Done when:** a phone links the device and a message sent from it shows up in `receive`.
Spike code may be thrown away; the next phases rebuild it properly.
(Verified up to the QR code against the live server; the phone scan and receive are not yet
tested.)

### Phase 2 — Account and storage

#### 2.1 Facade skeleton (`internal/signal`)

- [x] Define our own types: `Account`, `Recipient`, `Event` (sum type: message, receipt, typing,
      reaction, edit, delete, sync, …), `SendRequest`, `SendResult`
      (`types.go`, `events.go`; sync transcripts are `Message`s with `Envelope.Sync`, read syncs
      are `ReadSync`, connection changes incl. logout are `Connection` events)
- [x] `Client` interface used by `cmd/` (link, connect, send, events channel, close), plus
      `Account` to read the selected account without connecting
- [x] signalmeow-backed implementation (CGO, `signal.Open`) and an in-memory fake for tests (no
      CGO, `internal/signal/signaltest`). `Send` on the real client returns `ErrNotImplemented`
      until Phase 3.3.
- [x] Inject the client factory via the root command so `cmd` tests use the fake
      (`cmd.NewRootCmd(cmd.WithClientFactory(fake.Factory))`)

**Done when:** `link` and `receive` from the spike run through the facade, and `cmd` tests run
with `CGO_ENABLED=0`. (Done; `link` verified up to the QR code against the live server.)

Notes: the real client's handler blocks until the consumer reads the event from `Events()`, and
only then acks the envelope, so unread events are redelivered next time. `Close` drains the
connection (Phase 3.1). `-a` already selects by
number or ACI (`ErrAccountNotFound`); the multi-account rules followed in 2.3.

#### 2.2 Data-dir layout and permissions

- [x] Resolve `--data-dir` (default `$XDG_DATA_HOME/go-signal`; `~/` expanded, made absolute;
      `store.DefaultDir`, `store.OpenDir`)
- [x] Layout: `<data-dir>/<aci>/account.db` (SQLite, WAL, `busy_timeout`) and
      `<data-dir>/accounts.json` mapping number ↔ ACI ↔ device ID (versioned, written atomically)
- [x] Create dirs 0700 and files 0600; warn (don't fail) when existing perms are looser
      (`account.db` is pre-created 0600; SQLite gives `-wal`/`-shm` the same mode)
- [x] Lock file per account so two processes don't run receive loops on the same account
      (`<data-dir>/<aci>/lock`, non-blocking `flock`, holder PID in the error; best effort on
      non-unix)
- [x] Our own tables (schema migrations via `dbutil`) next to signalmeow's, for later
      metadata (e.g. last-seen timestamps, trust decisions) (`gosignal_version`, v1 creates a
      `gosignal_meta` key/value table)

**Done when:** linking writes the layout above, and a second concurrent `receive` fails with a
clear "account in use" error. (Done; `signal.ErrAccountInUse`.)

Notes: the ACI is only known once the phone confirms the link, so linking uses a
`store.LinkStore` (a signalmeow `DeviceStore`) that takes the lock and opens
`<aci>/account.db` inside `PutDevice`; relinking an account that is connected elsewhere
therefore fails. The client opens the account database on first use and takes the lock in
`Connect` (reading the account doesn't need it). The spike's `<data-dir>/signal.db` is not
migrated; relink after upgrading.

#### 2.3 Account selection

- [x] `-a/--account` accepts E.164 or ACI; resolve it via `accounts.json`
      (`signal.SelectAccount`; anything else is `ErrInvalidAccount`, ACIs match case-insensitively)
- [x] With exactly one linked account, `-a` is optional; with several, it's required
      (`ErrAccountRequired`, listing number and ACI of each)
- [x] `link` into an existing data dir adds a second account instead of overwriting (relinking
      the same ACI replaces its entry)

**Done when:** commands pick the right account with zero, one and two linked accounts (tests with
the fake). (Done; the fake and the real client share `signal.SelectAccount`, and the real client
is also tested against a seeded two-account data dir.)

#### 2.4 Account commands

- [x] `internal/output`: plain and JSON renderers, `-o` flag, and `docs/json.md` with a schema
      version field (`output.Printer`; every JSON document has `"version": 1`; an unknown `-o`
      fails before anything runs with `output.ErrInvalidFormat`; plain times in local time)
- [x] `account show`: number, ACI, PNI, device ID, device name, registration/link date (offline;
      name and link date come from `accounts.json`, which `link` now records)
- [x] `devices list`: all devices of the account (id, name, created, last seen), mark ours
      (`Client.Devices`, REST `GET /v1/devices/` with the device credentials, no receive loop)
- [x] `account unlink`: remove our device from the account if possible, then delete local data
      (with `--yes` confirmation guard) (`Client.Unlink`: `DELETE /v1/devices/<id>`, then
      `store.RemoveAccount` under the account lock; `--local-only` skips the server, and a
      server error keeps the local data and suggests `--local-only`)

**Done when:** all three commands work in plain and JSON mode with golden-file tests. (Done;
goldens in `cmd/testdata`, regenerate with `UPDATE_GOLDEN=1 just test`.)

Notes: device names and creation times are encrypted to our ACI identity key. signalmeow's
`DecryptDeviceName` never verifies (its synthetic-IV check is wrong), so `internal/signal` has its
own; the creation time is HPKE-sealed, which libsignalgo doesn't wrap, so `hpke.go` calls
`signal_privatekey_hpke_open` directly. Values that don't decrypt are shown as unknown. `link` and
`receive` don't use the renderers yet (`receive` gets them in 3.5). `devices list` and a non-local
`unlink` are not yet verified against the live server.

#### 2.5 Remote unlink handling

- [x] Detect "device removed" (403/logged-out event from signalmeow) during connect and receive
      (a websocket 403 arrives as `SignalConnectionEventLoggedOut`, a 401 only as a fatal error
      whose text is matched, and `events.LoggedOut` after a prekey 422; all become a
      `StateLoggedOut` Connection event. REST 401/403 in `devices list` count too. signalmeow's
      "Authed/Unauthed websocket …" error logs are demoted to debug)
- [x] Report it as a dedicated sentinel error and a non-zero exit code
      (`signal.ErrDeviceUnlinked`, replacing `ErrLoggedOut`; `signal.UnlinkedError` adds the
      number and the fix. `cmd.ExitCode` maps it to `cmd.ExitUnlinked` = 3, anything else to 1;
      documented in the README)
- [x] Mark the account as unlinked in `accounts.json`; `account unlink` then only cleans up locally
      (`unlinkedAt` via `store.MarkUnlinked`, carried as `Account.UnlinkedAt`; relinking the ACI
      replaces the entry and clears it. `receive` and `devices list` then fail fast without a
      connection, `account show` shows a `Status:` line / `unlinkedAt`, and `account unlink`
      skips the server and reports `localOnly: true`)

**Done when:** unlinking from the phone produces a clear message on the next command instead of a
stack of websocket errors. (Done; exit code 3 with "this device was unlinked from the account
<number>; run `go-signal -a <number> account unlink --yes --local-only` …". Tested with the fake
and a seeded data dir; not yet verified against the live server.)

Notes: `Connect` doesn't wait for the websocket, so an unlink detected while connecting still
arrives as a `StateLoggedOut` event (which `receive` returns as the error), not from `Connect`.
When signalmeow clears the credentials of a logged-out device, `account show` falls back to
`accounts.json`.

### Phase 3 — Send and receive (the core)

Since 5.1, the command logic goes into `internal/app` (typed request → typed result, tested
against the fake); `cmd/` only parses flags, calls `app` and renders via `internal/output`.

#### 3.1 Connection lifecycle

- [x] Root context cancelled on SIGINT/SIGTERM; second signal forces exit (`cmd.SignalContext`;
      the forced exit uses code 128 + signal, e.g. 130 for SIGINT. An interrupted `receive` ends
      normally with 0)
- [x] Connect/disconnect in the facade with bounded retries and backoff for transient errors
      (a `supervisor` watches the receive loops: signalmeow's websockets retry transient errors
      themselves, 10 s growing to 1 min; after a fatal error such as an unexpected 4xx, the
      supervisor restarts the loops on a fresh signalmeow client with exponential backoff, 2 s up to
      1 min, and gives up after 5 failed attempts with a final `StateFailed` event wrapping
      `signal.ErrConnectionFailed`)
- [x] Graceful shutdown: drain in-flight sends, ack received envelopes, close the db cleanly
      (`Close` refuses new sends (`ErrClosed`) and waits up to 5 s for running ones, stops handing
      out events, flushes the acks with a `GET /v1/keepalive` round trip, then closes the
      websockets and only then the database. The loops run on a context detached from the
      command's, so Ctrl-C no longer cuts them mid-ack. Since the Phase 4 review, the database is
      only closed once every running method that uses it has returned, also past the 5 s: sends
      still running then fail fast on the closed websockets)
- [x] `-v` logs connection state transitions via slog (`connection state` / `reconnecting` at
      debug level)

**Done when:** Ctrl-C during `receive --follow` exits within ~1 s without losing acked messages,
and a dropped network connection is recovered automatically. (Done with the fake and unit tests
for the supervisor; not yet verified against the live server.)

Notes: `Events` is unbuffered now, so an event is only acked once the consumer took it; before,
up to 64 buffered events were acked but lost on exit. A lost ack is harmless: signalmeow keeps
the hash of every handled envelope in its event buffer and drops a redelivered one. The ack
flush matters because signalmeow clears that buffer entry (a DB write) after the handler
returns, on the loops' context, so closing too early caused duplicates. The keepalive response
only proves that requests queued before it were written; an ack whose buffer update takes longer
than the round trip could still miss it. signalmeow notices a silently dead connection only after
5 failed pings (about 2.5 min). `receive --follow` (from 3.6) landed here for the Done-when test.

#### 3.2 Recipient resolution

- [x] Parse E.164, ACI UUID, `@username`, and `group:<id>` into a `Recipient`
      (`app.ParseRecipient` → `app.Target`: a `signal.Recipient`, a group ID or self; strict
      `+<digits>` E.164, lower-cased ACI, group IDs of 32 bytes in standard or URL-safe base64,
      normalized to standard. Invalid arguments fail with `app.ErrInvalidRecipient`)
- [x] E.164 → ACI via CDSI (signalmeow contact discovery), cached in the store
      (`Client.Resolve`: cache hits come from signalmeow's recipient table, misses go through
      `signalmeow.Client.LookupPhone` and are written back with `UpdateRecipientE164`. CDSI needs
      the authed websocket, so uncached numbers need `Connect` first (`ErrNotConnected`))
- [x] Username → ACI lookup (libsignal's `signal_username_hash` via cgo, since libsignalgo doesn't
      wrap it, then the unauthenticated `GET /v1/accounts/username_hash/<base64url>`; no
      connection needed; invalid usernames fail with `signal.ErrInvalidUsername` before any request)
- [x] Note-to-self as a recipient (`self` / own number) (also the own ACI; `app.ResolveRecipients`
      turns them into a `Self` target carrying the own ACI and number)

**Done when:** every recipient form resolves to an ACI (or group) in unit tests, and unknown
numbers give a clear "not on Signal" error. (Done: `internal/app` tests against the fake's
`Directory`, plus cgo tests for the username hash, the lookup response and store-cached numbers.
Errors name the recipient, e.g. `+4915100000000: not on Signal`, and all failing recipients are
reported together. Not yet verified against the live server.)

Notes: `app.ResolveRecipients` drops duplicates (same ACI or group), keeping the order. Usernames
aren't cached (signalmeow's recipient table has no column for them). CDSI returns no ACI for
users who hid their number from discovery; that is reported as "not on Signal" too. For 3.3:
while a command doesn't read `Events`, signalmeow's websocket read loop stalls once 256 incoming
requests are queued, which also blocks responses to our requests (like CDSI credentials), so
`send` must keep reading events (or Connect in a mode that doesn't hand them out).

#### 3.3 Send: text

- [x] `send <recipient>... -m <text>` to one or more contacts (`app.Send`: parses and resolves all
      recipients first; if one is invalid or not on Signal, nothing is sent. One timestamp for all
      recipients; `app.WithClock`/`cmd.WithClock` fix it in tests)
- [x] `--stdin` reads the body from stdin (trailing newlines stripped; excludes `-m`; an empty
      body fails with `app.ErrEmptyMessage`)
- [x] `--group <id>` sends to a group (sender keys via signalmeow) (repeatable; `group:<id>`
      arguments work too; `SendGroupMessage`, and a group we have no master key for fails with
      `signal.ErrUnknownGroup`)
- [x] Note-to-self and sync-sent transcript to our other devices (signalmeow sends the
      `SyncMessage.Sent` transcript after every send; a note-to-self is only that transcript, as
      in signal-cli; our profile key goes into every data message)
- [x] Output: timestamp per recipient, per-recipient success/failure; non-zero exit if any failed
      (plain table RECIPIENT/TIMESTAMP/STATUS/DETAILS, JSON `send` document in `docs/json.md`;
      group members are listed per group, and a failed member counts as a failure. Results are
      printed first, then `app.ErrSendFailed` gives exit 1; an unlinked device gives exit 3)

**Done when:** text arrives on the phone for 1:1, group and note-to-self sends. (Done with the fake
and unit tests; not yet verified against the live server.)

Notes: `send` connects with `signal.SendOnly()` (`Connect` takes `ConnectOption`s). In that mode
the handler returns false for every incoming event, so the envelope stays on the server (signalmeow
keeps the decrypted plaintext in its buffer) and comes with the next `receive`, and the websocket
read loop never blocks (the 256-request stall from 3.2). Connection events are only recorded; a
connection lost for good fails the next `Send`. signalmeow logs each deferred envelope as an error,
which the log bridge demotes to debug. Open: messages carry no disappearing-message timer
(`ExpireTimer`) because we don't track it per chat yet; untested whether the phone handles
same-timestamp sync transcripts across several chats well.

#### 3.4 Send: rich content

- [x] Edit sent messages with `send --edit <timestamp>` and MCP `send_message.editTimestamp`
      (2026-09-29). Sends a Signal `EditMessage` to users, groups and self, with a fresh
      timestamp and sync transcripts. Replacement text and rich content use the normal send
      path; the original content is not loaded. Rejects zero explicit targets, future targets,
      blank text and mixed reaction/delete edits. MCP allowlisting and confirmation also apply.
      Unit tests cover envelopes, validation, routing, partial failures and permissions;
      plain/JSON command goldens document the unchanged output shape.
- [x] Edit transport acceptance: opt-in `TestIntegrationEdit` passed on cgo and pure Go
      (2026-09-29), with self sync sends and peer delivery receipts for both the original and
      edited messages in direct and group chats.
- [ ] Verify edit rendering on the peer's phone separately from delivery receipts; media/quote
      edits are not yet verified live.

- [x] `--attach <file>` (repeatable): MIME sniffing, upload, size limit check (`app` reads the
      files before connecting: regular files up to `app.MaxAttachmentSize` = 100 MiB, the official
      clients' limit; the sniffed type wins unless it is generic (binary, plain text, ZIP), then
      the extension decides; GIF/JPEG/PNG get width and height. `Client.Upload` uploads each file
      once and returns handles that every recipient and group send reuses. `--attach` alone needs
      no text; it has no shorthand because `-a` is `--account`)
- [x] `--quote <author>:<ts>` quoting a previous message (`app.ParseQuote`; the author is a user
      recipient argument, `self` for our own messages, resolved to an ACI like recipients;
      optional `--quote-text` is what clients show when they no longer have the message)
- [x] Mentions: `@{<recipient>}` placeholders in the text → body ranges (users only: number,
      ACI, `@username` or `self`; each becomes U+FFFC with a mention range in UTF-16 units.
      Anything else in `@{…}`, like git's `HEAD@{1}` or a group, stays text; a mentioned user who
      isn't on Signal fails the send before anything is uploaded)
- [ ] Text styles (bold/italic/…) — optional and deferred; only pursue if cheap.
  - [ ] Choose the input syntax: markup or explicit `start:length:STYLE` offsets. Neither is
        decided; both currently need more design than they are worth.
  - [ ] Convert styles to Signal body ranges alongside mentions, preserving UTF-16 offsets.
  - [ ] Test overlapping ranges and non-ASCII text, document the syntax, and verify phone rendering.

**Done when:** attachments and quotes render correctly on the phone. (Done with the fake and unit
tests; not yet verified against the live server.)

Notes: the pointer gets content type, file name, dimensions and upload timestamp on top of what
signalmeow's `UploadAttachment` fills in; no thumbnail, blurhash, voice-note/GIF flags or
captions. The whole file is read into memory (signalmeow's upload takes a byte slice). Quotes
carry no quoted attachments (we don't store sent or received messages), and mentions in quotes
and received mentions (still U+FFFC in `receive`) are open.

#### 3.5 Receive: event model and output

- [x] Map signalmeow events to our `Event` types: data message, receipt (delivery/read/viewed),
      typing, reaction, edit, remote delete, sync-sent, sync-read (sync transcripts are
      `Message`/`Edit` with `Envelope.Sync`, and `Chat` is the destination, as signalmeow reports
      it; messages gained `Sticker`, `ViewOnce` and `Unsupported` parts)
- [x] Plain renderer: one line per event, `[time] <sender> → <dest>: <text>`, placeholders for
      stickers/attachments/unknown content (`output.Printer.Event`; `me` is our account, groups
      are `group:<id>`; control and bidi characters are escaped; `connection`/`queueEmpty` only go
      to the log; receipts have no time because signalmeow doesn't pass it on)
- [x] JSON renderer: NDJSON, one event per line, documented in `docs/json.md`, golden files
      (`cmd/testdata/receive_events*.golden`; connection changes and `queueEmpty` are events too)
- [x] Unknown/unsupported content is reported (type name) rather than silently dropped
      (`signal.Unsupported`: calls, group/timer/profile-key updates, end session, contact cards,
      payments, polls, pins, admin deletes, delete-for-me and message-request syncs; only
      signalmeow's store-only `ContactList`/`ACIFound` are ignored)

**Done when:** every event type above has a golden-file test for plain and JSON output. (Done; one
golden stream per format covers every event type. Not yet verified against the live server.)

Notes: signalmeow drops stories, null messages, delivery receipts from our own devices and the
sync keys/contacts blobs before they reach us. The destination number of a sync transcript isn't
passed on (signalmeow only stores it). Mentions still show as U+FFFC; plain lines don't show the
ms timestamp that `react --target`/`--quote` need (JSON has it). The receive loop itself still
lives in `cmd/` (MCP gets its own inbox in 5.4).

#### 3.6 Receive: modes

- [x] One-shot: drain queued messages, exit after `--timeout` of inactivity or `--max N` events
      (`--timeout` is now an inactivity timeout, default 5 s as in signal-cli, reset by every
      event including connection changes; receive no longer stops at the first message. `--max`
      counts content events, not `connection`/`queueEmpty`, and works with `--follow` too;
      events after the last counted one are not read, so they stay on the server)
- [x] `--follow`: stream until cancelled (landed with 3.1; `-f`, excludes `--timeout`)
- [x] Exit codes: 0 on normal end, distinct code for "unlinked" (from 2.5: `cmd.ExitUnlinked` = 3)

**Done when:** `receive` works in cron (one-shot) and as a long-running process (`--follow`).
(Done with the fake; not yet verified against the live server.)

Notes: waiting for `queueEmpty` instead of an idle timeout would end a drain sooner, but
signalmeow sends it again after every reconnect and not at all when the websocket never comes
up, so the timeout stays the only end condition.

#### 3.7 Attachments on receive

- [x] `--download-attachments <dir>`: download, decrypt, verify digest, write with a safe
      filename (`<ts>-<n>-<sanitized-name>`), no path traversal (`signal.Attachment.Remote`
      carries CDN number/key/ID, key and digest; `Client.Download` calls signalmeow's
      `DownloadAttachment`, which checks digest and MAC, and maps failures to
      `ErrAttachmentNotFound`/`ErrAttachmentInvalid`. `app.SaveAttachments` keeps only letters,
      digits, `.`, `-`, `_` of the sender's name (last path component, no leading dots, max 120
      bytes), falls back to `attachment.<ext>` by content type, and creates files with `O_EXCL`
      (0600) through an `os.Root` on the dir (0700, created up front, so a bad dir fails before
      connecting); a taken name gets `-2`, `-3`, … instead of being overwritten)
- [x] Emit the local path in plain and JSON output (plain appends `→ <path>` or
      `(download failed: …)` to the `[attachment …]` placeholder; JSON has `path` or
      `downloadError`. A failed download is logged as a warning and doesn't stop `receive`)
- [x] Without the flag, only print metadata (type, size, filename) (as since 3.5)

**Done when:** an image from the phone lands in the target dir byte-identical. (Done with the fake
and unit tests; not yet verified against the live server.)

Notes: view-once attachments are downloaded too, as signal-cli does; the output marks them. The
event is acked when it is read, before the download, and pointers aren't stored, so a failed
download can't be retried later. signalmeow's CDN request ignores the context and has no timeout:
`Download` stops waiting on cancel, but the transfer runs on in the background until the process
exits. It also panics on a CDN number past its host list, so such pointers are rejected first, as
are pointers without size (signalmeow would cut the content to zero bytes). The whole attachment
is held in memory (up to 100 MiB). The idle timeout of a one-shot `receive` now restarts after an
event is handled, so a slow download doesn't end it. A message delivered twice saves its
attachments again under numbered names. Thumbnails, blurhash, voice-note/borderless flags and
width/height aren't handled. Embedded sticker images are supported by the Later / on demand
sticker feature (2026-09-30), using the same download flag and verification path.

#### 3.8 Receipts, reactions, remote delete

- [x] `--send-read-receipts` (opt-in) on `receive` (new `Client.SendReceipt` taking the sender,
      type and timestamps; signalmeow also sends the read sync to our other devices.
      `app.ReadReceipts` queues one for every printed `Message` from another user (not sync
      transcripts, edits, reactions or deletes) and sends one receipt per sender with all
      timestamps, like
      signal-cli's merged `SendReceiptAction`: `receive` flushes the queue 1 s after the first
      queued message and when it ends, also after Ctrl-C (bounded by 10 s). A failed receipt is
      logged as a warning and doesn't end receive)
- [x] `react <recipient>... --target <author>:<ts> --emoji <e> [--remove]` (`app.React`; the
      target is parsed like `--quote` (`app.ParseTarget`, `self` for our own messages) and its author
      resolved to an ACI. `signal.SendRequest.Reaction` becomes `DataMessage.Reaction` with
      `RequiredProtocolVersion` REACTIONS, as in the mautrix bridge. The emoji check is a
      heuristic (no letters, white space or invisible characters except ZWJ/tags, not pure ASCII,
      at most 16 code points; `app.ErrInvalidEmoji`). JSON `react` document in `docs/json.md`)
- [x] `delete <recipient>... --target <ts>` (remote delete of our own message) (`app.Delete`;
      `signal.SendRequest.DeleteTarget` becomes `DataMessage.Delete`. The timestamp is the one
      `send` printed; we can't check that the message is ours or still recent, since we don't
      store sent messages. JSON `delete` document)
- [x] Group variants via `--group` (both commands reuse `send`'s path (`app.sendContent`):
      repeatable `--group <id>` and `group:<id>` arguments, `self`, one timestamp for all targets, sync
      transcripts to our other devices, per-recipient results with the `send` table and exit
      codes. `SendRequest.Check` rejects a reaction or delete mixed with other content
      (`signal.ErrInvalidContent`))

**Done when:** reactions and deletes show up on the phone for 1:1 and group targets. (Done with the
fake and unit tests; not yet verified against the live server.)

Notes: receipts are sent from the receive loop, so no event is read while one is in flight; with
the batching that is at most one request per sender and second, but a long burst still waits for
it (signalmeow's 256-request stall from 3.2 would need a very slow send). signalmeow silently skips
receipts (reporting success) to senders whose message request we haven't accepted, so those get
none. Read receipts go out for every printed message, including view-once and group messages, and
also when the user disabled read receipts on the phone (we don't sync that setting yet). Open:
viewed receipts (`ReceiptViewed` exists in `SendReceipt` but nothing sends it), reactions on
stories, admin deletes, and the MCP tools for react/delete (5.x).

#### 3.9 Initial sync after linking

- [x] Request/receive contacts, groups and the storage-service master key after `link`
      (new `Client.Sync`, needs `Connect`, `SendOnly` is enough: signalmeow's
      `SendContactSyncRequest` asks the phone for its contact list; signalmeow stores the reply
      (`SyncMessage.Contacts`) before it calls our handler with `events.ContactList`, which
      `handle` now passes to a waiter (unless `IsFromDB`, i.e. contacts changed by a storage
      sync) and still drops and acks, in send-only mode too. Provisioning already derives the
      master key from the account entropy pool the phone sends; if it is missing, Sync sends
      `SendStorageMasterKeyRequest` and polls the device table every 500 ms until the phone's
      `SyncMessage.Keys` arrives (signalmeow stores it without calling our handler). There is no
      group sync request: the protocol's GROUPS request is reserved, so groups come from the
      storage service's GroupV2 records (and from incoming group messages))
- [x] Fetch the storage service manifest and persist contacts/groups/blocked list (signalmeow's
      `SyncStorage` stores contact records (profile, system and nick names, number, profile key,
      blocked, whitelisted), group master keys and the account record. It returns no error, so
      Sync first calls `FetchStorage` only to learn whether the fetch works and then lets
      `SyncStorage` fetch and store it again (the manifest and records are downloaded twice).
      Since `SyncStorage` also swallows the failure of its own fetch or database update, Sync
      then checks that the store holds what the first fetch has (a recipient row for each
      contact record with an ACI, found without creating rows, and the master key of each GroupV2
      record with a valid key; what signalmeow skips or stores by PNI isn't checked); anything
      missing fails the storage stage with `signal.ErrStorageNotStored` ("N contacts, M groups
      missing"), so the sync is reported incomplete. A 204 (no manifest) skips `SyncStorage`,
      which would dereference its nil update, and counts as synced.
      `SyncResult` has the numbers of contacts (users with a name or number, without us) and
      groups in the store afterwards, from `LoadAllContacts` and the new
      `store.(*Store).GroupIdentifiers` (signalmeow's `GroupStore` can't list; 4.2 reuses it), and
      whether the master key is known, the storage service synced and the contact list arrived.
      A complete sync records its time as `last_sync` in `gosignal_meta`. New
      `account sync [--timeout 60s]` runs it for an existing account (`app.Sync`: connects with
      `SendOnly`; plain and JSON `sync` document in `docs/json.md`))
- [x] `link` waits for the initial sync (with progress on stderr and a timeout) (on the same
      client right after `Link`, which keeps the database and lock; `SyncOptions.Progress` reports
      the stages, printed as `Sync: <stage>...` lines on stderr, then `Synced N contacts and M
groups.` on stdout. `--sync-timeout` defaults to 60 s, 0 skips the sync. The timeout is the
      context deadline: `Sync` then returns what it has with an error wrapping
      `signal.ErrSyncIncomplete` and the cause, which `app.Sync` turns into
      `SyncResult.Incomplete`; `link` and `account sync` print a warning on stderr and exit 0.
      Any other sync error is only a warning for `link` (the device is linked) but fails
      `account sync`, with exit 3 for an unlinked device)

**Done when:** right after linking, contacts and groups from the phone are in the store. (Done with
the fake and unit tests for the contact list hook, the counts and the group query; not yet
verified against the live server.)

Notes: other incoming envelopes that arrive during the sync are left on the server as with every
send-only command, so the next `receive` gets them; the contact list itself is acked because
nothing is left to deliver (a redelivered one would only be stored again). The storage service is
always fetched in full (signalmeow tracks no manifest version), and nothing re-syncs it later
except signalmeow itself on a `FetchLatest` or `Keys` sync message while connected; `account
sync` is the manual way. signalmeow only skips a second contact request within a minute of the
first one on the same client. The counts include users that only messaged us, and the contact
list and storage records don't say which groups we left. Contact avatars, the storage service's
blocked groups and story distribution lists aren't handled. `last_sync` isn't shown anywhere yet.
`SyncResult.ContactList` is also true when signalmeow failed to download or parse the contacts
blob: it logs the error and still reports an (empty) `ContactList`, which we can't tell apart from
a phone without contacts. signalmeow's `SyncStorage` reads `Store.MasterKey` without
synchronising with the receive loop, which may update it from a `Keys` sync message meanwhile;
that is benign (either key is the account's current one).

### Phase 4 — Contacts, groups, identities

Since 5.1, the command logic goes into `internal/app` (typed request → typed result, tested
against the fake); `cmd/` only parses flags, calls `app` and renders via `internal/output`.

#### 4.1 Contacts

- [x] `contacts list` (name, number, ACI, username, blocked), filters `--blocked`, `--query`
      (new `Client.Contacts`, store only: signalmeow's `LoadAllContacts` (users with a contact
      name, profile name or number) plus the blocked users (new `store.(*Store).BlockedACIs`),
      without us. `app.ContactsList` filters (`--query`/`-q`: case-insensitive substring of any
      name, the number, ACI or PNI) and sorts by display name. Plain table NAME/NUMBER/ACI/BLOCKED,
      JSON `contacts` document with `output.ContactJSON` objects. No USERNAME column: signalmeow
      stores no usernames)
- [x] `contacts show <recipient>` (new `Client.Contact`, store only, by ACI, else PNI, else number,
      through signalmeow's concrete `LoadRecipientBy{ACI,PNI}`, which, unlike
      `LoadAndUpdateRecipient`, don't create rows; `signal.ErrUnknownContact` otherwise.
      `app.ContactsShow` resolves `@username` first (no connection needed), allows `self` and
      rejects groups (`app.ErrNotAUser`). Key/value view with names, number, ACI, PNI, blocked and
      message request state; JSON `contact` document)
- [x] `contacts block|unblock <recipient>` (updates storage service so the phone sees it)
      (not through a storage service write: new `Client.SetBlocked` reads the current blocked list
      (users and groups) from the storage service, applies the change and sends the complete list
      as a `SyncMessage.Blocked` to our own ACI, both the current fields (with the storage
      service's block times) and the deprecated ones; the phone applies it and writes the storage
      service itself. Then the store is updated. The storage service key is required
      (`signal.ErrStorageKeyUnknown`), since a list without the blocked groups would unblock them
      on the phone. For the same reason nothing is sent (`signal.ErrBlockedListIncomplete`) when
      the fetch isn't the complete list: no manifest yet (signalmeow's `nil` update on a 204),
      `MissingRecords` (records that couldn't be fetched, decrypted or parsed), or a blocked
      contact with neither ACI nor number or a blocked group without a valid master key, which a
      `SyncMessage.Blocked` can't carry. `app.ContactsBlock`/`ContactsUnblock` connect `SendOnly`, resolve like `send`,
      reject groups and self, and report per user whether it changed; plain table
      NAME/NUMBER/ACI/STATUS, JSON `block` document. Exit 3 for an unlinked device)
- [x] Name resolution (contact name → profile name → number → ACI) used by all plain renderers
      (nickname first, as the Signal apps do: nickname → contact name → profile name → number →
      ACI (`signal.Contact.Name`/`DisplayName`). `app.Names` is built from `Client.Contacts`; a
      display name shared by several contacts gets the number (or the first 8 characters of the
      ACI) added. `output.(*Printer).SetNames` makes plain output show names (and `me` for our own
      ACI, e.g. as a quote author) in `receive`, `send`, `react` and `delete`; JSON recipient
      objects and send results gain an optional `name` (no schema bump). `receive` loads the names
      once and reloads them (`app.NameBook`) when an event names a user without a name, at most
      every 30 s, since profiles and contacts arrive while receiving)

**Done when:** `receive` plain output shows names instead of UUIDs. (Done: golden
`receive_events_names` with the fake's contacts, plus cgo tests for the store reads, the override
handling and the blocked-list message. Not yet verified against the live server.)

Notes: signalmeow's storage sync always overwrites the blocked flag with the storage service's,
and it runs on `account sync`, on an incoming `Keys` or `FetchLatest` sync message and whenever
signalmeow feels like it; until the phone has written our change to the storage service, that
would undo it. So `SetBlocked` records an override (`gosignal_block_overrides`, migration
`03-contacts.sql`) wherever the storage service still disagrees, tied to the manifest version of
the fetch it built the list from (`storage_version`, migration `05-block-override-version.sql`).
Once any later manifest version is seen, the phone has written the storage service since (with our
change, or with a newer one made there), so the override is dropped and the store takes the state
of that fetch; the phone always wins from then on. That needs the fetch to know the user's state
(it read their record, or every record): if a later version was only partly readable and the
user's record is among the missing ones, the override stays, but isn't re-applied (the phone may
have changed it), until a fetch that knows or expiry. Earlier the override ended only when a sync
reported the contact as changed and agreeing, but signalmeow reports only contacts whose local row
changed, so an agreeing storage service went unnoticed, the override lived for 7 days, and an
unblock made on the phone in that time was undone locally and re-sent as a block by the next
`contacts block`. Versions are seen by `SetBlocked`'s own fetch (older overrides neither go into
the list nor survive), by `Client.Sync`'s fetch, and, since signalmeow's background storage sync
exposes no version, by a fetch of our own when that sync (`ContactList` with `IsFromDB`) changed
an overridden user: `FetchStorage` with the overrides' version gets a 204 (no records fetched)
while the phone hasn't written; then the override is re-applied. If that fetch fails, the overrides
are re-applied as before (none has been seen superseded). Overrides are also re-applied on
`Connect`; `Contacts`/`Contact` show them even before that. After `signal.BlockOverrideTTL` (7
days) the storage service wins anyway, e.g. if the phone never applied our list: the store takes
the state of the fetch at hand if it knows it; otherwise the override just ends and signalmeow's
next storage sync, which fetches every record and sets the blocked flag from each contact record
whether it changed or not, rewrites it (a user without any contact record there keeps the
overridden flag); overrides from before migration 5 (version 0) end at the next version seen.
Between a background sync and the re-apply, signalmeow may briefly see the old state (a message from a freshly blocked user could
get through then). The phone writing the storage service for another reason before it processed
our list also ends the override early (the store then shows the old state until the phone's next
write). Blocking needs an ACI (users not on Signal can't be blocked) and doesn't cover
groups. The phone's handling of our blocked list (the official apps replace their whole list with
it) is untested. Users that only have a nickname in the store are missing from `contacts list`
(signalmeow's `LoadAllContacts` skips them); `contacts show` finds them. With names loaded, our own
ACI as a quote author now shows as `me` (one line of the `receive_events` golden changed).

#### 4.2 Groups

- [x] `groups list` (id, title, member count, our role) (new `Client.Groups`: every group whose
      master key the store holds (`store.GroupIdentifiers` from 3.9) is fetched with signalmeow's
      `RetrieveGroupByID`, which needs group auth credentials over the authed websocket, so
      `Connect` (`SendOnly` is enough). signalmeow reports the server's status only in the error
      text: a 403 becomes `signal.ErrNotAMember` (left, removed, or a join request not approved)
      and a 404 or missing master key `signal.ErrUnknownGroup`; such groups are still listed with
      `Group.Err`, their last known title and `leftAt`, while any other error fails the list.
      Sorted by title. Plain table ID/TITLE/MEMBERS/ROLE with the role `admin`, `member`,
      `invited`, `requesting`, `left` or `not a member`; JSON `groups` document in `docs/json.md`)
- [x] `groups show <id>`: title, description, members with roles, pending members, timer
      (`Client.Group`; `signal.Group` carries ID, title, description, revision, disappearing
      timer, announcements-only, members with role and joined-at revision, pending members with
      role, inviter and time, requesting members with time, and our membership/role
      (`Group.MembershipOf`). The master key stays in `Group.MasterKey` but is never printed.
      Plain key/value lines plus Members/Invited/Requesting to join sections; JSON `group`
      document; `output.GroupJSON`/`NewGroupJSON` are exported for MCP)
- [x] `groups leave <id>` (`Client.LeaveGroup` builds a signalmeow `GroupChange` and calls
      `UpdateGroup`, which patches the group and sends the change to the members (a conflict
      with a change made meanwhile, 409, is not retried: signalmeow takes the reply for a
      `ContactManifestMismatchError`, which we map to `signal.ErrGroupChanged`, "try again"): a member deletes itself (`DeleteMembers`, as the mautrix bridge does), an invited
      user its invitation (`DeletePendingMembers` with its ACI), a requesting user its request
      (`DeleteRequestingMembers`). `Group.CheckLeave` refuses, like signal-cli's quitGroup, when we
      are the only admin while other members remain (`signal.ErrLastAdmin`; the CLI error names
      `--promote`); repeatable `--promote <member>` makes members admins in the same change
      (`ModifyMemberRoles`, only for admins and only for other members:
      `signal.ErrInvalidPromotion`). Needs `--yes` like `account unlink`. JSON `left` document)
- [x] Group id parsing: accept base64 master key / group id, and title if unique
      (`app.ResolveGroup`: `group:<id>` or a bare 32-byte value in standard or URL-safe base64 is
      passed on, and the facade looks it up as an ID in signalmeow's group store first, then
      derives the ID from it as a master key (`libsignalgo.GroupMasterKey.GroupIdentifier`); only
      groups whose key is stored are known. Anything else is a title, matched case-insensitively
      against the title cache (whole title, surrounding white space ignored) without connecting:
      several matches fail with `app.ErrAmbiguousGroup` listing the `group:<id>`s, none with
      `signal.ErrUnknownGroup`. Groups we left only count when no current group has the title)
- [x] Title cache for offline lookup (not in the original plan; our own `gosignal_groups` table,
      migration `04-groups.sql`: title, revision, `left_at`, `updated_at` per group ID, written
      after every successful fetch (which clears `left_at`; an empty title keeps the cached one)
      and by `LeaveGroup` (with the revision after leaving). New `Client.GroupTitles` returns
      title and `leftAt` per group without `Connect`; `app.Names` includes the titles (best
      effort: names load without them if the cache can't be read), so
      `receive` shows `group "<title>"` in plain lines and `groupTitle` in the JSON chat, and
      `send`/`react`/`delete` label groups the same way)

**Done when:** list/show/leave work against groups created on the phone. (Done with the fake and
unit tests for the conversion, master key derivation, the leave change and the title cache; not
yet verified against the live server.)

Notes: `groups list` fetches every known group (one request per group, sequentially).
The current fork retains ACI and PNI pending members, but ordinary list/show/join/leave
self-membership recognition remains ACI-only. `groups accept` matches own ACI first,
then own PNI, through a strict uncached full-state read without endorsement processing.
A requesting user may not fetch full group state (403); generic `UpdateGroup` fetches
it first. `groups cancel-request` instead uses a password-free authenticated preview and
one signed own-ACI request deletion, without full-state reads or member notifications.
Ordinary reads of invited groups retain their existing missing-endorsement behavior:
the log bridge demotes those cache errors to debug, while cgo may print a caught empty-
endorsement panic to stderr. The dedicated acceptance reader avoids that path. After leaving, signalmeow's endorsement update for
the new revision probably fails (only logged), its `signalmeow_groups` row stays (the group keeps
being listed, as `left`), and a failure to tell the members is only logged by signalmeow. Groups
can't be told apart as "left on another device" versus "removed" (both a 403). Creation,
renaming, member addition/removal, settings updates (`groups update`), standalone
administrator-role changes (`groups promote|demote`), banned-member management
(`groups ban|unban`), invite-link management (`groups link show|update`), avatar updates
(`groups update --avatar|--remove-avatar`) and invite-link joining (`groups join <link>`)
are implemented under "Later / on demand", with live acceptance tracked separately
there. Join-request cancellation is also implemented there, with live verification
tracked separately. PNI invitation decline remains open.

#### 4.3 Identities and safety numbers

- [x] `identities list [<recipient>]`: identity key fingerprint, trust level, first seen
      (`Client.Identities` → `app.IdentitiesList`; the fingerprint is the hex of the 33-byte
      public key, as signal-cli shows it; trust levels `untrusted`, `trusted-unverified`,
      `trusted-verified`; plain table RECIPIENT/FINGERPRINT/TRUST/FIRST SEEN/CHANGED, JSON
      `identities` document. Keys signalmeow stored before go-signal tracked trust count as
      trusted on first use, with an unknown first-seen date; our own ACI/PNI keys and PNI
      identities are left out)
- [x] `identities show <recipient>`: safety number (numeric + QR) (`Client.SafetyNumber`:
      libsignal's numeric fingerprint, version 2 over both ACIs with 5200 iterations, as the apps
      compute it; plain shows 12 blocks of 5 digits and the scannable encoding as a QR code via
      qrterminal, JSON has `safetyNumber` and base64 `scannable`)
- [x] `identities trust <recipient> [--safety-number <n>]` (`Client.TrustIdentity`: without a
      number the current key becomes `trusted-unverified` (a verified key stays verified); with
      one, white space ignored, it becomes `trusted-verified` if it matches, otherwise
      `signal.ErrSafetyNumberMismatch` and nothing changes)
- [x] Policy: TOFU; on identity change, warn on stderr and emit an `identity-changed` event;
      sending to an untrusted changed identity requires explicit trust (a wrapper around
      signalmeow's ACI/PNI identity stores (`meow_identity.go`, installed on the device in
      `Connect`) keeps our state in `gosignal_identities` (migration v2) through the context of
      the callbacks, so inside signalmeow's decryption transaction. A different key, seen when
      decrypting (`SaveIdentityKey`) or when setting up a session to send (`IsTrustedIdentity`),
      becomes `untrusted`, is logged as a warning and marked for an `identityChanged` event (JSON
      type in camelCase like the others), which `receive` gets right before the event of the
      envelope that carried the key, or with the next event if it was seen while sending. libsignal
      then refuses to encrypt for it; `Send` maps that per recipient to
      `signal.ErrUntrustedIdentity` with the `identities trust` hint. Receiving is never refused)

**Done when:** an identity change is detected, reported, and blocked for sending until trusted.
(Done with the fake, cgo unit tests of the wrapper against a real account database, and
libsignal's session setup refusing a changed prekey bundle until it is trusted; not yet verified
against the live server.)

Notes: only the current key can be trusted. The first version also accepted the last trusted key
before a change, for the sessions of the user's "other devices"; but all devices of an account
share one identity key, and after the user trusted or verified the new key, a prekey bundle signed
with the old one (lost phone, malicious server) was accepted silently. Since the Phase 4 review
every key other than the current one is a change, the previous key included (a delayed message
with it flips the key back and must be trusted again; the event's `oldFingerprint` then equals
`newFingerprint` if the change in between wasn't trusted). A change, and `identities trust`,
remove the user's sessions whose identity key (read from the serialized session record, which
libsignalgo has no accessor for) isn't the current key; the next send fetches new prekey bundles.
A message that still arrives on a removed session can't be decrypted (a `decryptionFailure`
event); where its content hint allows, signalmeow sends a retry receipt and the sender resends it
on a new session. Before, such messages were decrypted; only messages sent before the key
change and still queued are affected (the sender's old sessions ended with its old key). A change is reported until a `receive`
has handed its event out (`pending_event`), so it isn't lost when receive stops before reading
it; a change in a rolled-back decryption is neither stored nor reported. Gaps: libsignal's
multi-recipient (sender key) encryption doesn't ask whether a key is trusted, so group members
who already have our sender key still get group messages after their key changed (a member
whose devices changed gets a new sender key distribution message, which is blocked); PNI
identities can't be listed or trusted, so the wrapper leaves them to signalmeow, which trusts
every key (trust on first use without change detection); signalmeow bypasses the wrapper
for the PNI identity key of sync messages, PNI signatures and provisioning; the storage
service's `ContactRecord` identity state/verified flag isn't read or written, and
`SyncMessage.Verified` is neither sent nor handled, so verification doesn't sync with the phone;
the identities commands don't connect, so a number must already be cached (the ACI always
works).

### Phase 5 — MCP server

Expose the account to MCP clients (Claude Code, Claude Desktop, other agents) through
`go-signal mcp serve`. It uses the same binary, facade and store as the CLI. The server is a
long-running process that holds the account lock and runs its own receive loop, so it replaces
the CLI for that account while it is running. SDK: the official
`github.com/modelcontextprotocol/go-sdk` (evaluate `mark3labs/mcp-go` only if the official SDK
lacks something we need).

#### 5.1 Shared use-case layer (`internal/app`)

- [x] Move the logic behind `send`, `react`, `delete`, `contacts`, `groups`, `identities` and
      `account show` out of `cmd/` into `internal/app` functions that take typed requests and
      return typed results (no printing, no Cobra) (done for the commands that exist so far:
      `app.App` wraps an open `signal.Client` with `AccountShow`, `AccountUnlink` and
      `DevicesList`; the Phase 3/4 commands land there directly)
- [x] `cmd/` becomes flag parsing + `internal/app` call + `internal/output` rendering (`account`
      and `devices`; `link` and `receive` stay in `cmd/` since MCP doesn't expose them as tools)
- [x] Recipient resolution, name resolution and trust checks live only in `internal/app` (lands
      with 3.2, 4.1 and 4.3; recipient resolution is in since 3.2: `app.ResolveRecipients`, name
      resolution since 4.1: `app.Names`/`app.NameBook`, which `output` only renders. The trust
      policy of 4.3 is enforced in the facade, because libsignal asks the identity store while
      encrypting; `internal/app` has the `identities` use cases)

**Done when:** the CLI behaves as before (golden files unchanged), and `internal/app` has unit
tests against the fake facade.

#### 5.2 Server skeleton and transport

- [x] `cmd/mcp.go`: `mcp serve` command; stdio transport; server name/version from `version`
      (official `github.com/modelcontextprotocol/go-sdk` v1.8.0; `internal/mcp.Serve` runs the
      SDK's newline-delimited JSON-RPC over the command's stdin/stdout, server `go-signal` with
      `cmd.Version`, plus short instructions)
- [x] stdout carries only the MCP protocol; all logging goes through slog to stderr (enforce with
      a test that runs the server and checks stdout contains only JSON-RPC frames)
      (`TestMCPServeStdout` re-runs the test binary as a child with `-v` and checks every line of
      its real stdout; the SDK's own logs are demoted to debug, and the `logging` capability is
      not advertised)
- [x] Account selection (`-a`), lock acquisition and connect happen before the server
      advertises tools; a locked or unlinked account fails at startup with a clear error
      (`ErrAccountInUse`, `ErrNotLinked`, and `ErrDeviceUnlinked` with exit code 3, before
      anything is read from stdin or written to stdout)
- [x] Graceful shutdown on stdin EOF and SIGINT/SIGTERM (reuses 3.1) (both end with exit 0;
      the client is closed through `Client.Close`)
- [x] Tests via the SDK's in-memory transport against the fake facade (`CGO_ENABLED=0`)
      (`internal/mcp`; `cmd` tests drive `mcp serve` over pipes)

**Done when:** `claude mcp add signal -- go-signal mcp serve` connects and lists the server's
tools. (Done with the fake: initialize and `tools/list` over stdio; not yet verified with Claude
Code against a linked account.)

Notes: until the inbox (5.4) consumed events, the server connected with `signal.SendOnly()`, so
incoming messages stayed on the server for the next `receive`, and a remote unlink while it ran
only surfaced on the next send; since 5.4 it receives. `account_show` (from 5.3) landed here so that there is a tool to
list; its structured output is the `docs/json.md` account object (`output.AccountJSON`).

#### 5.3 Read-only tools

- [x] `account_show`, `contacts_list` (with `query`), `contacts_show`, `groups_list`,
      `groups_show`, `identities_list` (`account_show` landed with 5.2; `contacts_list` also
      takes `blocked`, `identities_list` an optional `recipient`)
- [x] Input/output JSON schemas derived from Go structs; outputs reuse the `docs/json.md` types
      as structured content, with a short text summary for clients that ignore structured output
      (the SDK infers both schemas and validates the output against its schema; lists are wrapped
      in an object like `{"contacts": [...]}`, without the CLI's `version`. The text content is
      the CLI's plain output (times in local time, like the CLI). Group members carry
      their `name`, so `output.NewGroupJSON` takes `app.Names`)
- [x] Tool annotations: `readOnlyHint: true` (plus `openWorldHint: false`)

**Done when:** an agent can answer "who is in group X?" and "what is Alice's number?" through the
tools. (Done with the fake: `TestGroups` and `TestContactsList` in `internal/mcp`.)

Notes: the server's client is connected once at startup, while the group (and later the send)
use cases connect on their own for the CLI. A second `Connect` now fails with
`signal.ErrAlreadyConnected` (the fake mimics it), and `internal/app` uses a client that is
connected already as it is. `identities_show` is left out: its QR payload is binary and the
safety number is only useful next to the phone; add it if an agent needs it.

#### 5.4 Inbox: receiving through MCP

MCP is request/response, so incoming messages are buffered by the server and pulled by the client.

- [x] Background receive loop writes events into our own `inbox` table (bounded retention:
      `--inbox-max-age`, `--inbox-max-count`) (`app.Inbox.Run` next to the server in
      `mcp.Serve`; table `gosignal_inbox` behind the facade's `Inbox*` methods, defaults 30 days
      and 10000 entries, pruned after every stored event)
- [x] `messages_list`: filters `chat` (recipient or group), `since` (timestamp/cursor), `limit`;
      returns a cursor for the next call (`chat` takes a user as `send` does or a group ID or
      title; `cursor` and `since` (RFC 3339) are separate; without either it returns the newest
      entries, else the oldest after them; `limit` 50 by default, at most 200; `more` says that
      more follow)
- [x] `messages_wait`: long-poll for new events with a timeout (capped, e.g. 60 s) (default
      30 s, at most 60 s; without a cursor it waits for entries after the newest; on timeout it
      returns no entries and the cursor to wait on)
- [x] Resources: `signal://chats` and `signal://chat/{id}` (recent messages); support
      `resources/subscribe` and send `notifications/resources/updated` on new messages (the chat
      ID is `signal.Chat.Key()`, percent-encoded: `group:<id>` or the ACI; both are JSON; a new
      entry updates `signal://chats` and its chat)
- [x] Attachments: metadata only by default; `attachment_get` downloads on demand into a
      configured dir (reuses 3.7) and returns the path, or small images as image content
      (`--download-dir`, by default `attachments` in the account's directory; the file is always
      saved, and JPEG/PNG/GIF/WebP images up to 1 MiB are returned as image content as well)
- [x] `mark_read` tool; read receipts are only sent through it, never automatically (all unread
      messages, or those of one `chat` up to a `cursor`; one receipt per sender)

**Done when:** an agent can wait for a message, read it, and fetch its attachment, with no other
`receive` process running. (Done with the fake: `TestInboxFlow` in `internal/mcp` and
`TestMCPServeInbox` over stdio; the table and the real client's inbox methods have cgo tests; not
yet verified against the live server.)

Notes: `mcp serve` now connects for receiving (no longer `SendOnly`), so it acks what it
receives; an event counts as received once the inbox loop read it, and a store error ends the
server (the events not read stay on Signal's server). Stored: messages, edits, deletes,
reactions, unsupported content, decryption failures and identity changes (the last two in the
1:1 chat with the user). Not stored: typing, receipts (5.5 may add a delivery status for our own
sends), connection changes; a read sync from another device marks the messages read in the
inbox. Only incoming messages count as unread. Events are stored as JSON with Go field names (see
`signal.marshalEvent`); renaming a field of an event type loses it in older entries, and an entry
that can't be decoded any more shows as `unsupported` `unreadableInboxEntry`. The entry objects
(`output.InboxEntryJSON`: `id`, `receivedAt`, `unread`, `event` as in docs/json.md) and the
resources' JSON are MCP-only and get documented in `docs/mcp.md` (5.6). A connection lost for
good ends the server with its error (exit code 3 when unlinked). The tools that return message
content say in their description that it is untrusted data (part of 5.5's last item).
`attachment_get` and `mark_read` are the first tools that aren't read-only; 5.5 decides whether
`--read-only` drops them. With the SDK's newer protocol (2026-07-28) a subscription is a
background `subscriptions/listen`, so its errors don't reach the client.

#### 5.5 Write tools and safety policy

- [x] `send_message` (recipients or group, text, attachments from local paths, quote),
      `react`, `delete_message` (recipients and `chat` take users or group IDs/titles as
      `messages_list` does; `quote` and `react`'s `message` take an inbox id or
      `<author>:<timestamp>`; the output is the `docs/json.md` `send`/`react`/`delete` object;
      partial failures are tool errors that keep the structured result)
- [x] `--read-only`: write tools are not registered at all (`mark_read` too, see §1)
- [x] `--allow-recipient <r>` (repeatable, also via config): write tools reject other recipients
      with a sentinel error; default when unset is decided here (all vs. none) and recorded in §1
      (none; `'*'` allows all. `app.WithAllowlist` enforces it in `sendContent`, after resolving
      and before anything is uploaded; entries are resolved once, users match by ACI. Config key
      `mcp.allow-recipient`, env `GOSIGNAL_MCP_ALLOW_RECIPIENT` separated by commas; the other
      three flags are bound the same way)
- [x] Attachment paths restricted to `--attach-dir` (no arbitrary file exfiltration) (paths are
      relative to it and opened through `os.Root`, so `..` and symlinks can't leave it
      (`app.ErrOutsideAttachDir`); without `--attach-dir` attachments are rejected)
- [x] Tool annotations: `destructiveHint` for `delete_message`, `openWorldHint` for sends
- [x] Optional `--confirm` mode using MCP elicitation, where the client supports it (the tool
      returns an input request (SEP-2322); the SDK falls back to `elicitation/create` for older
      protocol versions. The answer is one-shot and bound to the tool and its arguments; a
      client without elicitation gets an error, so nothing is sent unconfirmed)
- [x] Incoming message text is returned as data with sender metadata; tool descriptions state
      that message content is untrusted (prompt-injection note in `docs/mcp.md`) (descriptions
      and instructions done in 5.4/5.5; the `docs/mcp.md` note landed with 5.6)

**Done when:** sends work end to end, and the allowlist, read-only mode and attach-dir
restriction each have tests that prove the rejection. (Done with the fake: `internal/app`
`TestAllowlistRejects`/`TestSendAttachDir`, `internal/mcp` `TestWriteToolsRejected`,
`TestListTools`, `TestConfirm*`, and `cmd` `TestMCPServeAllowlist*`/`TestMCPServeReadOnly`
over stdio; not yet verified against the live server or Claude Code's elicitation UI.)

Notes: the allowlist covers the chats a message goes to, not mentioned users or quote authors,
and not `mark_read`'s receipts. With `--confirm`, the allowlist is checked before the user is
asked.

#### 5.6 Docs and optional HTTP transport

- [x] `docs/mcp.md`: tool/resource reference, config snippets for Claude Code and Claude Desktop,
      safety flags, the "one process per account" rule, the prompt-injection note (from 5.5)
      (also the inbox entry and resource JSON that 5.4 left for it, and troubleshooting)
- [x] Optional: streamable HTTP transport (`--listen 127.0.0.1:<port>`, bearer token) for
      clients that can't spawn a process. This overlaps with the daemon item in "Later".
      (`mcp.ServeHTTP` on the SDK's `StreamableHTTPHandler` at `/mcp`; the token comes from
      `--token-file` or `GOSIGNAL_MCP_TOKEN`/`mcp.token`, with no flag so that it stays out of
      the process list, and needs at least 16 characters; only loopback addresses, since it is
      plain HTTP; the SDK's DNS-rebinding check plus `http.CrossOriginProtection`; runs until
      SIGINT/SIGTERM; idle sessions close after an hour)

**Done when:** a new user can wire go-signal into Claude Code by following `docs/mcp.md`.
(Written against the fake-tested behaviour; the Claude Code and Claude Desktop snippets and the
HTTP transport with `claude mcp add --transport http` are not yet verified against a linked
account.)

Notes: the HTTP transport is still one process holding the account, not the daemon of "Later":
all sessions share one `mcp.Server`, so they share the inbox, the allowlist and pending
confirmations. `TestServeHTTP*` (`internal/mcp`) and `TestMCPServeHTTP`/`TestMCPServeListenErrors`
(`cmd`) cover it.

#### 5.7 Health checks

- [x] `go-signal mcp doctor`: takes the flags of `mcp serve` (shared via `addMCPFlags`, bound to
      the config keys when the command runs, since viper keeps one flag per key) and checks the
      settings, `--listen`, the account, the account lock (`Client.CheckLock`, a non-blocking
      flock probe; held is only a warning), the device on Signal's server (`Devices`; skip with
      `--offline`), the inbox and the download dir; exit 0/1/3 (`app.DoctorError`)
- [x] MCP tool `doctor` (also in `--read-only`): the same account checks (`app.Doctor`, the server
      only with `checkServer`), plus version, uptime, and the connection state that `app.Inbox`
      now records from `*Connection` events (`Inbox.Connection`)
- [x] `docs/mcp.md` (tool reference, troubleshooting starts with `mcp doctor`), `docs/json.md`
      (`mcp doctor`)

**Done when:** a user whose server doesn't start, or whose agent sees no messages, gets told why by
`mcp doctor` or the `doctor` tool. (Done with the fake: `TestDoctor*` in `internal/app` and
`internal/mcp`, `TestMCPDoctor*` in `cmd`, `TestProbe` in `internal/store`; not yet tried against a
linked account.)

#### 5.8 Hooks: reacting to messages

With `mcp serve --listen` running as a user service, the server can react to messages instead of
only storing them, e.g. by having an LLM answer through the same server.

- [x] `mcp serve --on-message <program>`: the server runs the program (absolute path, no shell)
      for every incoming message (`*signal.Message`, no sync transcript, so the program's own
      replies never trigger it) of a chat that `--hook-from` allows. The program gets the inbox
      entry as JSON on stdin (`output.NewInboxEntryJSON`) plus `GOSIGNAL_ENTRY_ID`, `GOSIGNAL_CHAT`
      and `GOSIGNAL_SENDER`. It hangs off `app.InboxOptions.Added` (`internal/mcp/hook.go`).
- [x] `--hook-from` is required with `--on-message` and uses the allowlist syntax; it matches the
      chat (`app.ChatAllowed`, which shares the resolution with the send allowlist). A startup
      warning lists `--hook-from` chats that `--allow-recipient` lacks (`Allowlist.Missing`).
- [x] Runs happen one at a time from a queue of 64 (a full queue drops, with a warning). A run is
      killed with its process group after `--on-message-timeout` (default 5m) or at shutdown. Its
      output goes to the log, and nothing is retried.
- [x] Example `contrib/hooks/claude-reply.sh` (`claude -p`, no built-in tools, only
      `messages_list`/`send_message`/`mark_read`); `docs/mcp.md` "Hooks".
- [x] Queue overflow regression coverage (`TestHookQueueOverflow`): hold one run, fill all 64
      waiting slots, then verify overflow warns without blocking receive, all messages stay
      unread in the inbox, accepted runs execute once in order, and a fresh message runs after
      draining without retrying the overflow entries.

**Done when:** a message from a `--hook-from` chat runs the program once, with the entry on stdin,
and nothing else runs it. (`TestHook*` in `internal/mcp`, `TestMCPServeHook*` in `cmd`,
`TestChatAllowed` in `internal/app`.)

### Phase 6 — Packaging and release

Decision: releases ship only fully static musl Linux binaries (plus macOS arm64), so 6.1 and 6.2
are one pipeline and there is no glibc release build. Details and the one-time repo setup are in
`docs/dev.md` ("Releases").

#### 6.1 Release pipeline

- [x] release-please config and workflow (conventional commits → changelog + tag)
      (`release-please-config.json`, manifest at 0.0.0 so the first release is 0.1.0;
      `release-please.yaml` calls `release.yaml` itself, because a tag pushed with `GITHUB_TOKEN`
      triggers no workflow; optional `RELEASE_PLEASE_TOKEN` so CI runs on the release PR)
- [x] Tag-triggered release workflow: per-OS/arch runners (linux amd64/arm64 first), reusing the
      cached `libsignal_ffi.a` (`release.yaml`: `v*` tags, `workflow_call`, `workflow_dispatch`;
      native `ubuntu-24.04` / `ubuntu-24.04-arm` runners, the musl `.a` cached per arch and
      libsignal commit; the release is created if missing, assets uploaded with `--clobber`)
- [x] Checksums file and (optional) cosign/SLSA provenance (`SHA256SUMS`;
      `actions/attest-build-provenance` on the archives and the image instead of cosign)
- [x] `version` prints go-signal, signalmeow and libsignal versions (signalmeow from the build
      info's `go.mau.fi/mautrix-signal` dep)

**Done when:** pushing a tag produces a GitHub release with linux amd64/arm64 binaries.
(actionlint-clean; the first real tag run on GitHub is still to come.)

#### 6.2 Static build

- [x] musl build of `libsignal_ffi.a` (`x86_64-unknown-linux-musl`, `aarch64-unknown-linux-musl`)
      (native build in `golang:<go>-alpine`, `scripts/build-static.sh`, into
      `third_party/lib-musl/<arch>/` with a SHA stamp; `RUSTFLAGS=-C target-feature=-crt-static`
      for proc-macros on a musl host)
- [x] Static Go link (`-linkmode external -extldflags -static`, musl-gcc or `zig cc`) (Alpine's
      gcc; tags `netgo,osusergo,timetzdata,sqlite_omit_load_extension`; `just build-static`)
- [x] Smoke test: the binary runs in a `scratch`/`alpine` container (`ldd` → "not a dynamic
      executable") (`just smoke-static`: ldd, then `version` and `account show` in the scratch
      image from the root `Dockerfile`)

**Done when:** the release ships a fully static linux binary.
(Verified locally on amd64; arm64 is first built by the release workflow.)

#### 6.3 Docs and distribution

- [x] Man pages and shell completions via `cobra/doc`, included in release archives
      (`scripts/gendocs`, `just docs-gen`; `<placeholders>` escaped for md2man, the data-dir
      default shown as `$XDG_DATA_HOME/go-signal`, date from `SOURCE_DATE_EPOCH`)
- [x] README: install, link, send/receive quickstart, keep-alive note (30-day unlink)
- [x] Example systemd user unit/timer for periodic `receive` (`contrib/systemd/`)
- [x] Optional: macOS build (native runner), Homebrew tap, AUR / container image
      (macOS arm64 on `macos-15`, `continue-on-error` until it is proven;
      `ghcr.io/cwbudde/go-signal` from scratch for amd64/arm64; Homebrew formula and
      `go-signal-bin` PKGBUILD rendered from `packaging/` by `scripts/render-packaging.sh`, jobs
      skipped until `HOMEBREW_TAP_TOKEN` / `AUR_SSH_KEY` are set)

**Done when:** a new user can install from a release and link + send by following the README.
(Pending the first release. Not yet verified: the macOS link, where libsignalgo passes
`-lstdc++`; the tap and AUR pushes, until the `cwbudde/homebrew-tap` repo and the secrets exist.)

### Phase 7 — Pure-Go backend: forks and protocol core

Phases 7–10 are a later stage: a pure-Go libsignal backend.

Goal: build with `CGO_ENABLED=0`, so there's no Rust toolchain, no `libsignal_ffi.a` and no musl
dance, and cross-compiling works like any other Go program. This starts after Phase 6. Until
Phase 10 flips the default, the CGO backend stays the default and the reference.

Starting point: [`GoCodeAlone/libsignal-go`](https://github.com/GoCodeAlone/libsignal-go) (AGPL-3.0,
pure Go, wire-compatible with libsignal v0.96.4). It covers curve/XEdDSA, Kyber1024, PQXDH, the
Double Ratchet, SPQR, sender keys, sealed sender v1/v2, AES-256-GCM-SIV, fingerprints, account keys
and usernames. What signalmeow needs from `libsignalgo` but libsignal-go doesn't have
(evaluated 2026-09-25):

| Gap                                              | Upstream crate(s)                              | signalmeow uses it for                   |
| ------------------------------------------------ | ---------------------------------------------- | ---------------------------------------- |
| zkgroup (credentials, ciphertexts, endorsements) | `zkgroup`, `zkcredential`, `poksho` (~20k LOC) | groups v2, profiles, group send tokens   |
| SGX attestation + Noise NK / NKhfs               | `attest` (`dcap`, `cds2`, `sgx_session`)       | contact discovery (`NewCDS2ClientState`) |
| HPKE seal/open on identity keys                  | `protocol` (hpke)                              | our `hpke.go` (device creation time)     |
| Delta v0.96.4 → our pinned v0.102.2              | all                                            | protocol changes since the fork's pin    |

Integration seam: `libsignalgo` sits in the same Go module as `signalmeow`
(`go.mau.fi/mautrix-signal`), so there's no way to swap it out from the outside. We'll use two
forks. The first is `cwbudde/libsignal-go`, which gets the new implementations. The second is
`cwbudde/mautrix-signal`, originally a thin fork adapting `pkg/libsignalgo`: the
existing CGO files and pure-Go twins implement the same exported API on top of
libsignal-go (the current tag is `libsignal_go`). Since `v0.2609.0-purego.11`, the
fork also deliberately exposes invite preview/single-attempt joining and opt-in
websocket credential logging redaction. Keep those extensions bounded and tested
on both backends; preserve them when rebasing until upstream offers equivalent
behavior. The maintenance procedure records this exception, and the shim can
still be offered upstream separately.

#### 7.1 Fork and re-pin libsignal-go

- [x] Fork to `github.com/cwbudde/libsignal-go` and rename the module path (a `replace` directive
      would break `go install` for users). Keep `GoCodeAlone` as the `upstream` remote and merge
      from it regularly. (Protos regenerated with the pinned buf, since `go_package` sits in the
      raw descriptors. First release `v0.7.1-cw.1`.)
- [x] Re-pin its Rust compat harness (`compat/rust-harness`) from v0.96.4 to the libsignal tag in
      `third_party/libsignal` (v0.102.2). Regenerate the vectors, then port whatever the drift
      breaks. (No drift: the harness follows v0.102.2 to SPQR v1.5.3, libcrux-ml-kem 0.0.10 and
      Rust 1.98.1, and every vector and fixture regenerates byte-identical. Upstream's changes in
      `protocol`/`usernames`/`account-keys` between the tags are refactors, the removal of
      `should_use_nonpq_session`, and new account-keys APIs not ported yet.)
- [x] Point the fork's `upstream-pin` workflow at _our_ pin (the version libsignalgo expects), not
      at upstream's latest (read from the mautrix fork's `signalversion`; needs the
      `GH_MANAGEMENT_TOKEN` secret before its scheduled runs do anything)
- [x] Record the fork policy (what we change, how we merge from upstream) in the fork's
      `decisions/` (`0007-cwbudde-fork-policy.md`, including the manual re-pin steps the script
      can't do: toolchain, direct `spqr`/`libcrux-ml-kem` pins)

**Done when:** the fork's CI, including live Rust↔Go interop, is green against v0.102.2.
(Green on 2026-09-26: build/test on linux and macOS, lint, `compat-interop`.)

#### 7.2 mautrix-signal fork and build tag

- [x] Fork to `github.com/cwbudde/mautrix-signal`, with a `purego` branch rebased on the tag we pin
      (v0.2609.0)
- [x] Add `//go:build !purego` to every CGO file in `pkg/libsignalgo`, then add a
      `libsignalgo_purego.go` skeleton that declares the full exported API (the ~124 symbols
      signalmeow uses) returning `ErrNotImplemented`. With that, `go build -tags purego` compiles.
      (One generated `x_purego.go` twin per cgo file, 48 in all, covering all 516 exported
      declarations. `internal/stubgen` generates them and checks API parity, and the fork's
      `purego.yml` CI runs it. `DeserializeServerPublicParams` is hand-written, because
      signalmeow calls it at init.)
- [x] go-signal: a `just build-purego` recipe (`CGO_ENABLED=0 go build -tags purego`). Our own CGO
      files (`internal/signal/libsignal.go`, `hpke.go`) get pure counterparts behind the same tag.
      (Also `username.go`'s username hash, now `username_cgo.go` plus `username_purego.go` on
      libsignal-go's `usernames`. HPKE is real already, see 9.4. `just check-purego` and the
      `test-unit` CI job vet, lint and test the purego build.)
- [x] Decide how go-signal consumes the fork: always require the fork (simple; the CGO build uses
      it too), or use a `replace` only in a purego build. Record the choice in §1. (Always replace.)
- [x] SQLite without cgo: `modernc.org/sqlite` in purego builds (`internal/store/sqlite_*.go`), with
      the same pragmas (`TestConnectionPragmas` runs in both builds). The store tests that don't
      need libsignal keys run in both builds.

**Done when:** `CGO_ENABLED=0 go build -tags purego ./...` produces a binary, and the CGO build
is unchanged. (The purego binary is fully static and starts; `version` and `account show` work,
and `just check` is green.)

#### 7.3 Shim: protocol core onto libsignal-go

- [x] Keys and addresses: `PrivateKey`, `PublicKey`, `IdentityKey(Pair)`, `KyberKeyPair`,
      `ServiceID`/`Address`, `GenerateRandomness`
      (Keys and addresses ported in the mautrix fork's `3027201`, tag `v0.2609.0-purego.3`; `TestDiffKeys`,
      `TestDiffServiceIDs` and `TestDiffPreKeyRecords` match the cgo build. `GenerateRandomness`
      now uses `crypto/rand` in the local Phase 8.3 shim; that change is not released yet.)
- [x] Prekeys and records: `PreKeyRecord`, `SignedPreKeyRecord`, `KyberPreKeyRecord`,
      `PreKeyBundle`, `SessionRecord`, `SenderKeyRecord`. The serialized forms must be
      **byte-identical** to the CGO backend, so that one DB works with either backend.
      (Fork `3027201`. `TestDiffPreKeyRecords` builds EC pre-key records from the same keys on
      both backends and gets the same bytes; Kyber, session and sender key records written by
      either backend load in the other and serialize back to the same bytes. The fork's
      `TestCrossBackend` checks the same for committed fixtures.)
- [x] Store interfaces (`SessionStore`, `IdentityKeyStore`, `PreKeyStore`, `SignedPreKeyStore`,
      `KyberPreKeyStore`, `SenderKeyStore`) mapped onto libsignal-go's store interfaces, with no
      callback trampolines
      (Fork `3027201`, `storeadapters_purego.go`: direct method calls. The fork's session, group
      and sealed-sender tests go through them in the purego build.)
- [x] Session cipher (`Encrypt`, `Decrypt`, `DecryptPreKey`, `ProcessPreKeyBundle`), group cipher
      and SKDM, sealed sender (`SealedSenderEncrypt`, `SealedSenderMultiRecipientEncrypt`,
      `SealedSenderDecryptToUSMC`, `SenderCertificate`), `DecryptionErrorMessage`,
      `PlaintextContent`
      (Ported in fork `3027201`. libsignal-go interoperates with the cgo build in both
      directions for all of it (`TestDiffSessions`, `TestDiffGroupCipher`,
      `TestDiffSealedSender` for v1 and v2, `TestDiffDecryptionErrorMessage`). Fork `0a18785`
      adds `TestDerivations`: the purego `DecryptionErrorMessage`/`PlaintextContent` wrappers
      reproduce the cgo build's serialized DEM, content, body and ratchet key byte for byte.)
- [x] Account entropy pool, `BackupKey`/`BackupID`/`MessageBackupKey`, `AccessKey`, AES-GCM-SIV,
      fingerprints
      (Ported in fork `3027201`. `TestDiffAccountEntropyPool`, `TestDiffAccessKey`,
      `TestDiffAES256GCMSIV` and `TestDiffFingerprint` give equal outputs on both backends, and
      the fork tests the AES-GCM-SIV and fingerprint wrappers in the purego build. Fork
      `0a18785`'s `TestDerivations` holds the purego AEP, backup key (ID, EC key, metadata,
      media and thumbnail keys), `MessageBackupKey` and `AccessKey` wrappers to known answers
      recorded by the cgo build.)
- [x] `InitLogger`/`Version` as thin stubs. `Version` reports the libsignal-go version and our pin.
      (`InitLogger` does nothing in the fork. `libsignalgo.Version` stays the pin, and go-signal's
      `version` adds a `libsignal-go:` line from the build info in purego builds:
      `libsignal: v0.102.2` / `libsignal-go: v0.7.1-cw.3`.)
- [x] Differential tests in go-signal (`cgo` build tag): run libsignal-go and the CGO libsignalgo
      on the same inputs, and require equal serialized records and mutual decryptability in both
      directions (`internal/signal/purego_diff_test.go`; username hash and HPKE so far)
      (Keys, records, service IDs, 1:1 sessions, sender keys, sealed sender v1/v2, decryption
      error messages and the key derivations. Each flow runs with cgo and with libsignal-go as
      the sender (`purego_diff_parties_test.go`).)

**Done when:** a purego build links a device and does 1:1 send/receive against the live server,
and a DB created with the CGO build keeps working with the purego build (and the other way round).
(Live linking/send/receive remains open. Offline database switching is covered by
`scripts/test-backend-switch.sh`, run by `just test-diff` and CI: separate CGO and purego test
binaries alternate over the same temporary `account.db` files, starting with either backend.
They preserve ACI/PNI identity keys, consume persisted EC/Kyber prekeys, continue session replies
and sender-key messages across switches, recover skipped session/group-message keys and reject
replays. Tampered prekey, session and group messages return no plaintext and leave serialized
sessions, identities, prekeys and sender-key records unchanged; the valid message still decrypts.
`TestProtocolStateSurvivesReopen` runs the same scenario within each backend's ordinary suite.)

### Phase 8 — zkgroup in pure Go (in the libsignal-go fork)

Port `poksho`, `zkcredential` and the client side of `zkgroup` at the pinned version. The APIs
take explicit 32-byte `randomness`, so every step is deterministic and can be checked against
vectors. Server-side issuance is ported too, **for tests only** (it lives in `internal/` or
`_test` code), so that round-trips can run without Rust. Every subphase gets committed vectors
from the Rust harness plus live interop: Go creates, Rust verifies, and the other way round.

#### 8.1 poksho

- [x] SHO (`ShoHmacSha256`, `ShoSha256`), scalar/point helpers on `ristretto255`
- [x] Statement/proof engine (`statement.rs`, `proof.rs`), `sign`
- [x] Vectors from `rust/poksho` tests. Constant-time review of scalar handling.
      (Implemented in the sibling `libsignal-go` fork's `poksho` package against v0.102.2:
      explicit 32-byte randomness, canonical proof encoding, transactional statement validation,
      and proof self-verification. `compat/vectors/poksho.json` contains 43 SHO/signature/proof
      cases and 16 scalar/point conversion cases generated by the pinned Rust crate.
      `poksho/CONSTANT_TIME.md` records the source-level review, including potentially secret
      points; it is not an independent audit.)

**Done when:** poksho proofs made in Go verify in Rust and the other way round.
(Verified locally: `TestPokshoVectors`, `TestPokshoInterop` with fresh messages/randomness,
`TestPokshoVectorRegeneration` with two byte-identical regenerations, and malformed-input
rejection. Full fork pure-Go build/tests, race tests, vet, lint and Rust interop pass;
`FuzzProof` completed 1.53 million executions. No fork release or go-signal dependency bump
is needed for this standalone milestone.)

#### 8.2 zkgroup crypto layer

- [x] `uid_struct`/`uid_encryption`, `profile_key_struct`/`profile_key_encryption`,
      `profile_key_commitment`, `timestamp_struct`
      (Implemented in the sibling `libsignal-go` fork's `zkgroup/zkcrypto` package.
      Includes ACI/PNI binding, reversible Lizard/Elligator encoding, authenticated
      decryption, canonical serialization, commitments with secret nonces, and timestamp
      scalar derivation. `compat/vectors/zkgroup-crypto.json` contains 40 pinned Rust cases
      and all three system-parameter sets. Live interop checks fresh inputs, mutual
      decryption, wrong UUID/group-key rejection and malformed ciphertexts. Degenerate
      profile keys that map to the identity retain upstream's decryption rejection.)
- [x] `credentials` (KVAC), `signature`, `proofs`, `profile_key_credential_request`
      (Implemented in the sibling fork's `zkgroup/zkcrypto`: all six historical
      key layouts, profile and receipt blinded issuance/unblinding, signatures,
      all active request/issuance/presentation proofs, and deserialize-only V1/V2
      profile presentations. `compat/vectors/zkgroup-credentials.json` records
      24 complete pinned Rust flows; live tests add 16 fresh random inputs,
      mutual verification and altered metadata/key/ciphertext rejection.
      Go retains exact-length parsing; a differential regression test records
      the pinned Rust decoder's unexpected acceptance of trailing proof bytes.)
- [x] `zkcredential`: attributes, credentials, issuance, presentation, endorsements
      (Implemented in the sibling fork's `zkcredential` package: domain-separated
      attributes, standard/legacy credential modes, clear/blinded issuance,
      mixed encrypted/revealed presentations and tag-derived batch endorsements.
      `compat/vectors/zkcredential.json` contains 52 complete pinned Rust flows
      spanning every supported arity and both presentation key policies. Live
      interop adds 26 fresh randomized flows, mutual verification, altered-input
      rejection and a regression for standard-mode individual public-key binding.
      Parsers bound vector allocations and reject trailing data; empty endorsement
      batches return errors instead of upstream's indexing panic.)

**Done when:** crypto-layer vectors match byte for byte.
(Attribute/encryption milestone verified: vectors and two regenerations are byte-identical;
full fork pure-Go tests/build, race tests, vet, lint and Rust interop pass. Lizard and
encoding fuzzers completed 245,400 and 743,523 executions, respectively.
Legacy credential/proof milestone also verified: all serialized artifacts and final
SHO states match Rust, two vector regenerations are byte-identical, and full fork
pure-Go tests/build, race tests, vet, lint and live Rust interop pass. Credential
encoding fuzzing completed 499,755 executions. `zkgroup/zkcrypto/CONSTANT_TIME.md`
records the extended source-level review. Generic zkcredential milestone verified:
all artifacts and final SHO states match Rust, two regenerations are byte-identical,
and full fork pure-Go tests/build, race tests, vet, lint and live Rust interop pass.
Generic encoding fuzzing completed 392,873 executions; `zkcredential/CONSTANT_TIME.md`
records its source-level timing review. Phase 8.2 is complete; no fork release or
dependency bump is needed until the API/shim integration.)

#### 8.3 zkgroup API: groups and profiles

- [x] `ServerPublicParams` (deserialize, `VerifySignature`, `NotarySignature`)
- [x] `GroupMasterKey` → `GroupSecretParams`/`GroupPublicParams`/`GroupIdentifier`,
      `UUIDCiphertext` and `ProfileKeyCiphertext` encrypt/decrypt
- [x] `ProfileKey` (commitment, version, access key), `ProfileKeyCredentialRequestContext`,
      `ExpiringProfileKeyCredential(Response)`, `ProfileKeyCredentialPresentation`
- [x] `AuthCredentialWithPni` (receive response, create presentation)

Implemented locally in the sibling `libsignal-go` fork's `zkgroup` package and
`mautrix-signal`'s `pkg/libsignalgo` purego shim. Includes padded group-attribute blobs,
cryptographic randomness, exact serialized layouts, and credential time policies.
Profile presentations V1–V4 parse; new profile/auth presentations are V4 (wire byte 3).
Server issuance and verification live in `internal/zkgroupserver` for tests only.

Offline evidence: `compat/vectors/zkgroup-api.json` contains 16 complete pinned Rust
flows; live Rust interop adds 16 randomized flows, mutual verification, altered-input
rejection, and two byte-identical regenerations. The shim consumes a Rust fixture in
both builds, and `TestDiffZKGroup` compares fresh group/profile encryption against CGO.
Full library tests/race tests, build, vet, lint and Rust interop pass; API encoding
fuzzing completed 353,263 executions. `zkgroup/CONSTANT_TIME.md` records the source review.
Go-signal's CGO race suite, purego checks/build, pin and formatting checks pass with
local dependencies. Module tidiness is checked with a temporary modfile because
`go mod tidy` ignores workspace replacements for unpublished packages.

`scripts/test-zkgroup-integration.sh` uses a test-only overlay to exercise signalmeow's
group-response decryption/profile-key storage and a mocked WebSocket profile fetch in
both builds. Failed endorsement caching does not prevent returning group data. The
WebSocket test exposes an existing shutdown race in signalmeow's `connectLoop` under
`-race`; it is outside the libsignalgo-only fork boundary and remains unfixed.

**Done when:** a purego build lists groups (Phase 4.2) and fetches profiles against the live
server. **Live acceptance remains pending**: no account was accessed. Endorsements are
Phase 8.4. (2026-09-27: published. The zkgroup packages are in libsignal-go `v0.7.1-cw.4`, the
shim in mautrix-signal `v0.2609.0-purego.4`, and go.mod pins both.)

#### 8.4 Group send endorsements

- [x] `GroupSendEndorsementsResponse` (receive/verify), `GroupSendEndorsement` (combine, remove),
      `GroupSendFullToken`, expiry handling
- [x] Shim wiring for multi-recipient sealed-sender group sends

Implemented locally in the sibling forks, with canonical bounded codecs, batch-proof
verification, protocol ordering by doubled ciphertext points, combine/remove and
bearer-token conversion. Receipt requires a day-aligned expiry between two hours
and seven days away, inclusive. The shim excludes the local user from the combined
endorsement while retaining every individual member in its map. The CGO wrapper
now uses Rust's appended combined result directly, fixing its previous duplication
of endorsements.

`compat/vectors/group-send.json` contains 16 pinned Rust flows, including mixed
ACI/PNI identities and single-member groups. Interop adds 16 randomized flows,
mutual verification, recipient/key/tampering rejection, exact expiry boundaries
and two byte-identical regenerations. Parser fuzzing completed 2,258,150 executions.
The shared shim tests run against both backends with fresh expiry timestamps.
The timing/secret-handling review is in `zkgroup/CONSTANT_TIME.md`.

`scripts/test-zkgroup-integration.sh` verifies cache insertion/expiry and exercises
signalmeow's actual multi-recipient sender over localhost in both builds. It checks
the exact `Group-Send-Token` recipients and expiry, decrypts the text at both
recipients, and rejects per-recipient fallback requests. Session metadata and an
already-distributed sender key are supplied by test stores. The known upstream
WebSocket shutdown race documented in 8.3 remains outside the fork boundary.

**Done when:** a purego build sends to a group using endorsements (no fallback to per-recipient
sends). **Offline acceptance passes; live-server acceptance remains pending.** No
account was accessed. (2026-09-27: published with 8.3 in libsignal-go `v0.7.1-cw.4` and
mautrix-signal `v0.2609.0-purego.4`, which go.mod pins. The CGO wrapper fix is its own fork
commit, `303436b`.)

### Phase 9 — Enclaves, HPKE and the version delta

#### 9.1 Noise

- [x] `Noise_NK_25519_ChaChaPoly_SHA256` and `Noise_NKhfs_25519+Kyber1024_ChaChaPoly_SHA256`
      (hybrid forward secrecy with Kyber). Check whether an existing Go Noise library can be
      extended for `hfs`; otherwise write a minimal handshake that only supports these two
      patterns.
      (2026-09-26 — Minimal handshake of our own: flynn/noise has no hfs/KEM support. New `noise`
      package in the libsignal-go fork, fork commit `526388cb7`. It follows snow 0.10.0 token for
      token. snow puts `e1` after the DH, so the pattern is `-> e, es, e1` / `<- e, ee, ekem1`.
      "Kyber1024" is ML-KEM-1024 (std `crypto/mlkem`), as in attest's resolver, not the fork's
      round-3 `kem` Kyber. Initiator and responder, injectable randomness, and a `Transport` that
      chunks like `ClientConnection`. Stricter than snow: it rejects all-zero X25519 results.
      Unit tests, race tests, lint and `FuzzReadMessage` pass.)
- [x] Vectors from libsignal's `snow`-based implementation (`attest/src/snow_resolver.rs`)
      (2026-09-26 — The harness `noise` domain, fork commit `460a03a76`: snow 0.10.0 with attest's
      resolver, fed from a seeded, recording ChaCha20 stream. `compat/vectors/noise.json` has 9 cases,
      including a two-chunk transport message. `TestNoiseVectors` replays the initiator byte for
      byte and `noise.TestSnowResponderKAT` does the same for the responder (derandomized ML-KEM).
      Two regenerations are byte-identical (`TestNoiseVectorRegeneration`).)

**Done when:** Go↔Rust handshakes interoperate for both patterns.
(Green on 2026-09-26: `TestNoiseInterop` runs fresh NK and NKhfs handshakes with Go as initiator
and as responder, exchanges transport messages both ways, and checks that each side rejects
tampered or truncated messages and a wrong static key. The shim wiring is 9.3.
2026-09-27: branch `feat/noise` is merged into the fork's main and released in `v0.7.1-cw.4`.)

#### 9.2 SGX DCAP attestation

- [x] Quote v3 parsing, PCK certificate chain up to the pinned Intel SGX root CA, CRL checks
      (2026-09-26 — New `attest/dcap` package in the libsignal-go fork, branch `feat/dcap`, commit
      `277f1e917`, based on `f1669cb0b`, unpublished. It covers the v3 ECDSA quote, Open Enclave
      custom claims, the SGX PCK extension, and the ISV/QE signature and QE report checks. Chain
      validation is BoringSSL's `X509_verify_cert` with `CRL_CHECK|CRL_CHECK_ALL` at a fixed time,
      rebuilt on `crypto/x509` because `x509.Verify` has no CRLs. The root and root CRL are pinned
      to Intel's key (`RootTrustStore`). All 34 upstream tests of `sgx_quote.rs`, `evidence.rs`,
      `sgx_x509.rs`, `cert_chain.rs` and `util.rs` are ported under their names and pass.
      `TestIntelPCKChain` validates the recorded Intel PCK chain against the recorded CRLs, as
      `verify_certificates` does. The fixture uses a test-only endorsements field reader; the real
      parser is the next item.)
- [x] TCB info and QE identity verification, TCB status policy identical to `attest/src/dcap`
      (2026-09-26 — fork commit `7eed573c3` on `feat/dcap`, unpublished. `ParseEndorsements` reads
      the Open Enclave collateral and checks the TCB info and QE identity signatures over the raw
      JSON before decoding it. The decoding follows serde: exact keys, duplicate and missing fields
      rejected, u8/u16 ranges, untagged v2/v3 TCB layout. `attest` follows `attest_impl` step by
      step: all four chains and both CRLs against the root key, the QE identity (vendor ID,
      MRSIGNER, ISVPRODID, masked MISCSELECT and attributes, QE TCB level), the TCB level lookup
      (first level reached; UpToDate or SWHardeningNeeded only), the claims hash and the debug flag.
      All 5 `endorsements.rs` tests and the 21 `FakeAttestation` tests of `dcap.rs` pass under their
      names, on a port of `fakes.rs`. Extra cases cover other TCB statuses, strict JSON and tampered
      collateral.)
- [x] MRENCLAVE/config checks against the enclave constants of the pinned version, and evidence
      expiry
      (2026-09-26 — same commit. `VerifyRemoteAttestation` requires the expected MRENCLAVE and the
      acceptance of every advisory of a SWHardeningNeeded level. Expiry covers the PCK chain, all
      collateral chains and CRLs, and TCB info/QE identity `nextUpdate` with evaluation data number
      ≥ 21. `SWAdvisories` and the 15 `EnclaveID*` values are those of v0.102.2 `constants.rs`;
      raft configs stay with SVR2. The recorded CDSI handshake verifies at its timestamp (pk claim
      matches) and fails two years later, with a wrong MRENCLAVE, or with an unaccepted advisory.
      It also passes one second before the earliest collateral expiry and fails one second after.
      `test_attestation_metrics` passes.)
- [x] Use upstream's recorded attestation blobs (`rust/attest/tests/data`) as positive **and**
      negative vectors (tampered quote, expired collateral, wrong measurement)
      (2026-09-27 — fork commits `2df1e3706` and `eef58c872` on `feat/dcap`, unpublished. Every
      blob is in use. `TestRecordedVectors` covers `cds2_test` at the four `test_clock_skew`
      times plus the one-day skew, where the `pk` claim is the X25519 key of
      `cds2_test.privatekey`. It also covers `dcap_v3`, `dcap-expired` and the attestation half
      of `attest_svr2`. Each comes with tampered quote, expiry and wrong measurement cases, 18 in
      all. The expected outcomes come from upstream's `verify_remote_attestation`, run on the same
      inputs and times with `test-util`. That was needed because no upstream test uses `dcap_v3`
      and `dcap-expired`. Upstream rejects `dcap-expired` while parsing, because its PCK CRL is
      not DER. Upstream's test-only acceptance of evaluation data number 12 is an unexported
      switch that only the package's tests turn on. `TestVeryExpiredEvalNumber` checks that it
      is off by default and that the number-12 blobs fail without it.)

**Done when:** every upstream attestation test case gives the same accept/reject result in Go.
(2026-09-27: branch `feat/dcap` is merged into the fork's main and released in
`v0.7.1-cw.4`; the commits named above are unchanged.)

#### 9.3 CDSI client state

- [x] `SGXClientState`/`CDS2ClientState`: initial request, `CompleteHandshake`,
      `EstablishedSend`/`EstablishedRecv`, wired into the shim. All subtasks below are complete.
  - [x] Fork: `attest/enclave` client state on DCAP (9.2) and Noise NK (9.1)
        (2026-09-27 — released in libsignal-go `v0.7.1-cw.4`. The fork's `attest/enclave` tests
        complete the `cds2_test` handshake against a Go Noise responder; see the ported tests below.)
  - [x] Shim: `sgxclient_purego.go` on `attest/enclave`, with upstream's error codes
        (2026-09-27 — mautrix-signal `4313c0e`, released in `v0.2609.0-purego.5`, which go.mod
        pins. Errors follow upstream's `IntoFfiError` for `enclave::Error`: invalid state 2,
        attestation data 42, everything else 30 with the "SGX operation failed" prefix.
        `grep -l ErrNotImplemented pkg/libsignalgo/*_purego.go` finds nothing.)
  - [x] Shim tests in both builds, up to the handshake
        (2026-09-27 — the shared `TestCDS2ClientState*` tests pass under cgo and purego with the
        same codes. They cover construction on the recorded CDSI staging attestation, a wrong
        MRENCLAVE, time, malformed and tampered evidence, failed handshakes and wrong-state calls.)
  - [x] Purego shim test of a completed handshake (`CompleteHandshake`, then `EstablishedSend`
        and `EstablishedReceive` both ways). `scripts/test-cdsi-integration.sh` makes disposable
        copies of both pinned forks and adds a test-only helper under `attest/` to scope the
        existing evaluation-number-12 exception to the recorded `cds2_test` fixture. Neither
        published dependencies nor production code change. `TestCDSIIntegration` checks
        production rejection before and after the exception, completes Noise NKhfs against
        the known enclave key, exchanges empty/single/multiple-chunk messages at exact size
        boundaries, rejects tampering/truncation/wrong-channel messages and replays, and checks
        nonce recovery, failed-handshake state and Destroy. Runs in `just test-fork` and with
        `-race` in `just test-diff`/CI. The CGO shim uses the live acceptance below for this path;
        its compiled attestation verifier cannot enable the fixture exception.
  - [x] go-signal: the CDSI lookup path is compiled in purego builds
        (2026-09-27 — `internal/signal/meow_resolve.go` is `cgo || purego` and calls
        signalmeow's `LookupPhone`; `go list -tags purego` includes it, and `just check-purego`
        passes on `purego.5`.)
  - [x] Live acceptance: contact discovery against Signal's production servers on both backends.
        `TestIntegration/CDSI` passed on 2026-09-27 and again on 2026-09-29 (see 10.2).
        The uncached lookup completes the enclave handshake and returns the peer's PNI;
        ACI is not required without its access key. `Resolve` also succeeds for the known peer.
- [x] Port the handshake-level attestation tests on the recorded blobs: `sgx_session.rs`
      `test_clock_skew` with `SKEW_ADJUSTMENT` in the session, `test_happy_path`,
      `test_mismatched_keys` and `test_invalid_private_key` on `cds2_test`, and `cds2.rs`
      `attest_cds2`. The DCAP half of all of these is already covered by 9.2. The remaining half needs
      `Handshake::for_sgx` and Noise NK from `feat/noise`. The shim test above exposes the
      evaluation number 12 exception only in disposable test copies. `svr2.rs`
      `attest_svr2_bad_config` checks the raft config, not DCAP; PLAN.md has no SVR2 item.
      (2026-09-27 — fork commits `b9b3d8598` and `ae07c98cb`. All five are ported
      under their names in `attest/enclave`, with a Go `noise` responder in place of snow. The
      evaluation number 12 exception moved to `attest/internal/testhook`, which only packages
      under `attest/` can import and only tests turn on. Extra cases cover the input checks,
      `ClientHandshakeStart` decoding, metrics and the state machine, including a failed
      `CompleteHandshake` and a reply with a payload, which upstream rejects. The expected
      decoding and reply outcomes were checked against upstream's
      `cds2::new_handshake_with_advisories` and `Handshake::complete`. SVR2's `config` and
      `minimum_limits` claims stay undecoded; `attest_svr2_bad_config` remains unported.)

**Done when:** a purego build resolves a phone number through contact discovery (Phase 3.2).

#### 9.4 HPKE and leftovers

- [x] HPKE seal/open with libsignal's suite (DHKEM-X25519, HKDF-SHA256, AES-256-GCM). Use the
      standard library's `crypto/hpke` (Go 1.26) if it covers the suite; otherwise port it. This
      replaces `hpke.go` in purego builds. (Done early, in 7.2: `hpke_std.go` on `crypto/hpke`
      with libsignal's framing (type byte, enc, AEAD output). Checked against libsignal in both
      directions (`TestDiffHPKE`) and against a committed libsignal ciphertext.)
      Malformed-input tests also reject every truncation and single-byte mutation of that
      ciphertext, invalid key lengths/types, a wrong private key and a low-order public key,
      without returning plaintext on failure.
- [x] Sweep: any `libsignalgo` symbol still returning `ErrNotImplemented` gets implemented or
      listed as a known gap in the fork's scope matrix
      (2026-09-27 — The last three stubs are implemented in mautrix-signal and released in
      `v0.2609.0-purego.5`, which go.mod pins: `sgxclient` (`4313c0e`, see 9.3), `hsmenclave`
      (`3d7acf8`) and `devicetransfer` (`2d4d711`). They sit on libsignal-go `v0.7.1-cw.4`'s
      `attest/hsmenclave` and `devicetransfer`, with upstream's error codes. The HSM and device
      transfer tests now run in both builds. The HSM test completes a handshake against a Go
      Noise responder, including under cgo. `TestDeviceTransferFixture` certifies a key the cgo
      build made, and its to-be-signed fields match the cgo certificate except for the validity
      times. Name and `days` handling (NUL cut, UTF-8, u32 wrap, overflow) match the cgo build.
      `grep -l ErrNotImplemented pkg/libsignalgo/*_purego.go` finds nothing. `stubgen -check`
      reports 516 matching declarations.)

**Done when:** no `*_purego.go` file in the shim returns `ErrNotImplemented`, and `devices list`
shows creation times in a purego build. (The definition in `notimplemented.go` stays, because
`stubgen` emits it for new upstream API. The first half is met, as of 2026-09-27. The
`devices list` half needs the live account and remains pending.)

### Phase 10 — Hardening and switch-over

#### 10.1 CI — ✅ DONE (2026-09-27)

- [x] `test-purego` job: `CGO_ENABLED=0` build and tests (no `-race`, which needs cgo), plus the
      fork's vectors
      (2026-09-27 — `693573b`. `.github/workflows/test-purego.yaml`: `go build -tags purego`,
      `just check-purego` (vet, golangci-lint with `--build-tags purego`, tests), and the new
      `just test-fork`: the pinned libsignal-go's full suite with its committed vectors, the
      shim's purego tests, and `scripts/test-zkgroup-integration.sh -tags purego`. The script now
      tests a writable copy of the pinned fork when there is no workspace, since Go refuses
      overlays in the module cache. `test-unit` drops its purego step.)
- [x] Differential job (CGO): Phase 7.3 differential tests, extended to zkgroup and HPKE
      (2026-09-27 — `693573b`. Job `differential` in `test-cgo.yaml`, `needs: cgo`, restores the
      same `libsignal_ffi.a` cache (fails on a miss) and runs `just test-diff`: `TestDiff*` with
      `-race` (including `TestDiffHPKE` and `TestDiffZKGroup`), the shim's cgo tests
      (`TestCrossBackend`, `TestZKGroupAPI`, `TestGroupSendEndorsementShim`) and the zkgroup
      integration script on libsignal. The script runs without `-race`: upstream signalmeow's
      `SignalWebsocket.connectLoop` has a data race.)
- [x] Release matrix builds purego binaries for linux/darwin/windows × amd64/arm64 without
      per-OS runners
      (2026-09-27 — `2f3f17b`. `build-purego.yaml` cross-compiles all six on one ubuntu runner
      (`just build-purego-release <os> <arch>`, `.exe` and `.zip` for windows), smoke-runs the
      linux/amd64 binary and uploads the `purego` artifact. It runs in `tests.yaml` and as the
      `purego` job of `release.yaml`. Build only: the archives are not attached to releases until
      10.3.)
- [x] Deflake the tests that failed CI at random (found in this batch)
      (2026-09-27 — `f0f23bc`. `TestMCPServeHTTP` waited 5s, as long as the server's shutdown
      grace period; it now closes its client's idle connections before cancelling and waits 10s.
      The cause is inferred: it didn't reproduce locally. `TestSupervisorCancelDuringBackoff`
      cancelled after a fixed 10ms, which under load came before the stopped status was read; it
      now cancels once the disconnect is emitted. Stress runs: 100× and 300× with `-race`, green.)

**Done when:** both backends are green on every PR.
(2026-09-27: `tests` run `36297560916` on `2f3f17b` is green in all 7 jobs: test-unit,
test-purego, build-purego, test-cgo, differential, test-lint, test-format. The logs show
`libsignal-go/compat`, `pkg/libsignalgo` and `pkg/signalmeow` passing in test-purego and
differential, and the six archives in build-purego. `tests.yaml` runs on every pull request to
main, as on pushes.)

#### 10.2 Security hardening

- [x] Go native fuzzing for every parser (wire messages, records, certificates, quotes, zkgroup
      serializations) in the fork
      (2026-09-27 — libsignal-go main, commits `e52619e2c` and `b29cd1175`. The fork's
      `docs/fuzzing.md` lists 161 parsers: 114 already had fuzz targets, and 35 new targets plus
      one extended target cover the other 47, which makes 74 targets. It also lists what is left
      out and why (derived key material, parsers that accept any input, packages without parsers).
      `scripts/fuzz.sh` finds and runs every target; `fuzz.yml` runs it in 4 shards, 10s per target on
      PRs and pushes and 2m weekly, and uploads new corpus entries on failure. Run `36300130921`
      on `b29cd1175` is green, and its shards ran 19+19+18+18 = 74 targets. `FuzzRecv` found a
      panic in `spqr` on a corrupted stored state: a decoder that needs too few points gave a short
      header or ct2. Both sites now return `ErrInvalidState`, and the input is a regression seed
      that fails without the fix. Also fixed: gofmt on `sealedsender/known_certs.go` had turned the
      fork's `go` workflow red since the `feat/cdsi` merge; run `36300130885` is green.)
- [x] Constant-time review of secret-dependent code paths. Document the zeroization posture.
      (2026-09-27 — [review](docs/constant-time-review.md) covers the pinned libsignal-go
      `v0.7.1-cw.4`, mautrix `purego.5`, Go 1.26.0 and relevant dependency paths; also checked
      against fork main `b29cd1175`. Source review plus targeted optimized amd64 disassembly,
      not an independent audit or a blanket constant-time claim. No comprehensive zeroization:
      many shim Destroy methods are no-ops, and key/state/DB copies remain. Findings below
      are still open; completing this review does not clear the default-switch gate.)
- [x] CT-01: fix secret-dependent SPQR encapsulation-state endianness detection and balanced
      coefficient conversion in libsignal-go, preserve byte compatibility, test the edge cases
      listed in the review, inspect amd64/arm64 output, release and update the dependency pin.
      (2026-09-27 — libsignal-go `478422d0e`, released in `v0.7.1-cw.5`; mautrix-signal
      `v0.2609.0-purego.6` pins it, and so does go.mod. `FixEncapsStateEndianness` classifies all
      256 e₂ coefficients with `subtle` masks and always returns a copy swapped under a mask;
      `toBalanced` uses a sign mask, `fromBalanced` adds 10q and uses the Barrett reduction.
      `ct_test.go` pins them to the old branching code: every decisive position × 11 value
      classes, the all-ambiguous state, input and trailing message unchanged, all 3329 and 65536
      conversion inputs. The libcrux oracle, ACVP, fuzz targets, fork CI (`go`, `fuzz`, `compat`
      runs 36329631511/…472/…542) and `just test-diff` (backend switch both ways) pass. Optimized
      amd64 (`GOAMD64=v1`) and arm64 output: no division, coefficients only through
      SETcc/CMOV/CSET/CSEL/SAR, jumps only on length, loop counters and bounds.)
- [x] CT-02: replace the backend-selection `purego` tag with a distinct tag across go-signal
      and the mautrix fork: it currently disables Go's hardware AES even on capable CPUs.
      Verify release build-file selection, define/enforce supported CPU conditions or supply
      a reviewed constant-time fallback, and correct the fork's AES-GCM-SIV timing claim.
      (2026-09-27 — The backend tag is now `libsignal_go`, in mautrix-signal `v0.2609.0-purego.7`
      and go-signal. `purego_tag.go` makes `-tags purego` fail to build. `just check-aes-asm`
      (in `check-purego` and the `build-purego` workflow) asserts the stdlib AES assembly for all
      six release targets, and the workflow checks the binary's `-tags=libsignal_go` build info.
      CPU policy: warn, don't refuse. `internal/cpu.HasAESHardware` mirrors Go's selection; there
      is a startup slog warning and a `cpu` check in `mcp doctor` and the MCP `doctor` tool. No
      software fallback. The GCM-SIV comment was fixed in libsignal-go `e3aa3bf3e` (`cw.5`).)
- [x] CT-03: remove the CBC padding check's secret-dependent early return in libsignal-go;
      test all padding values/positions and preserve authentication before decryption.
      Current callers authenticate first, so this is defense in depth, not a demonstrated
      unauthenticated padding oracle.
      (2026-09-27 — libsignal-go `d6f6f7409` and `8e218f057`, in `v0.7.1-cw.5` / `purego.6`.
      The 1..16 range check joins the constant-time mask, the whole final block is scanned
      and the result branched on once; `DecryptCBC` clears the plaintext on rejection.
      `TestPKCS7UnpadMatchesReference` covers all 256 final bytes, correct and with each padding
      position corrupted. Callers are unchanged, so they still authenticate first. Optimized amd64
      and arm64 output uses only SETcc/CMOV/CSEL on the pad byte.)
- [ ] Opt-in integration suite (`-tags integration,libsignal_go`) against production with a
      dedicated test account. signalmeow is hard-wired to production hosts, zkgroup parameters
      and the CDSI enclave; there is no staging variant.
  - [x] Implement the opt-in suite, `just test-integration` (cgo, then pure Go on the same
        account), and setup documentation in docs/dev.md (2026-09-27).
  - [x] Manual linking: the initial test account was linked with the pure-Go binary; the user
        confirmed successful prior linking on 2026-09-29. No repeat manual run is needed.
  - [x] Live Receive: drain the incoming queue on both backends; check for decryption failures.
  - [x] Live CDSI: uncached contact discovery and peer resolution pass on both backends.
        CDSI returns only the PNI without the peer's access key; the test accepts that (see 9.3).
  - [x] Live Profile: fetch own and peer profiles with the zkgroup credential on both backends.
  - [x] Live NoteToSelf and Direct: sends pass on both backends, including the peer's direct
        delivery receipt. Receive, CDSI, Profile and these sends passed on 2026-09-27 and 2026-09-29.
  - [x] Reusable group fixture: `TestIntegrationCreateGroup` (`GOSIGNAL_IT_CREATE_GROUP=1`)
        creates a two-member group with invite links disabled and verifies full membership.
        Live creation with pure Go passed on 2026-09-29; keep its ID in `GOSIGNAL_IT_GROUP`.
  - [x] Live Group: send through the same account and group on cgo and pure Go, requiring the
        peer as a full member and waiting for its delivery receipt (2026-09-29).
  - [x] Correct `TestIntegrationLink` cleanup: close the connected client, reopen the temporary
        account, then Unlink with an independent timeout. Builds and lint pass (2026-09-29).
  - [ ] Live automated Link on cgo: QR provisioning, connect, device listing, note to self,
        and successful unlink cleanup. The 2026-09-29 attempt reached the QR step but expired
        without a scan (60-second idle timeout); it did not exercise provisioning or cleanup.
  - [ ] Live automated Link on pure Go: the same full lifecycle with `GOSIGNAL_IT_LINK=1`.
        This remains separate from the successful manual linking above.
- [x] IT-01: the cgo libsignalgo passes `time.Now().Unix()` (seconds) as `now` to
      `SessionCipher_EncryptMessage` and `SessionBuilder_ProcessPreKeyBundle`, which take epoch
      milliseconds (`message.go`, `prekeybundle.go`; upstream mautrix too). Unacknowledged
      sessions created under cgo store a 1970 timestamp, so cgo's 30-day stale-session rule never
      fires, and libsignal-go treats all of them as stale: after the switch the first send to such
      a device fails with "stale unacknowledged session", and signalmeow refetches pre-keys and
      starts a new session (seen live on the second backend's run).
      (2026-09-28 — mautrix-signal `acd96fe`, `v0.2609.0-purego.8`, which go.mod pins. Also
      `SessionRecord.HasCurrentState` (`SessionRecord_HasUsableSenderChain`, same unit); all
      other time arguments were already right (zkgroup's `Timestamp` is in seconds).
      `TestUnacknowledgedSessionClock` runs in both shim builds: the stored pending pre-key time
      is now, and a session backdated past 30 days is stale for `HasCurrentState` and `Encrypt`;
      it failed on cgo before the fix (stored `1790575`). Sessions stored before the fix count as
      stale once. `just test-diff`, `check-purego` and the live suite on both backends pass.
      Not yet reported upstream.)
- [ ] Consider an external review of the zkgroup and attestation ports. The default already
      flipped (10.3); this is an outstanding follow-up, not a completed review.
  - [x] Establish the internal review baseline and resolve CT-01/CT-02/CT-03 (above).
        This source review is not an independent audit.
  - [ ] Decide whether to commission an external review; record scope and the decision.
  - [ ] If commissioned, obtain the review and track findings through fixes and verification.
        Otherwise, record the deferral explicitly rather than claiming an audit was completed.

**Done when:** fuzzers run in CI (short budget), the integration suite passes against production
with the test account (signalmeow can't reach staging),
and the CT-01/CT-02 default-switch blockers above are resolved in the shipped dependencies
and build configuration.

#### 10.3 Default flip — ✅ DONE (2026-09-29)

- [x] Release binaries are built with the `libsignal_go` tag. The CGO backend stays available (`-tags cgo`
      builds, and the differential CI job keeps it honest).
      (2026-09-28 — `build.yaml` (was `build-purego.yaml`) cross-compiles linux/darwin/windows ×
      amd64/arm64 with `just build-release <os> <arch>` into `go-signal_<version>_<os>_<arch>`
      (`.zip` for windows), checks the AES assembly and the `-tags=libsignal_go` build info,
      smoke-runs the binary and the scratch image (`just smoke-image`). `release.yaml` calls it
      and attaches all six archives; the image and the Homebrew/AUR templates use them (Homebrew
      gains darwin/amd64). `just build`, `build-dev` and `run` are pure Go; `just build-cgo` builds
      the cgo backend, which `test-cgo` and `differential` keep testing.)
- [x] Phase 6.2 (musl + static CGO link) is superseded for release builds. Update the README
      install and build docs, and drop Rust from the release workflow.
      (2026-09-28 — The musl/Alpine build (`scripts/build-static.sh`, `just build-static`,
      `clean-static`) and the macOS cgo job are gone; the release workflow has no Rust, no cgo and
      no per-OS runner. README: binaries for all six targets, building needs only Go. docs/dev.md:
      prerequisites split into default and cgo, "Release build" replaces "Static build".)
- [x] `version` reports the backend (`purego`/`cgo`) and the libsignal-go fork version
      (2026-09-28 — `backend: libsignal_go`, `cgo` or `none` (`signal.Backend`, per build tag);
      `libsignal-go:` was already there. `TestVersion` checks both in each build.)
- [x] Update procedure for a mautrix-signal bump: rebase the `purego` branch, re-pin the fork's
      harness to the new libsignal tag, port the drift. Document it in `docs/maintenance.md`.
      (2026-09-28 — [docs/maintenance.md](docs/maintenance.md): rebase and stubgen, porting the
      drift, re-pinning libsignal-go (ADR 0007), tagging, the go.mod and submodule bump, and the
      test ladder ending in the live suite. docs/dev.md's upgrade section points there.)

**Done when:** a tagged release ships pure-Go binaries only, and a fresh clone builds with
`go build -tags libsignal_go` and nothing else installed.
(2026-09-29 — [v0.1.0](https://github.com/cwbudde/go-signal/releases/tag/v0.1.0), the first
release, ships the six pure-Go archives, `SHA256SUMS`, the attestation and the image (release run
36492142495). A fresh clone builds with `go build -tags libsignal_go .` with or without cgo, no
submodules, no `CGO_LDFLAGS`, no Rust. `go install -tags libsignal_go
github.com/cwbudde/go-signal@latest` works too: the mautrix fork has its own module path
`github.com/cwbudde/mautrix-signal` since `v0.2609.0-purego.9`, so go.mod has no `replace`
(docs/maintenance.md covers redoing the rename on a rebase). The Homebrew tap and the AUR package
don't exist yet; their jobs skip until the secrets are set, and the README no longer lists them.)

### Later / on demand

These remain optional/on demand. Checked foundations do not imply the user-facing feature is done.

- [x] Daemon mode: long-running receiving with our own local API for scripts and bots;
      no signal-cli JSON-RPC compatibility required.
  - [x] Shared `internal/app` use cases and persistent inbox (Phase 5).
  - [x] Long-running MCP server with authenticated loopback HTTP (5.6); this provides an
        existing transport but is not the proposed general-purpose daemon API.
  - [x] Define the local API and choose loopback HTTP JSON + SSE transport (2026-09-30).
        `/v1` exposes health, persistent inbox pages and events, text sends and explicit
        mark-read requests. One account per process; Unix sockets and richer sends remain deferred.
  - [x] Implement `daemon serve` on existing use cases and inbox, with account locking,
        bearer authentication on every route, default-deny send allowlists, read-only mode,
        independent bounded SSE readers and graceful shutdown. Partial write outcomes are
        preserved without retries; receive errors and remote unlink terminate the server.
        Shared use cases now allocate unique outgoing timestamps within one App and treat
        an explicit zero read cursor as a no-op instead of an unbounded receipt request.
  - [x] Test reconnects, cursor replay, paging/filtering, retention, concurrent/slow readers,
        storage failures, auth/configuration, read-receipt bounds and lock release using
        offline fixtures on both backends; real SQLite close/reopen preserves IDs and entries.
        [Operation and API docs](docs/daemon.md), a reconnecting standard-library Python
        script and an [implementation plan](docs/superpowers/plans/2026-09-30-daemon-api.md)
        are included. Replay is limited to retained stored entries; acknowledgement-before-store
        and pruned-history gaps remain, without exactly-once or crash-proof delivery claims.
- [ ] Group management and profile updates.
  - [x] Existing group list/show/leave commands and group title resolution (4.2).
  - [x] Integration-only two-member group creation, verified live (10.2); not a public command.
  - [x] Expose general group creation through the facade, use cases and CLI
        (`groups create <title> [--member <recipient>]...`, 2026-09-29). Creator is admin;
        repeated/self members are deduplicated, empty lists create a self-only group,
        unavailable peer credentials fall back to invitations. Caches the server-returned
        title; errors retain the prepared ID for inspection before retrying.
  - [x] Creation: validation, membership/defaults, use-case failure/no-retry tests, plain/JSON
        command goldens and documentation. The opt-in fixture now calls the public facade.
  - [ ] Creation: run the refactored fixture live on both backends; verify a pending invitation
        and acceptance on the peer's phone. Previous live fixture creation predates this API.
  - [x] Add group members, including invited/pending membership handling
        (`groups add-members <group> <recipient>...`, 2026-09-29). Fetches current membership
        and add-member permissions; skips duplicates, self, existing members and invitations.
        Unavailable profile credentials fall back to pending invitations. Administrators can
        approve join requests; banned ACIs are rejected. Submits one change without conflict
        retries, notifies new members/requesters and fetches the accepted membership before
        returning. A failed post-change fetch reports acceptance and asks for inspection.
  - [x] Addition: policy/backend failure-path tests, atomic/no-retry use-case tests,
        plain/JSON command goldens and documentation.
  - [ ] Addition: verify full membership, pending invitations and acceptance, join-request
        approval and permission failures live on both backends with disposable test groups.
  - [x] Remove group members with administrator and membership checks
        (`groups remove-members <group> <recipient>...`, 2026-09-29). One change removes
        full members, revokes ACI invitations or rejects join requests. Duplicates are
        ignored; self and absent targets fail before submission. Fetches current state,
        reports conflicts without automatic retry and preserves removed members as
        notification recipients. Removal does not ban rejoining; PNI-only invitations
        remain unsupported by the backend.
  - [x] Removal: policy and backend failure-path tests, atomic/no-retry use-case tests,
        plain/JSON command goldens and documentation.
  - [ ] Removal: verify member removal, revoked invitations and rejected join requests
        live on both backends with disposable test groups.
  - [x] Rename a group and update its cached title (`groups rename <group> <title>`,
        2026-09-29). Fetches current membership and edit permissions, rejects blank/invalid
        titles, skips unchanged titles and reports revision conflicts for a retry.
  - [x] Define and implement own-profile text updates (`profile show`, `profile update`,
        2026-09-30). Omitted text is preserved; explicit empty flags clear fields.
        Fresh authenticated current-version reads preserve the name split and encrypted
        payment/privacy fields, avatar and badges. One HTTP write is attempted, with
        verification and independent local-state/device-notification follow-up errors.
        Accepted and uncertain errors request inspection before retrying. V1 cannot
        prevent concurrent edits by another device; v2, avatar changes and remote
        storage writing are deferred.
        [Written design](docs/superpowers/specs/2026-09-30-own-profile-updates-design.md)
        and [implementation plan](docs/superpowers/plans/2026-09-30-own-profile-updates.md)
        are approved and implemented.
  - [x] Profile: crypto/byte-limit/preflight/preservation tests, backend HTTP and accepted
        failure-path tests, cancellation/lifecycle regressions, account-aware fake/use-case
        tests, plain/JSON command goldens and documentation. Independent review, `just check`
        and `just check-purego` passed; fixture cleanup isolates a disclosed pre-existing
        websocket shutdown race in the pinned dependency, without fixing production code.
  - [ ] Profile: verify text mutations, omitted/empty fields, no-op, avatar/payment/privacy/
        badges, phone refresh and restoration live on both backends with a disposable account.
  - [x] Rename: facade/use-case tests, plain/JSON command goldens and documentation. Opt-in
        `TestIntegrationRenameGroup` passed on cgo and pure Go (2026-09-29): server title and
        revision, unchanged-title no-op and cached title checked; original title restored and
        verified on the server after each run.
  - [x] Update group settings (`groups update <group>`): description, disappearing timer in
        integer seconds, announcement mode and edit/add-member permissions. Omitted fields
        are preserved; explicit empty/zero/false values clear or disable settings. Full
        membership and original fresh permissions are checked for every supplied field,
        including unchanged administrator-only settings, before one combined patch.
        Unchanged updates do nothing; conflicts are not retried. Fresh accepted state is
        returned, with accepted ID/revision retained on follow-up failures for inspection.
        Group output adds edit/add-member permissions without a JSON schema-version bump.
  - [x] Settings: policy/atomicity/current-cache and transport failure-path tests,
        account-aware fake/application tests, CLI preflight and plain/JSON goldens,
        documentation and independent reviews. `just check`, `just check-purego`, the
        no-cgo fallback suite and `just build` passed (2026-10-02). Opaque patch failures
        report uncertainty with inspection guidance instead of claiming acceptance.
  - [ ] Settings: verify combined changes, omission/clearing, no-op, permissions, peer-phone
        updates and restoration live on both backends with disposable groups, following
        [the live procedure](docs/dev.md#group-settings-live-check).
  - [x] Standalone administrator-role changes (`groups promote|demote <group> <member>...`,
        2026-10-02), extending the merged group-settings work. Numbers, ACIs, usernames and
        self resolve before one role-only patch. Fresh full-admin authorization and every
        full-member target are checked atomically; invitations and join requests are rejected.
        Duplicates and unchanged roles are skipped. Self-demotion requires another full admin;
        demoting every administrator, including in a self-only group, is refused. Conflicts
        are not retried. Fresh accepted state is returned; accepted follow-up failures retain
        ID/revision, while opaque failures report uncertainty with inspection guidance.
  - [x] Roles: policy/ownership/revision and exact-wire tests, stale-cache authorization,
        transport failure paths, account-aware fake/application tests, CLI preflight and
        plain/JSON goldens, documentation and independent reviews. `just fmt`, `just check`,
        `just check-purego`, the no-cgo fallback suite, `just build` and `just docs-gen` passed.
        Existing group JSON schema version 1 is retained.
  - [ ] Roles: verify promotion/demotion, duplicates/no-op, permissions, invalid batches,
        self-demotion, last-admin refusal, peer-phone updates and restoration live on both
        backends with disposable groups, following
        [the live procedure](docs/dev.md#group-role-live-check). No production mutations were run.
  - [x] Banned-member management (`groups ban|unban <group> <recipient>...`, 2026-10-02).
        Numbers, ACIs and usernames resolve before one patch; fresh full-administrator
        authorization is required even for no-ops. Self targets are rejected atomically.
        Banning removes full membership, revokes ACI invitations or rejects join requests
        in the same change; absent users can also be banned. Unbanning does not add membership.
        Duplicates and unchanged bans are skipped, preserving ban times and revision;
        inconsistent active membership of an already-banned user is removed. Existing PNI
        bans are displayed and preserved, while mutation targets remain ACI-only.
        Fresh accepted state is returned without retries; accepted follow-up failures retain
        ID/revision, while opaque failures report uncertainty with inspection guidance.
        Only removed requesters join the original members/invitees as notification recipients;
        absent preventive targets and unbanned users receive no group update.
  - [x] Bans: policy/ownership/revision, exact-wire and stale-cache authorization tests,
        transport failure paths, account-aware fake/application tests, ban/add/unban
        regression, CLI preflight and plain/JSON goldens, documentation and independent
        reviews. `just fmt`, `just check`, `just check-purego` and the no-cgo fallback suite
        passed. `just build` and `just docs-gen` passed with automatic Go VCS stamping
        disabled for the sandbox's spurious `/tmp/.git`; justfile version metadata is retained.
        Group JSON adds `banned` and optional `bannedAt` with schema version 1 retained.
  - [ ] Bans: verify membership removal, invitation revocation, request rejection, denied
        link joining, unbanning, duplicates/no-op, permissions, peer-phone updates and
        restoration live on both backends with disposable groups, following
        [the live procedure](docs/dev.md#group-ban-live-check). No production mutations were run.
  - [x] Invite-link management (`groups link show|update <group>`, 2026-10-03).
        Explicit link output contains the state and active URL; ordinary group/MCP output
        contains no invite secrets. Full membership is required to read, and fresh full
        administrator permissions to update, including unchanged requests. States are
        disabled, enabled and enabled-with-approval; unknown server access exposes no URL.
        Initial enabling creates a random 16-byte password in the same patch. Disabling
        preserves the password; re-enabling restores the URL. Resetting invalidates the old
        URL without enabling a disabled link. State/reset can be combined in one patch;
        unchanged state without reset preserves the revision. Changes are not retried.
        Accepted failures retain the committed ID/revision without an unverified URL;
        uncertain failures advise inspecting `groups link show` without claiming acceptance.
  - [x] Links: permission/no-op/revision, exact-wire, URL decoding, malformed-secret,
        stale-cache authorization and transport failure tests; account-aware fake/application
        tests, CLI preflight/secret-redaction, plain/JSON goldens, documentation and independent
        reviews. `just fmt`, `just check`, `just check-purego` and the no-cgo fallback suite
        passed. `just build` and `just docs-gen` passed with automatic Go VCS stamping
        disabled for the sandbox's spurious `/tmp/.git`; justfile version metadata is retained.
        Temporary compiler/just files and caches used ignored workspace directories after
        `/tmp` quota errors. Dedicated `groupLink` JSON retains schema version 1.
  - [ ] Links: verify initial enabling, disable/re-enable, approval, resets and old-link
        rejection, permissions, no-op, peer-phone notifications and access-mode restoration
        live on both backends with disposable groups, following
        [the live procedure](docs/dev.md#group-link-live-check). No production mutations were run.
  - [x] Group avatar updates (`groups update <group> --avatar <file>|--remove-avatar`,
        2026-10-03), combinable with existing settings. Local regular PNG/JPEG files are
        bounded to 2 MiB and 2048 pixels per dimension, fully decoded for validation and
        uploaded unchanged. Prepared bytes are owned before account opening, without
        reopening the file. Omission preserves the avatar; setting always uploads and
        advances the revision, while clearing an absent avatar is a fresh-authorized no-op.
        Original fresh membership and permissions authorize every supplied field before
        upload; malformed keys and revision overflow fail before upload. One combined
        patch is attempted without conflict retries or re-uploading. Upload failures submit
        no patch; rejected patches can leave unused encrypted ciphertext on the CDN.
        Fresh server state supplies the avatar path, including later concurrent changes;
        accepted follow-up failures retain only the committed ID/revision, while uncertain
        patch failures request inspection. Ordinary group JSON adds optional `avatarPath`
        with schema version 1 retained; plain output quotes the path and neither downloads
        images nor prints paths for inaccessible groups.
  - [x] Avatars: format/size/dimension/corruption, permission/no-op/revision/ownership,
        exact combined patch, stale-cache and transport failure tests; account-aware fake,
        application/CLI preflight and eight plain/JSON goldens, documentation and independent
        review. Controller integrated checks, `just fmt`, `just check`, `just check-purego`
        and the no-cgo fallback suite passed. `just build` and `just docs-gen` passed with
        automatic Go VCS stamping disabled for the sandbox's spurious `/tmp/.git`;
        justfile version metadata is retained. No dependency changes or production requests.
  - [ ] Avatars: verify PNG/JPEG rendering, combined settings, omission, repeated sets,
        clearing/no-op, permissions, peer notifications and restoration live on both
        backends with disposable groups, following
        [the live procedure](docs/dev.md#group-avatar-live-check). Live acceptance remains open.
  - [x] Invite-link joining (`groups join <link>`). Open links join as an ordinary
        member; approval links submit a request. Fresh full membership and existing
        requests are verified no-ops. Known invitations use `groups accept` or the phone.
        One membership PATCH at most, with distinct accepted/uncertain outcomes,
        retained account-local keys and no invite secrets in join output/errors/logs.
        Direct joins verify fresh membership; requesters may not fetch full state.
  - [x] Joining: strict preflight/parser, exact wire/signature/group binding, bounded
        secret-safe HTTP and websocket logging, lifecycle/cancellation, persistence,
        account-aware fake and partial-outcome tests; four plain/JSON outcomes,
        docs, independent reviews and integrated checks. Dedicated `groupJoin` JSON
        is additive with schema version 1 retained; fork pin is
        `v0.2609.0-purego.11` without `replace` or upstream/libsignal pin changes.
  - [ ] Joining: verify open/approval links, repeats/no-ops, approval, disabled/reset/
        banned refusals, conflict/failure inspection, notifications and restoration
        live on both backends with disposable accounts/groups, following
        [the live procedure](docs/dev.md#group-join-live-check). No production
        mutations were run; live acceptance remains open.
  - [x] Invitation acceptance (`groups accept <group>`), including own PNI invitations.
        Uses only selected-account known keys, prefers ACI invitations and returns a fresh
        already-member no-op. One PATCH at most; signed promotion and fresh own ACI membership
        are verified independently, with authoritative title/revision and preserved partial
        outcomes after cancellation, persistence or notification errors.
  - [x] Acceptance: strict bounded invitation reads, ACI/PNI wire/signature binding,
        malformed/secret preflight, lifecycle/cancellation, selected-account fake isolation,
        repeat-after-accepted-failure, App/CLI/output tests and four new goldens; task reviews
        and integrated cgo/pure-Go/fallback checks. Additive six-field `groupAccept` JSON
        retains version 1; fork pin `v0.2609.0-purego.13`, without `replace` or other pin changes.
  - [ ] Acceptance: verify actual ACI and PNI invitations, no-ops, revoked/foreign refusals,
        conflict/failure inspection, notifications and restoration live on both backends
        with disposable accounts/groups, following
        [the live procedure](docs/dev.md#group-invitation-live-check). No production mutations
        were run; live acceptance remains open.
  - [x] Join-request cancellation (`groups cancel-request <group> --yes`). Uses only
        selected-account known keys and a password-free authenticated preview. Fresh
        pending=false is a verified no-op; 403/404 refusals do not prove absence.
        One PATCH at most deletes only own ACI request; signed removal is verified at
        its revision, with accepted/uncertain outcomes preserved and no automatic retries.
        Full membership, invitations, keys and title/left records are preserved.
  - [x] Cancellation: exact wire/signature/group/source/target binding, bounded sensitive
        HTTP, approval races, malformed/secret preflight, lifecycle/Close, account-aware
        fake isolation and repeat-after-accepted-failure; App/CLI/output tests and four
        new goldens, task reviews and integrated cgo/pure-Go/no-backend checks. Additive
        six-field `groupCancelRequest` JSON retains version 1; fork pin
        `v0.2609.0-purego.14`, without `replace` or other pin changes. No member
        notifications or linked-device sync; preview title is not cached full state.
  - [ ] Cancellation: verify approval requests, cancellation, repeats/no-ops/refusals,
        disabled/reset links, approval races, administrator visibility and restoration
        live on both backends with disposable accounts/groups, following
        [the live procedure](docs/dev.md#group-join-request-cancellation-live-check).
        No production Signal calls were run; live acceptance remains open.
  - [ ] PNI invitation decline. Global PNI self-membership reporting remains separate work.
  - [ ] Add command/output tests, documentation and live verification for the remaining operations.
- [ ] Stickers, stories, polls and pinned messages.
  - [x] Receive and render sticker metadata (pack ID, sticker ID and emoji), with conversion
        tests (3.5).
  - [x] Receive embedded sticker images and send stickers (2026-09-30).
        `send --sticker-pack <Signal share link> --sticker-id <number>` sends a sticker alone,
        including ID 0. Recipient resolution and allowlists precede one selected-image fetch
        and upload shared by users/groups/self. Fixed-CDN fetching bounds encrypted responses
        and verifies HMAC, ciphertext and padding. WebP, PNG/APNG and GIF bytes are preserved.
        `receive --download-attachments` saves embedded images separately from ordinary
        attachments, with private collision-safe files and per-image errors. The inbox retains
        image pointers; JSON adds optional `sticker.image` fields without a schema bump.
        Pack installation/caching, MCP sticker tools and expired-image fallback remain deferred.
  - [x] Stickers: facade crypto/HTTP/cancellation/size/redirect tests, upload ownership and
        message clone regressions, inbox compatibility, fake/use-case policy and partial-outcome
        tests, safe-download tests, plain/JSON command goldens and documentation. Independent
        reviews, `just check`, `just check-purego` and the no-cgo/no-backend-tag suite passed.
        [Implementation plan](docs/superpowers/plans/2026-09-30-stickers.md) records the batch.
  - [ ] Stickers: verify image/emoji rendering, animation, device sync and received downloads
        live on both backends with disposable accounts/groups, using the separately opt-in
        [live procedure](docs/dev.md#sticker-live-check). No production mutations were run.
  - [ ] Define and implement story send/receive support. Requires separate fork transport
        work: pinned signalmeow drops story receive payloads and has no story-send API.
  - [x] Group polls: creation, voting/withdrawal, creator closure and received poll state
        (2026-09-30). `polls create|vote|close` sends standalone content to one canonical
        group ID; votes use zero-based indexes and an explicit increasing uint32 counter.
        Partial member outcomes reuse normal sending, without automatic retries. Received
        creation is `message.poll`; `pollVote` and `pollClose` are stored controls.
        `polls show` reconstructs one bounded retained inbox snapshot offline, reporting
        missing creation, conflicts, truncation and always unknown completeness. Ordinary
        `receive` and outgoing sends do not populate this history. Direct-chat sends,
        automatic counters, durable projections and MCP/daemon poll write tools are deferred.
  - [x] Polls: exact wire/malformed-content tests, clone ownership, inbox codec/reopen and
        legacy compatibility; application policy, partial outcomes, reducer timelines and
        unread semantics; command preflight and plain/JSON goldens; documentation and
        independent reviews. `just check`, `just check-purego` and the no-cgo/no-backend-tag
        suite passed. [Implementation plan](docs/superpowers/plans/2026-09-30-polls.md)
        records the batch and receive-boundary limitation.
  - [ ] Polls: verify phone rendering, voting/withdrawal, closure and device sync live on
        both backends with disposable accounts/groups, using the separately opt-in
        [live procedure](docs/dev.md#poll-live-check). No production mutations were run.
  - [x] Pin/unpin operations and received pinned-message controls (2026-09-30).
        `pins add|remove` sends standalone controls to users/groups/self with an explicit
        positive uint32 seconds duration or forever mode. All destination allowlists and
        fresh group membership/attribute-edit permissions are checked before any sends;
        delivery outcomes preserve partial failures without retries. Received `pin`/`unpin`
        controls retain target author/timestamp and duration, including device sync.
        `pins list` reads one bounded retained inbox snapshot offline in local inbox order,
        preserving unpins, receipt-based expiry and creator deletions with always unknown
        completeness. It does not reconstruct the phone's three-pin eviction or eligibility.
  - [x] Pins: exact wire/malformed-content and clone ownership tests; inbox codec/reopen and
        legacy compatibility; application preflight, policy, partial outcomes, reducer and
        unread semantics; command and plain/JSON goldens; documentation and independent
        reviews, including the fresh-group-cache regression. `just check`, `just check-purego` and the no-cgo/no-backend-tag
        suite passed. [Implementation plan](docs/superpowers/plans/2026-09-30-pins.md)
        records the batch and receive-boundary limitation.
  - [ ] Pins: verify phone rendering, expiry, permissions, target deletion and device sync
        live on both backends with disposable accounts/groups, using the separately opt-in
        [live procedure](docs/dev.md#pin-live-check). No production mutations were run.
  - [ ] Add facade/command/output tests, documentation and live checks for each supported feature.
- [ ] Import an existing signal-cli account to avoid re-linking.
  - [ ] Map the source account format and cryptographic/session state to the local store;
        identify supported source versions and incompatible data before writing an importer.
  - [ ] Implement import without modifying the source or overwriting an existing account.
  - [ ] Test fixtures and failure recovery; document limitations and verify live send/receive.
- [ ] Primary registration (`register`/`verify`/PIN). signalmeow doesn't cover it; build on
      `libsignalgo` + `web` if demanded.
  - [ ] Design the registration flow, persistent state and required server operations.
  - [ ] Implement the registration request and verification commands.
  - [ ] Implement PIN/registration-lock handling.
  - [ ] Test interrupted/failed registration, document setup and verify with a dedicated account.
- Out of scope: voice/video calls (RingRTC), DBus

## 5. Testing strategy

- Unit tests: command wiring (`cmd.NewRootCmd()` + `SetArgs`) and renderers, with the
  `internal/signal` facade behind an interface so that most tests run without CGO against a fake
- Golden files for JSON output
- Aim for at least 80% combined statement coverage (`just test-coverage` then
  `just coverage-report`); command/MCP tests count toward the application packages,
  and the signaltest fake is excluded. Measured 86.1% with the race detector on
  2026-09-29 after group removal and MCP regression tests. Keep live production tests opt-in.
- Pure-Go backend (Phases 7–10): Rust-generated vectors and live Rust↔Go interop in the
  libsignal-go fork; differential CGO-vs-purego tests in go-signal
- MCP server tests through the SDK's in-memory transport against the fake facade
- Opt-in integration tests (`-tags integration`) against Signal's production servers with a
  dedicated test account (signalmeow is hard-wired to production). Never in CI.

## 6. Risks

| Risk                                                         | Mitigation                                                                                                                             |
| ------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------- |
| Signal server/protocol changes break us                      | Track mautrix-signal releases; Renovate/Dependabot; the facade limits how far changes spread                                           |
| libsignal version drift between the Go bindings and the `.a` | Pin the submodule to the SHA mautrix uses; fail the build if the versions differ                                                       |
| CGO complicates builds and CI                                | Cache the Rust build; provide prebuilt `libsignal_ffi.a` artifacts; static musl release                                                |
| Linked devices get unlinked after ~30 days offline           | Document it; `receive` periodically (e.g. systemd timer) to keep the link alive                                                        |
| Signal ToS / unofficial client                               | Same position as signal-cli. Document it and don't spam.                                                                               |
| Prompt injection via incoming messages (MCP)                 | Recipient allowlist, `--read-only`, attach-dir restriction, no automatic read receipts                                                 |
| Bugs in the pure-Go crypto ports (zkgroup, attestation)      | Vectors + interop + differential tests, fuzzing, CGO stays default until Phase 10.3                                                    |
| Two forks drift from upstream (libsignal-go, mautrix-signal) | Bounded shim/join/logging scope; backend parity and offline wire tests; harness pinned to our libsignal tag; documented bump procedure |
