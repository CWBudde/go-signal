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

| Date       | Decision                                                                                                                     |
| ---------- | ---------------------------------------------------------------------------------------------------------------------------- |
| 2026-09-25 | Crypto and protocol come from `go.mau.fi/mautrix-signal/pkg/signalmeow` + `libsignalgo` (CGO → `libsignal_ffi.a`). See §1.1. |
| 2026-09-25 | License: **AGPL-3.0** (required by signalmeow).                                                                              |
| 2026-09-25 | **No strict drop-in compatibility.** Idiomatic CLI (noun-verb subcommands, kebab-case), with our own documented JSON output. |
| 2026-09-25 | Priority: **plain CLI send/receive** first. The daemon/JSON-RPC is deferred.                                                 |
| 2026-09-25 | **Linked device only.** Primary registration (`register`/`verify`) is deferred indefinitely.                                 |

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

## 2. Target layout

```
main.go                  -> cmd.Execute()
cmd/                     Cobra commands (link.go, send.go, receive.go, contacts.go, ...)
internal/signal/         facade over signalmeow: Account, Client, events -> our own types
internal/store/          data-dir layout, SQLite (signalmeow store + our own tables)
internal/output/         plain / json renderers
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
go-signal version
```

Recipients are E.164 numbers, ACI UUIDs, or `@username`. JSON output uses its own schema, which is
documented in `docs/json.md` and versioned so that scripts can rely on it.

## 4. Phases

### Phase 0 — Scaffold

- [x] Repo, `reference/signal-cli` submodule, go.mod, Cobra/Viper/slog root command, `version`
- [x] justfile, treefmt, golangci-lint, CI (build, unit, lint, format)
- [x] License: AGPL-3.0
- [ ] Create GitHub repo (`github.com/cwbudde/go-signal`) and push

### Phase 1 — Build foundation and link spike

- [ ] Install Rust locally (rustup)
- [ ] Add `third_party/libsignal` submodule and a `just libsignal` recipe (cargo build of
      `libsignal-ffi`, output to `third_party/lib`). Wire `CGO_LDFLAGS` into the justfile.
- [ ] Depend on `go.mau.fi/mautrix-signal` (pinned). Check that the libsignal versions match.
- [ ] CI: install Rust, cache the libsignal build (keyed on the submodule SHA), and run CGO tests.
- [ ] **Spike:** `link` prints a `sgnl://linkdevice?...` URI plus a terminal QR code. After the
      phone scans it, `receive` prints one incoming message. This proves the whole stack.

### Phase 2 — Account and storage

- [ ] Data-dir layout: `<data-dir>/<aci>/account.db` (SQLite, WAL), plus `<data-dir>/accounts.json`
      that maps numbers to ACIs
- [ ] Single account first. Use `-a` only when more than one is linked.
- [ ] File permissions: 0700 for the directory, 0600 for the db
- [ ] `account show`, `account unlink`, `devices list`
- [ ] Handle the phone unlinking this device: detect it, report it clearly, clean up

### Phase 3 — Send and receive (the core)

- [ ] `send`: text to a contact or group, attachments, `--stdin`, quote, mentions, note-to-self
- [ ] `receive`: one-shot (`--timeout`, `--max`) and `--follow` (streaming until Ctrl-C). Show
      data messages, receipts, typing, reactions, edits, deletes and sync-sent messages from the
      phone. Plain and JSON (NDJSON, one event per line) output.
- [ ] Attachment download (`--download-attachments <dir>`), sticker/placeholder rendering in plain
      mode
- [ ] Read receipts (opt-in), `react`, `delete`
- [ ] Initial sync after linking: contacts, groups and the storage master key from the phone
- [ ] Graceful reconnects, context cancellation, clean shutdown on SIGINT/SIGTERM

### Phase 4 — Contacts, groups, identities

- [ ] `contacts list/show/block/unblock`. Resolve names for plain output.
- [ ] `groups list/show/leave`
- [ ] Safety numbers: `identities list`, `identities trust` (default policy: trust on first use,
      and warn on change)

### Phase 5 — Packaging and release

- [ ] release-please + a tag-triggered release workflow. CGO rules out simple cross-compiling,
      so use per-OS runners or `zig cc`. Targets: linux amd64/arm64 first.
- [ ] Fully static linux build (musl) as the "no dependencies" deliverable
- [ ] Man page and shell completions generated from Cobra (`cobra/doc`)

### Later / on demand

- [ ] Daemon mode: long-running `receive --follow` with a local API (unix socket / HTTP + SSE)
      for scripts and bots. Our own API; no signal-cli JSON-RPC compatibility required.
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
