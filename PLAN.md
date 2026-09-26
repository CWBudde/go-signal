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
go-signal receive [--timeout 5s] [--max N] [--follow] [--download-attachments <dir>] [--send-read-receipts]
go-signal react <recipient>... --target <author>:<ts> --emoji 👍 [--remove] [--group <id>]
go-signal delete <recipient>... --target <ts> [--group <id>]   # remote delete of our own message
go-signal contacts list [--blocked] [--query <q>] | show <recipient> | block <recipient>... | unblock <recipient>...
go-signal groups list | show <group> | leave <group> --yes [--promote <member>]...   # <group>: ID, master key or title
go-signal devices list
go-signal identities list [<recipient>] | show <recipient> | trust <recipient> [--safety-number <n>]
go-signal account show | sync [--timeout 60s] | unlink   # unlink = remove local data
go-signal mcp serve [--read-only] [--allow-recipient <r>]...   # MCP server on stdio
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
- [ ] Text styles (bold/italic/…) — optional, only if cheap (deferred: a markup syntax or
      signal-cli's `start:length:STYLE` offsets both need more design than they are worth now)

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
attachments again under numbered names. Thumbnails, blurhash, voice-note/borderless flags,
width/height and received stickers' images aren't handled.

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

Notes: every `groups` command fetches every group (one request per group, sequentially). Users
invited by phone number (PNI) are missing from the pending members, and we can't see a group we
were invited to by number: signalmeow skips PNI pending members when decrypting. A requesting
user probably can't fetch the group at all (403), and `UpdateGroup` fetches it first, so
cancelling a join request likely fails with `ErrNotAMember` despite the code path for it. For a
group we are only invited to, the server sends no send endorsements; signalmeow's resulting cache
errors are demoted to debug in the log bridge (only that case, recognised by the error text; libsignal
also prints its caught panic about the empty endorsements to stderr, which we can't catch). After leaving, signalmeow's endorsement update for
the new revision probably fails (only logged), its `signalmeow_groups` row stays (the group keeps
being listed, as `left`), and a failure to tell the members is only logged by signalmeow. Groups
can't be told apart as "left on another device" versus "removed" (both a 403). Avatars, access
control, banned members, invite links and group changes (`groups update`, join) are open.

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

Notes: until the inbox (5.4) consumes events, the server connects with `signal.SendOnly()`, so
incoming messages stay on the server for the next `receive`, and a remote unlink while it runs
only surfaces on the next send. `account_show` (from 5.3) landed here so that there is a tool to
list; its structured output is the `docs/json.md` account object (`output.AccountJSON`).

#### 5.3 Read-only tools

- [ ] `account_show`, `contacts_list` (with `query`), `contacts_show`, `groups_list`,
      `groups_show`, `identities_list` (`account_show` landed with 5.2)
- [ ] Input/output JSON schemas derived from Go structs; outputs reuse the `docs/json.md` types
      as structured content, with a short text summary for clients that ignore structured output
- [ ] Tool annotations: `readOnlyHint: true`

**Done when:** an agent can answer "who is in group X?" and "what is Alice's number?" through the
tools.

#### 5.4 Inbox: receiving through MCP

MCP is request/response, so incoming messages are buffered by the server and pulled by the client.

- [ ] Background receive loop writes events into our own `inbox` table (bounded retention:
      `--inbox-max-age`, `--inbox-max-count`)
- [ ] `messages_list`: filters `chat` (recipient or group), `since` (timestamp/cursor), `limit`;
      returns a cursor for the next call
- [ ] `messages_wait`: long-poll for new events with a timeout (capped, e.g. 60 s)
- [ ] Resources: `signal://chats` and `signal://chat/{id}` (recent messages); support
      `resources/subscribe` and send `notifications/resources/updated` on new messages
- [ ] Attachments: metadata only by default; `attachment_get` downloads on demand into a
      configured dir (reuses 3.7) and returns the path, or small images as image content
- [ ] `mark_read` tool; read receipts are only sent through it, never automatically

**Done when:** an agent can wait for a message, read it, and fetch its attachment, with no other
`receive` process running.

#### 5.5 Write tools and safety policy

- [ ] `send_message` (recipients or group, text, attachments from local paths, quote),
      `react`, `delete_message`
- [ ] `--read-only`: write tools are not registered at all
- [ ] `--allow-recipient <r>` (repeatable, also via config): write tools reject other recipients
      with a sentinel error; default when unset is decided here (all vs. none) and recorded in §1
- [ ] Attachment paths restricted to `--attach-dir` (no arbitrary file exfiltration)
- [ ] Tool annotations: `destructiveHint` for `delete_message`, `openWorldHint` for sends
- [ ] Optional `--confirm` mode using MCP elicitation, where the client supports it
- [ ] Incoming message text is returned as data with sender metadata; tool descriptions state
      that message content is untrusted (prompt-injection note in `docs/mcp.md`)

**Done when:** sends work end to end, and the allowlist, read-only mode and attach-dir
restriction each have tests that prove the rejection.

#### 5.6 Docs and optional HTTP transport

- [ ] `docs/mcp.md`: tool/resource reference, config snippets for Claude Code and Claude Desktop,
      safety flags, the "one process per account" rule
- [ ] Optional: streamable HTTP transport (`--listen 127.0.0.1:<port>`, bearer token) for
      clients that can't spawn a process. This overlaps with the daemon item in "Later".

**Done when:** a new user can wire go-signal into Claude Code by following `docs/mcp.md`.

### Phase 6 — Packaging and release

#### 6.1 Release pipeline

- [ ] release-please config and workflow (conventional commits → changelog + tag)
- [ ] Tag-triggered release workflow: per-OS/arch runners (linux amd64/arm64 first), reusing the
      cached `libsignal_ffi.a`
- [ ] Checksums file and (optional) cosign/SLSA provenance
- [ ] `version` prints go-signal, signalmeow and libsignal versions

**Done when:** pushing a tag produces a GitHub release with linux amd64/arm64 binaries.

#### 6.2 Static build

- [ ] musl build of `libsignal_ffi.a` (`x86_64-unknown-linux-musl`, `aarch64-unknown-linux-musl`)
- [ ] Static Go link (`-linkmode external -extldflags -static`, musl-gcc or `zig cc`)
- [ ] Smoke test: the binary runs in a `scratch`/`alpine` container (`ldd` → "not a dynamic
      executable")

**Done when:** the release ships a fully static linux binary.

#### 6.3 Docs and distribution

- [ ] Man pages and shell completions via `cobra/doc`, included in release archives
- [ ] README: install, link, send/receive quickstart, keep-alive note (30-day unlink)
- [ ] Example systemd user unit/timer for periodic `receive`
- [ ] Optional: macOS build (native runner), Homebrew tap, AUR / container image

**Done when:** a new user can install from a release and link + send by following the README.

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
`cwbudde/mautrix-signal`, a thin fork whose **only** change is under `pkg/libsignalgo`: the
existing CGO files get `//go:build !purego`, and new `//go:build purego` files implement the same
exported API on top of libsignal-go. Keeping the mautrix fork to one package keeps rebases cheap,
and the shim can be offered upstream later.

#### 7.1 Fork and re-pin libsignal-go

- [ ] Fork to `github.com/cwbudde/libsignal-go` and rename the module path (a `replace` directive
      would break `go install` for users). Keep `GoCodeAlone` as the `upstream` remote and merge
      from it regularly.
- [ ] Re-pin its Rust compat harness (`compat/rust-harness`) from v0.96.4 to the libsignal tag in
      `third_party/libsignal` (v0.102.2). Regenerate the vectors, then port whatever the drift
      breaks.
- [ ] Point the fork's `upstream-pin` workflow at _our_ pin (the version libsignalgo expects), not
      at upstream's latest
- [ ] Record the fork policy (what we change, how we merge from upstream) in the fork's
      `decisions/`

**Done when:** the fork's CI, including live Rust↔Go interop, is green against v0.102.2.

#### 7.2 mautrix-signal fork and build tag

- [ ] Fork to `github.com/cwbudde/mautrix-signal`, with a `purego` branch rebased on the tag we pin
- [ ] Add `//go:build !purego` to every CGO file in `pkg/libsignalgo`, then add a
      `libsignalgo_purego.go` skeleton that declares the full exported API (the ~124 symbols
      signalmeow uses) returning `ErrNotImplemented`. With that, `go build -tags purego` compiles.
- [ ] go-signal: a `just build-purego` recipe (`CGO_ENABLED=0 go build -tags purego`). Our own CGO
      files (`internal/signal/libsignal.go`, `hpke.go`) get pure counterparts behind the same tag.
- [ ] Decide how go-signal consumes the fork: always require the fork (simple; the CGO build uses
      it too), or use a `replace` only in a purego build. Record the choice in §1.

**Done when:** `CGO_ENABLED=0 go build -tags purego ./...` produces a binary, and the CGO build
is unchanged.

#### 7.3 Shim: protocol core onto libsignal-go

- [ ] Keys and addresses: `PrivateKey`, `PublicKey`, `IdentityKey(Pair)`, `KyberKeyPair`,
      `ServiceID`/`Address`, `GenerateRandomness`
- [ ] Prekeys and records: `PreKeyRecord`, `SignedPreKeyRecord`, `KyberPreKeyRecord`,
      `PreKeyBundle`, `SessionRecord`, `SenderKeyRecord`. The serialized forms must be
      **byte-identical** to the CGO backend, so that one DB works with either backend.
- [ ] Store interfaces (`SessionStore`, `IdentityKeyStore`, `PreKeyStore`, `SignedPreKeyStore`,
      `KyberPreKeyStore`, `SenderKeyStore`) mapped onto libsignal-go's store interfaces, with no
      callback trampolines
- [ ] Session cipher (`Encrypt`, `Decrypt`, `DecryptPreKey`, `ProcessPreKeyBundle`), group cipher
      and SKDM, sealed sender (`SealedSenderEncrypt`, `SealedSenderMultiRecipientEncrypt`,
      `SealedSenderDecryptToUSMC`, `SenderCertificate`), `DecryptionErrorMessage`,
      `PlaintextContent`
- [ ] Account entropy pool, `BackupKey`/`BackupID`/`MessageBackupKey`, `AccessKey`, AES-GCM-SIV,
      fingerprints
- [ ] `InitLogger`/`Version` as thin stubs. `Version` reports the libsignal-go version and our pin.
- [ ] Differential tests in go-signal (`cgo` build tag): run libsignal-go and the CGO libsignalgo
      on the same inputs, and require equal serialized records and mutual decryptability in both
      directions

**Done when:** a purego build links a device and does 1:1 send/receive against the live server,
and a DB created with the CGO build keeps working with the purego build (and the other way round).

### Phase 8 — zkgroup in pure Go (in the libsignal-go fork)

Port `poksho`, `zkcredential` and the client side of `zkgroup` at the pinned version. The APIs
take explicit 32-byte `randomness`, so every step is deterministic and can be checked against
vectors. Server-side issuance is ported too, **for tests only** (it lives in `internal/` or
`_test` code), so that round-trips can run without Rust. Every subphase gets committed vectors
from the Rust harness plus live interop: Go creates, Rust verifies, and the other way round.

#### 8.1 poksho

- [ ] SHO (`ShoHmacSha256`, `ShoSha256`), scalar/point helpers on `ristretto255`
- [ ] Statement/proof engine (`statement.rs`, `proof.rs`), `sign`
- [ ] Vectors from `rust/poksho` tests. Constant-time review of scalar handling.

**Done when:** poksho proofs made in Go verify in Rust and the other way round.

#### 8.2 zkgroup crypto layer

- [ ] `uid_struct`/`uid_encryption`, `profile_key_struct`/`profile_key_encryption`,
      `profile_key_commitment`, `timestamp_struct`
- [ ] `credentials` (KVAC), `signature`, `proofs`, `profile_key_credential_request`
- [ ] `zkcredential`: attributes, credentials, issuance, presentation, endorsements

**Done when:** crypto-layer vectors match byte for byte.

#### 8.3 zkgroup API: groups and profiles

- [ ] `ServerPublicParams` (deserialize, `VerifySignature`, `NotarySignature`)
- [ ] `GroupMasterKey` → `GroupSecretParams`/`GroupPublicParams`/`GroupIdentifier`,
      `UUIDCiphertext` and `ProfileKeyCiphertext` encrypt/decrypt
- [ ] `ProfileKey` (commitment, version, access key), `ProfileKeyCredentialRequestContext`,
      `ExpiringProfileKeyCredential(Response)`, `ProfileKeyCredentialPresentation`
- [ ] `AuthCredentialWithPni` (receive response, create presentation)

**Done when:** a purego build lists groups (Phase 4.2) and fetches profiles against the live
server.

#### 8.4 Group send endorsements

- [ ] `GroupSendEndorsementsResponse` (receive/verify), `GroupSendEndorsement` (combine, remove),
      `GroupSendFullToken`, expiry handling
- [ ] Shim wiring for multi-recipient sealed-sender group sends

**Done when:** a purego build sends to a group using endorsements (no fallback to per-recipient
sends).

### Phase 9 — Enclaves, HPKE and the version delta

#### 9.1 Noise

- [ ] `Noise_NK_25519_ChaChaPoly_SHA256` and `Noise_NKhfs_25519+Kyber1024_ChaChaPoly_SHA256`
      (hybrid forward secrecy with Kyber). Check whether an existing Go Noise library can be
      extended for `hfs`; otherwise write a minimal handshake that only supports these two
      patterns.
- [ ] Vectors from libsignal's `snow`-based implementation (`attest/src/snow_resolver.rs`)

**Done when:** Go↔Rust handshakes interoperate for both patterns.

#### 9.2 SGX DCAP attestation

- [ ] Quote v3 parsing, PCK certificate chain up to the pinned Intel SGX root CA, CRL checks
- [ ] TCB info and QE identity verification, TCB status policy identical to `attest/src/dcap`
- [ ] MRENCLAVE/config checks against the enclave constants of the pinned version, and evidence
      expiry
- [ ] Use upstream's recorded attestation blobs (`rust/attest/tests/data`) as positive **and**
      negative vectors (tampered quote, expired collateral, wrong measurement)

**Done when:** every upstream attestation test case gives the same accept/reject result in Go.

#### 9.3 CDSI client state

- [ ] `SGXClientState`/`CDS2ClientState`: initial request, `CompleteHandshake`,
      `EstablishedSend`/`EstablishedRecv`, wired into the shim

**Done when:** a purego build resolves a phone number through contact discovery (Phase 3.2).

#### 9.4 HPKE and leftovers

- [ ] HPKE seal/open with libsignal's suite (DHKEM-X25519, HKDF-SHA256, AES-256-GCM). Use the
      standard library's `crypto/hpke` (Go 1.26) if it covers the suite; otherwise port it. This
      replaces `hpke.go` in purego builds.
- [ ] Sweep: any `libsignalgo` symbol still returning `ErrNotImplemented` gets implemented or
      listed as a known gap in the fork's scope matrix

**Done when:** `grep ErrNotImplemented` in the shim finds nothing, and `devices list` shows
creation times in a purego build.

### Phase 10 — Hardening and switch-over

#### 10.1 CI

- [ ] `test-purego` job: `CGO_ENABLED=0` build and tests (no `-race`, which needs cgo), plus the
      fork's vectors
- [ ] Differential job (CGO): Phase 7.3 differential tests, extended to zkgroup and HPKE
- [ ] Release matrix builds purego binaries for linux/darwin/windows × amd64/arm64 without
      per-OS runners

**Done when:** both backends are green on every PR.

#### 10.2 Security hardening

- [ ] Go native fuzzing for every parser (wire messages, records, certificates, quotes, zkgroup
      serializations) in the fork
- [ ] Constant-time review of secret-dependent code paths. Document the zeroization posture.
- [ ] Opt-in staging integration suite (`-tags integration,purego`): link, 1:1, group send,
      profile fetch, CDSI
- [ ] Consider an external review of the zkgroup and attestation ports before flipping the default

**Done when:** fuzzers run in CI (short budget) and the integration suite passes on staging.

#### 10.3 Default flip

- [ ] Release binaries are built with `purego`. The CGO backend stays available (`-tags cgo`
      builds, and the differential CI job keeps it honest).
- [ ] Phase 6.2 (musl + static CGO link) is superseded for release builds. Update the README
      install and build docs, and drop Rust from the release workflow.
- [ ] `version` reports the backend (`purego`/`cgo`) and the libsignal-go fork version
- [ ] Update procedure for a mautrix-signal bump: rebase the `purego` branch, re-pin the fork's
      harness to the new libsignal tag, port the drift. Document it in `docs/maintenance.md`.

**Done when:** a tagged release ships pure-Go binaries only, and a fresh clone builds with
`go build -tags purego` and nothing else installed.

### Later / on demand

- [ ] Daemon mode: long-running `receive --follow` with a local API (unix socket / HTTP + SSE)
      for scripts and bots. Our own API; no signal-cli JSON-RPC compatibility required. Build it
      on the `internal/app` layer and the inbox from Phase 5.
- [ ] Group management (create, add/remove members, rename), profile updates
- [ ] Stickers, stories, polls, pinned messages
- [ ] Import of an existing signal-cli account, to avoid re-linking
- [ ] Primary registration (`register`/`verify`/PIN). signalmeow doesn't cover it; it would be built
      on `libsignalgo` + `web`.
- Out of scope: voice/video calls (RingRTC), DBus

## 5. Testing strategy

- Unit tests: command wiring (`cmd.NewRootCmd()` + `SetArgs`) and renderers, with the
  `internal/signal` facade behind an interface so that most tests run without CGO against a fake
- Golden files for JSON output
- Pure-Go backend (Phases 7–10): Rust-generated vectors and live Rust↔Go interop in the
  libsignal-go fork; differential CGO-vs-purego tests in go-signal
- MCP server tests through the SDK's in-memory transport against the fake facade
- Opt-in integration tests (`-tags integration`) against Signal **staging** with a dedicated test
  account. Never against live in CI.

## 6. Risks

| Risk                                                         | Mitigation                                                                                                |
| ------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------- |
| Signal server/protocol changes break us                      | Track mautrix-signal releases; Renovate/Dependabot; the facade limits how far changes spread              |
| libsignal version drift between the Go bindings and the `.a` | Pin the submodule to the SHA mautrix uses; fail the build if the versions differ                          |
| CGO complicates builds and CI                                | Cache the Rust build; provide prebuilt `libsignal_ffi.a` artifacts; static musl release                   |
| Linked devices get unlinked after ~30 days offline           | Document it; `receive` periodically (e.g. systemd timer) to keep the link alive                           |
| Signal ToS / unofficial client                               | Same position as signal-cli. Document it and don't spam.                                                  |
| Prompt injection via incoming messages (MCP)                 | Recipient allowlist, `--read-only`, attach-dir restriction, no automatic read receipts                    |
| Bugs in the pure-Go crypto ports (zkgroup, attestation)      | Vectors + interop + differential tests, fuzzing, CGO stays default until Phase 10.3                       |
| Two forks drift from upstream (libsignal-go, mautrix-signal) | mautrix fork limited to `pkg/libsignalgo`; harness pinned to our libsignal tag; documented bump procedure |
