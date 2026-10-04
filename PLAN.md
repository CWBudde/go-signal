# go-signal — Implementation Plan

Goal: a Signal command-line client in Go, inspired by
[signal-cli](https://github.com/AsamK/signal-cli), that runs **without a Java runtime** and ships
as a single binary. It is **not** a drop-in replacement. The CLI is idiomatic Go/Cobra with its own
command names and JSON schema. signal-cli serves as the reference for protocol behaviour and
features (`reference/signal-cli`, read-only submodule).

Status: [v0.1.0](https://github.com/cwbudde/go-signal/releases/tag/v0.1.0) shipped on 2026-09-29
with pure-Go binaries for linux/darwin/windows × amd64/arm64. Phases 0–10 are complete (summary in
§4); the open work starts at Phase 11. Development history is in git and `CHANGELOG.md`.

Legend: `[x]` done · `[ ]` open

---

## 1. Decisions

| Decision                                                                                                                                                                      |
| ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Protocol and crypto come from `signalmeow` (mautrix-signal), isolated behind the `internal/signal` facade so upstream churn stays in one package.                             |
| License: **AGPL-3.0** (required by signalmeow).                                                                                                                               |
| **No drop-in compatibility** with signal-cli: noun-verb kebab-case subcommands and our own documented JSON (`docs/json.md`, `output.SchemaVersion`).                          |
| **Linked device only.** Primary registration is deferred (Phase 17).                                                                                                          |
| Default backend is **pure Go** (`libsignal_go` tag: `cwbudde/libsignal-go` + the `pkg/libsignalgo` shim in `cwbudde/mautrix-signal`). The cgo backend stays as reference.     |
| signalmeow comes from the fork `github.com/cwbudde/mautrix-signal` under its own module path (no `replace`), tags `vX.YYMM.Z-purego.N`; libsignal-go fork tags `vX.Y.Z-cw.N`. |
| The libsignal-go compat harness is pinned to **the libsignal tag libsignalgo expects** (currently v0.102.2), not upstream's latest.                                           |
| Pure-Go builds use `modernc.org/sqlite` with the same connection options as mattn/go-sqlite3.                                                                                 |
| **Blocking** goes out as a complete `SyncMessage.Blocked` to our own devices; the phone writes the storage service. No storage-service writes of our own.                     |
| **Identity trust is TOFU**: a changed key blocks sending to that user until `identities trust`; receiving keeps working.                                                      |
| **MCP/daemon write tools send to nobody by default**: only `--allow-recipient` entries; `'*'` opts in to everyone. `--read-only` drops every tool that sends.                 |
| CPU without AES hardware: warn (startup log, `doctor`), don't refuse. No software AES fallback.                                                                               |
| Live tests run only opt-in against production with a dedicated account: signalmeow is hard-wired to production hosts, zkgroup parameters and the CDSI enclave.                |

## 2. Target layout

```
main.go                  -> cmd.Execute()
cmd/                     Cobra commands (link.go, send.go, receive.go, contacts.go, ...)
internal/signal/         facade over signalmeow: Account, Client, events -> our own types
internal/signal/signaltest/  in-memory fake Client for tests (no CGO)
internal/store/          data-dir layout, SQLite (signalmeow store + our own tables)
internal/output/         plain / json renderers
internal/app/            use-case layer shared by the CLI, the MCP server and the daemon
internal/mcp/            MCP server: tool/resource definitions, inbox, safety policy, hooks
third_party/libsignal/   submodule: signalapp/libsignal at the version libsignalgo expects (cgo only)
reference/signal-cli/    submodule: upstream Java implementation (reference only)
```

Conventions: Cobra + Viper (flag > `GOSIGNAL_*` env > `$XDG_CONFIG_HOME/go-signal/config.yaml`),
`log/slog` to **stderr** (stdout belongs to command output), `just` recipes, treefmt, golangci-lint
with `default = 'all'`, reusable GitHub workflows (`tests.yaml` → `test-*`).

## 3. CLI design

Noun-verb subcommands with kebab-case names. Global flags: `-a/--account`, `-o/--output plain|json`,
`-v/--verbose`, `--data-dir`, `--config`, `--log-format`.

```
go-signal link [--name <device-name>] [--sync-timeout 60s]
go-signal send <recipient>... -m <text> | --stdin [--attach <file>]... [--group <id>] [--quote <author>:<ts>]
go-signal send <recipient>... --edit <timestamp> -m <replacement>
go-signal send <recipient>... --sticker-pack <link> --sticker-id <n>
go-signal receive [--timeout 5s] [--max N] [--follow] [--download-attachments <dir>] [--send-read-receipts]
go-signal react <recipient>... --target <author>:<ts> --emoji 👍 [--remove]
go-signal delete <recipient>... --target <ts>
go-signal contacts list [--blocked] [--query <q>] | show <r> | block <r>... | unblock <r>...
go-signal groups list | show | create | rename | update | leave | join | accept | cancel-request
go-signal groups add-members | remove-members | promote | demote | ban | unban <group> <recipient>...
go-signal groups link show|update <group> [--state disabled|enabled|enabled-with-approval] [--reset]
go-signal polls create | vote | close | show
go-signal pins add | remove | list
go-signal profile show | update
go-signal devices list
go-signal identities list [<r>] | show <r> | trust <r> [--safety-number <n>]
go-signal account show | sync [--timeout 60s] | unlink [--local-only]
go-signal mcp serve [--read-only] [--allow-recipient <r>]... [--listen <addr>] [--on-message <prog>]
go-signal mcp doctor [<mcp serve flags>] [--offline]
go-signal daemon serve                         # loopback HTTP JSON + SSE API (docs/daemon.md)
go-signal version
```

Recipients are E.164 numbers, ACI UUIDs, `@username` or `self`; groups are IDs, master keys or
unique titles. Exit codes: 0 ok, 1 error, 3 device unlinked, 128+signal on forced exit.

## 4. Phases

Each subphase ends with a **Done when** line: the observable result that closes it.

### Completed: Phases 0–10

| Phase                          | Result                                                                                                                                                                                                                                                                                                                                   |
| ------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 0 Scaffold                     | Repo, Cobra/Viper/slog root, justfile, treefmt, lint, CI, AGPL-3.0.                                                                                                                                                                                                                                                                      |
| 1 Build foundation             | libsignal submodule + `just libsignal`, version guard (`just check-libsignal`), cgo CI with cached `libsignal_ffi.a`.                                                                                                                                                                                                                    |
| 2 Account and storage          | Facade + fake client, data-dir layout (`accounts.json`, `<aci>/account.db`, flock), multi-account selection, `account show/unlink`, `devices list`, remote-unlink detection (exit 3).                                                                                                                                                    |
| 3 Send and receive             | Supervised connection with ack flushing, recipient resolution (CDSI, usernames, self), text/attachments/quotes/mentions/edits, receive event model with plain/NDJSON output, one-shot and `--follow`, attachment download, read receipts, reactions, remote delete, initial sync and `account sync`.                                     |
| 4 Contacts, groups, identities | Contacts list/show/block with storage-service override handling, display names in all output, groups list/show/leave with title cache, identities list/show/trust with TOFU enforcement.                                                                                                                                                 |
| 5 MCP server                   | `internal/app` use-case layer, `mcp serve` (stdio and loopback HTTP), read/write tools with allowlist, `--read-only`, `--attach-dir`, `--confirm`, inbox with `messages_list/wait` and resources, `mcp doctor`, `--on-message` hooks. Docs: `docs/mcp.md`.                                                                               |
| 6 Packaging                    | release-please + release workflow, checksums and provenance, man pages and completions, README, systemd timer, container image, Homebrew/AUR templates.                                                                                                                                                                                  |
| 7–9 Pure-Go backend            | Forks of libsignal-go and mautrix-signal; protocol core shim with byte-identical records; zkgroup (poksho, zkcredential, groups/profiles, group send endorsements); Noise NK/NKhfs, SGX DCAP attestation, CDSI client state, HPKE, HSM enclave, device transfer. No shim symbol returns `ErrNotImplemented`. Differential tests vs. cgo. |
| 10 Hardening and switch-over   | Both backends on every PR, fuzzing of every parser in the fork, constant-time review (`docs/constant-time-review.md`, CT-01–03 fixed), cgo timestamp bug IT-01 fixed, opt-in live suite (`just test-integration`), default flipped to pure Go, `docs/maintenance.md` for dependency bumps.                                               |
| Later (done)                   | `daemon serve` local API (`docs/daemon.md`); group create/rename/update/avatar/add/remove/promote/demote/ban/unban/link/join/accept/cancel-request; `profile show/update`; sticker send and receive; group polls; pins. Each has unit, fake and golden tests and docs.                                                                   |

Already verified live on both backends (`just test-integration`): receive drain, uncached CDSI,
own/peer profiles, note-to-self, direct and group sends with delivery receipts, edit transport,
group creation fixture (before the public API) and group rename. Manual linking with the pure-Go
binary works.

### Phase 11 — Live acceptance of shipped features

Everything below is implemented and tested offline but not yet verified against the production
server. Use disposable accounts/groups and restore state after each run. Procedures for the group,
sticker, poll and pin checks are in `docs/dev.md`.

#### 11.1 Link, account and core messaging

- [ ] Automated `TestIntegrationLink` on cgo: QR provisioning, connect, device listing, note to
      self and unlink cleanup (the 2026-09-29 attempt expired at the QR step without a scan).
- [ ] The same full link lifecycle on pure Go (`GOSIGNAL_IT_LINK=1`).
- [ ] `devices list` (names and creation times, also on pure Go) and server-side `account unlink`.
- [ ] Remote unlink from the phone gives exit 3 with the cleanup hint.
- [ ] Ctrl-C during `receive --follow` exits within ~1 s without losing acks; a dropped network
      connection recovers.
- [ ] Attachments, quotes and mentions render on the phone; received images land byte-identical.
- [ ] Edit rendering on the peer's phone, including media and quote edits.
- [ ] Reactions and remote deletes show up on the phone for 1:1 and group targets.
- [ ] Initial sync after linking puts the phone's contacts and groups into the store.
- [ ] `contacts block|unblock` is applied by the phone (official apps replace their whole list).
- [ ] `groups list/show/leave` against groups created on the phone.
- [ ] An identity change is detected, reported and blocks sending until trusted.

**Done when:** every item passes on both backends and the remaining scenarios are covered by
`just test-integration` where they can be automated.

#### 11.2 Groups and profile

- [ ] Creation through the public API: pending invitation and acceptance on the peer's phone.
- [ ] Add members: full membership, pending invitations, join-request approval, permission failures.
- [ ] Remove members: removal, revoked invitations, rejected join requests.
- [ ] Settings (`groups update`): combined changes, omission/clearing, no-op, permissions,
      peer-phone updates ([procedure](docs/dev.md#group-settings-live-check)).
- [ ] Roles (`promote|demote`): duplicates/no-op, permissions, self-demotion, last-admin refusal
      ([procedure](docs/dev.md#group-role-live-check)).
- [ ] Bans: removal, invitation revocation, request rejection, denied link joining, unbanning
      ([procedure](docs/dev.md#group-ban-live-check)).
- [ ] Invite links: enable, disable/re-enable, approval, reset and old-link rejection
      ([procedure](docs/dev.md#group-link-live-check)).
- [ ] Avatars: PNG/JPEG rendering, combined settings, repeated sets, clearing
      ([procedure](docs/dev.md#group-avatar-live-check)).
- [ ] Joining: open/approval links, repeats, disabled/reset/banned refusals
      ([procedure](docs/dev.md#group-join-live-check)).
- [ ] Invitation acceptance: ACI and PNI invitations, no-ops, revoked/foreign refusals
      ([procedure](docs/dev.md#group-invitation-live-check)).
- [ ] Join-request cancellation: cancellation, repeats, approval races, admin visibility
      ([procedure](docs/dev.md#group-join-request-cancellation-live-check)).
- [ ] Profile updates: text mutations, omitted/empty fields, no-op, preserved avatar/payment/
      privacy/badges, phone refresh and restoration.

**Done when:** each operation behaves as documented on both backends, observed from the peer's phone.

#### 11.3 Stickers, polls, pins

- [ ] Stickers: image/emoji rendering, animation, device sync, received downloads
      ([procedure](docs/dev.md#sticker-live-check)).
- [ ] Polls: phone rendering, voting/withdrawal, closure, device sync
      ([procedure](docs/dev.md#poll-live-check)).
- [ ] Pins: phone rendering, expiry, permissions, target deletion, device sync
      ([procedure](docs/dev.md#pin-live-check)).

**Done when:** all three render and sync correctly on the phone for both backends.

#### 11.4 MCP, daemon and distribution

- [ ] `claude mcp add` (stdio) and `--transport http` snippets in `docs/mcp.md` work against a
      linked account; `--confirm` works with Claude Code's elicitation UI.
- [ ] `mcp doctor` and the `doctor` tool diagnose a real broken setup (locked, unlinked, offline).
- [ ] darwin binaries link and run (macOS was never run from a release).
- [ ] Create the `cwbudde/homebrew-tap` repo and the AUR package, set `HOMEBREW_TAP_TOKEN` /
      `AUR_SSH_KEY`, verify the release jobs push, and list both in the README again.

**Done when:** a new user can install from any channel and wire go-signal into Claude Code by
following the docs.

### Phase 12 — Messaging gaps

- [x] Disappearing-message implementation: persist direct timers, learn before acknowledgement,
      and stamp outgoing direct/group messages; offline tests and docs on both backends.
- [ ] Disappearing-message phone acceptance on disposable accounts and both backends
      ([procedure](docs/dev.md)); deferred with the live checks until a phone session is arranged.
- [x] Mention implementation: render names in received message bodies, edits and quotes;
      retain raw text and UTF-16 metadata in JSON/inbox events; send mentions in quote text.
      Offline unit/golden tests and docs cover both backends.
- [ ] Mention and quote phone acceptance on disposable accounts and both backends
      ([procedure](docs/dev.md#mention-and-quote-live-check)); deferred until a phone session is arranged.
- [ ] Plain `receive` output shows the ms timestamp that `--quote`/`react --target` need.
- [ ] Send viewed receipts (`ReceiptViewed` exists in the facade but nothing sends it).
- [ ] Respect the phone's read-receipt setting before sending read receipts.
- [ ] QR refresh during `link`: reprovision before the server drops the socket (~60 s).
- [ ] Show `last_sync` (e.g. in `account show`).
- [ ] Optional, only if cheap: text styles (bold/italic/…).
  - [ ] Choose the input syntax (markup or explicit `start:length:STYLE` offsets).
  - [ ] Convert styles to body ranges alongside mentions, preserving UTF-16 offsets.
  - [ ] Test overlapping ranges and non-ASCII text, document the syntax, verify phone rendering.

**Done when:** each item has unit/golden tests and docs, and is checked on the phone.

### Phase 13 — Groups: PNI and remaining operations

- [ ] PNI invitation decline (today `groups leave` only declines ACI invitations).
- [ ] Global PNI self-membership reporting in list/show/join/leave (currently ACI-only; only
      `groups accept` matches the own PNI).
- [ ] Revoke PNI-only invitations in `remove-members` (unsupported by the backend today).
- [ ] Command/output tests, docs and live verification for these operations.

**Done when:** a PNI invitation can be seen, declined and accepted from the CLI.

### Phase 14 — Stories and sticker extras

- [ ] Stories: define and implement send/receive. Needs fork transport work first: the pinned
      signalmeow drops story payloads on receive and has no story-send API.
- [ ] Sticker pack installation/caching, expired-image fallback, MCP sticker tools.
- [ ] Polls: direct-chat sends, automatic vote counters, durable projections, MCP/daemon poll tools.
- [ ] Facade/command/output tests, docs and live checks for each.

**Done when:** a story sent from the phone appears in `receive`, and one sent with go-signal
appears on the phone.

### Phase 15 — Security and upstream follow-ups

- [ ] Decide whether to commission an external review of the zkgroup and attestation ports;
      record scope and decision. If commissioned, track findings through fixes; otherwise record
      the deferral explicitly (the internal review is not an audit).
- [ ] Report IT-01 (cgo libsignalgo passes seconds where libsignal expects ms) to upstream mautrix.
- [ ] Get signalmeow's `SignalWebsocket.connectLoop` data race fixed (upstream or in the fork), then
      run the zkgroup integration script with `-race`.
- [ ] Group sends: sender-key encryption doesn't check identity trust, so members whose key changed
      but who hold our sender key still receive group messages.
- [ ] Identities: PNI identities can't be listed or trusted; verification state doesn't sync with
      the phone (`ContactRecord` identity state, `SyncMessage.Verified`).
- [ ] Port `attest_svr2_bad_config` if SVR2 is ever needed.

**Done when:** the review decision is recorded and the upstream issues are filed or fixed.

### Phase 16 — Import a signal-cli account

- [ ] Map the signal-cli account format and session state to our store; identify supported source
      versions and incompatible data.
- [ ] Implement the import without modifying the source or overwriting an existing account.
- [ ] Test fixtures and failure recovery; document limitations; verify live send/receive.

**Done when:** an imported account sends and receives without re-linking.

### Phase 17 — Primary registration (on demand)

signalmeow doesn't cover it; build on `libsignalgo` + `web` if there is demand.

- [ ] Design the registration flow, persistent state and required server operations.
- [ ] Implement `register` / `verify`.
- [ ] Implement PIN / registration-lock handling.
- [ ] Test interrupted/failed registration, document setup, verify with a dedicated number.

**Done when:** a fresh number registers as a primary device and sends a message.

### Known limitations (no task yet)

- signalmeow drops stories, null messages and the destination number of sync transcripts.
- Attachments are held in memory (up to 100 MiB); failed downloads can't be retried later; no
  thumbnails, blurhash or voice-note flags on send or receive.
- The storage service is always fetched in full; contact avatars, blocked groups and story
  distribution lists aren't handled; group blocking isn't supported.
- `groups list` fetches every group sequentially; "left on another device" and "removed" both look
  like a 403.
- Polls and pins are reconstructed only from the retained inbox (`mcp serve`/`daemon serve`), with
  always-unknown completeness.
- Daemon replay is limited to retained entries; no exactly-once or crash-proof delivery.

Out of scope: voice/video calls (RingRTC), DBus, signal-cli JSON-RPC compatibility.

## 5. Testing strategy

- Unit tests: command wiring (`cmd.NewRootCmd()` + `SetArgs`) and renderers against the
  `signaltest` fake, so most tests run without cgo; golden files for plain and JSON output.
- At least 80% combined statement coverage (`just test-coverage`, `just coverage-report`; 86.1% on
  2026-09-29). The fake is excluded.
- Pure-Go backend: Rust-generated vectors and live Rust↔Go interop in the libsignal-go fork,
  differential cgo-vs-pure-Go tests in go-signal (`just test-diff`), backend switching over the same
  database (`scripts/test-backend-switch.sh`).
- MCP server tests through the SDK's in-memory transport against the fake.
- Opt-in live tests (`-tags integration`, `just test-integration`) against production with a
  dedicated test account. Never in CI.

## 6. Risks

| Risk                                                         | Mitigation                                                                                                                  |
| ------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------- |
| Signal server/protocol changes break us                      | Track mautrix-signal releases; the facade limits how far changes spread; `docs/maintenance.md` bump procedure               |
| Two forks drift from upstream (libsignal-go, mautrix-signal) | Bounded shim/join/logging scope; backend parity and offline wire tests; harness pinned to our libsignal tag                 |
| Bugs in the pure-Go crypto ports (zkgroup, attestation)      | Vectors, interop, differential tests, fuzzing, cgo backend kept as reference; external review open (Phase 15)               |
| libsignal drift between libsignalgo and the cgo `.a`         | Submodule pinned to the tag libsignalgo expects; `just check-libsignal` fails on mismatch                                   |
| Linked devices get unlinked after ~30 days offline           | Documented; run `receive` periodically (systemd timer in `contrib/systemd/`)                                                |
| Signal ToS / unofficial client                               | Same position as signal-cli. Document it and don't spam.                                                                    |
| Prompt injection via incoming messages (MCP, hooks)          | Default-deny recipient allowlist, `--read-only`, attach-dir restriction, no automatic read receipts, `--hook-from` required |
