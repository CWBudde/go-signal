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

| Date       | Decision                                                                                                                            |
| ---------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| 2026-09-25 | Crypto and protocol come from `go.mau.fi/mautrix-signal/pkg/signalmeow` + `libsignalgo` (CGO → `libsignal_ffi.a`). See §1.1.        |
| 2026-09-25 | License: **AGPL-3.0** (required by signalmeow).                                                                                     |
| 2026-09-25 | **No strict drop-in compatibility.** Idiomatic CLI (noun-verb subcommands, kebab-case), with our own documented JSON output.        |
| 2026-09-25 | Priority: **plain CLI send/receive** first. The daemon/JSON-RPC is deferred.                                                        |
| 2026-09-25 | **Linked device only.** Primary registration (`register`/`verify`) is deferred indefinitely.                                        |
| 2026-09-25 | Pinned `go.mau.fi/mautrix-signal v0.2609.0`, which expects **libsignal `v0.102.2`**; `third_party/libsignal` is pinned to that tag. |
| 2026-09-25 | **MCP server** (`go-signal mcp serve`) in the same binary, after contacts/groups and before release. See Phase 5.                   |

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
  linking; we don't yet.
- **Acks.** The handler's return value decides whether the envelope is acked, and acks go out
  asynchronously, so closing right after the handler returns can lose the ack. The spike sleeps
  1 s; Phase 3.1 needs a proper drain.
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
go-signal link [--name <device-name>]          # print sgnl:// URI + terminal QR, wait for scan
go-signal send <recipient>... -m <text> [--attach <file>]... [--group <id>] [--quote <ts>]
go-signal send --stdin <recipient>             # message body from stdin
go-signal receive [--timeout 5s] [--max N] [--follow] [--download-attachments <dir>]
go-signal react <recipient> --target <author>:<ts> --emoji 👍 [--remove]
go-signal delete <recipient> --target <ts>     # remote delete
go-signal contacts list | show <recipient> | block | unblock
go-signal groups list | show <id> | leave <id>
go-signal devices list
go-signal account show | unlink                 # unlink = remove local data
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
only then acks the envelope, so unread events are redelivered next time. `Close` still waits 1 s
for pending acks when anything was acked (Phase 3.1 replaces that). `-a` already selects by
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

#### 3.1 Connection lifecycle

- [ ] Root context cancelled on SIGINT/SIGTERM; second signal forces exit
- [ ] Connect/disconnect in the facade with bounded retries and backoff for transient errors
- [ ] Graceful shutdown: drain in-flight sends, ack received envelopes, close the db cleanly
- [ ] `-v` logs connection state transitions via slog

**Done when:** Ctrl-C during `receive --follow` exits within ~1 s without losing acked messages,
and a dropped network connection is recovered automatically.

#### 3.2 Recipient resolution

- [ ] Parse E.164, ACI UUID, `@username`, and `group:<id>` into a `Recipient`
- [ ] E.164 → ACI via CDSI (signalmeow contact discovery), cached in the store
- [ ] Username → ACI lookup
- [ ] Note-to-self as a recipient (`self` / own number)

**Done when:** every recipient form resolves to an ACI (or group) in unit tests, and unknown
numbers give a clear "not on Signal" error.

#### 3.3 Send: text

- [ ] `send <recipient>... -m <text>` to one or more contacts
- [ ] `--stdin` reads the body from stdin
- [ ] `--group <id>` sends to a group (sender keys via signalmeow)
- [ ] Note-to-self and sync-sent transcript to our other devices
- [ ] Output: timestamp per recipient, per-recipient success/failure; non-zero exit if any failed

**Done when:** text arrives on the phone for 1:1, group and note-to-self sends.

#### 3.4 Send: rich content

- [ ] `--attach <file>` (repeatable): MIME sniffing, upload, size limit check
- [ ] `--quote <author>:<ts>` quoting a previous message
- [ ] Mentions: `@{<recipient>}` placeholders in the text → body ranges
- [ ] Text styles (bold/italic/…) — optional, only if cheap

**Done when:** attachments and quotes render correctly on the phone.

#### 3.5 Receive: event model and output

- [ ] Map signalmeow events to our `Event` types: data message, receipt (delivery/read/viewed),
      typing, reaction, edit, remote delete, sync-sent, sync-read
- [ ] Plain renderer: one line per event, `[time] <sender> → <dest>: <text>`, placeholders for
      stickers/attachments/unknown content
- [ ] JSON renderer: NDJSON, one event per line, documented in `docs/json.md`, golden files
- [ ] Unknown/unsupported content is reported (type name) rather than silently dropped

**Done when:** every event type above has a golden-file test for plain and JSON output.

#### 3.6 Receive: modes

- [ ] One-shot: drain queued messages, exit after `--timeout` of inactivity or `--max N` events
- [ ] `--follow`: stream until cancelled
- [ ] Exit codes: 0 on normal end, distinct code for "unlinked" (from 2.5: `cmd.ExitUnlinked` = 3)

**Done when:** `receive` works in cron (one-shot) and as a long-running process (`--follow`).

#### 3.7 Attachments on receive

- [ ] `--download-attachments <dir>`: download, decrypt, verify digest, write with a safe
      filename (`<ts>-<n>-<sanitized-name>`), no path traversal
- [ ] Emit the local path in plain and JSON output
- [ ] Without the flag, only print metadata (type, size, filename)

**Done when:** an image from the phone lands in the target dir byte-identical.

#### 3.8 Receipts, reactions, remote delete

- [ ] `--send-read-receipts` (opt-in) on `receive`
- [ ] `react <recipient> --target <author>:<ts> --emoji <e> [--remove]`
- [ ] `delete <recipient> --target <ts>` (remote delete of our own message)
- [ ] Group variants via `--group`

**Done when:** reactions and deletes show up on the phone for 1:1 and group targets.

#### 3.9 Initial sync after linking

- [ ] Request/receive contacts, groups and the storage-service master key after `link`
- [ ] Fetch the storage service manifest and persist contacts/groups/blocked list
- [ ] `link` waits for the initial sync (with progress on stderr and a timeout)

**Done when:** right after linking, contacts and groups from the phone are in the store.

### Phase 4 — Contacts, groups, identities

#### 4.1 Contacts

- [ ] `contacts list` (name, number, ACI, username, blocked), filters `--blocked`, `--query`
- [ ] `contacts show <recipient>`
- [ ] `contacts block|unblock <recipient>` (updates storage service so the phone sees it)
- [ ] Name resolution (contact name → profile name → number → ACI) used by all plain renderers

**Done when:** `receive` plain output shows names instead of UUIDs.

#### 4.2 Groups

- [ ] `groups list` (id, title, member count, our role)
- [ ] `groups show <id>`: title, description, members with roles, pending members, timer
- [ ] `groups leave <id>`
- [ ] Group id parsing: accept base64 master key / group id, and title if unique

**Done when:** list/show/leave work against groups created on the phone.

#### 4.3 Identities and safety numbers

- [ ] `identities list [<recipient>]`: identity key fingerprint, trust level, first seen
- [ ] `identities show <recipient>`: safety number (numeric + QR)
- [ ] `identities trust <recipient> [--safety-number <n>]`
- [ ] Policy: TOFU; on identity change, warn on stderr and emit an `identity-changed` event;
      sending to an untrusted changed identity requires explicit trust

**Done when:** an identity change is detected, reported, and blocked for sending until trusted.

### Phase 5 — MCP server

Expose the account to MCP clients (Claude Code, Claude Desktop, other agents) through
`go-signal mcp serve`. It uses the same binary, facade and store as the CLI. The server is a
long-running process that holds the account lock and runs its own receive loop, so it replaces
the CLI for that account while it is running. SDK: the official
`github.com/modelcontextprotocol/go-sdk` (evaluate `mark3labs/mcp-go` only if the official SDK
lacks something we need).

#### 5.1 Shared use-case layer (`internal/app`)

- [ ] Move the logic behind `send`, `react`, `delete`, `contacts`, `groups`, `identities` and
      `account show` out of `cmd/` into `internal/app` functions that take typed requests and
      return typed results (no printing, no Cobra)
- [ ] `cmd/` becomes flag parsing + `internal/app` call + `internal/output` rendering
- [ ] Recipient resolution, name resolution and trust checks live only in `internal/app`

**Done when:** the CLI behaves as before (golden files unchanged), and `internal/app` has unit
tests against the fake facade.

#### 5.2 Server skeleton and transport

- [ ] `cmd/mcp.go`: `mcp serve` command; stdio transport; server name/version from `version`
- [ ] stdout carries only the MCP protocol; all logging goes through slog to stderr (enforce with
      a test that runs the server and checks stdout contains only JSON-RPC frames)
- [ ] Account selection (`-a`), lock acquisition and connect happen before the server
      advertises tools; a locked or unlinked account fails at startup with a clear error
- [ ] Graceful shutdown on stdin EOF and SIGINT/SIGTERM (reuses 3.1)
- [ ] Tests via the SDK's in-memory transport against the fake facade (`CGO_ENABLED=0`)

**Done when:** `claude mcp add signal -- go-signal mcp serve` connects and lists the server's
tools.

#### 5.3 Read-only tools

- [ ] `account_show`, `contacts_list` (with `query`), `contacts_show`, `groups_list`,
      `groups_show`, `identities_list`
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
- MCP server tests through the SDK's in-memory transport against the fake facade
- Opt-in integration tests (`-tags integration`) against Signal **staging** with a dedicated test
  account. Never against live in CI.

## 6. Risks

| Risk                                                         | Mitigation                                                                                   |
| ------------------------------------------------------------ | -------------------------------------------------------------------------------------------- |
| Signal server/protocol changes break us                      | Track mautrix-signal releases; Renovate/Dependabot; the facade limits how far changes spread |
| libsignal version drift between the Go bindings and the `.a` | Pin the submodule to the SHA mautrix uses; fail the build if the versions differ             |
| CGO complicates builds and CI                                | Cache the Rust build; provide prebuilt `libsignal_ffi.a` artifacts; static musl release      |
| Linked devices get unlinked after ~30 days offline           | Document it; `receive` periodically (e.g. systemd timer) to keep the link alive              |
| Signal ToS / unofficial client                               | Same position as signal-cli. Document it and don't spam.                                     |
| Prompt injection via incoming messages (MCP)                 | Recipient allowlist, `--read-only`, attach-dir restriction, no automatic read receipts       |
