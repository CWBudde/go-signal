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
go-signal stickers install <signal.art-link>
go-signal stickers list
go-signal stories audiences
go-signal stories send (--group <id> | --distribution-list <uuid> | --my-story) (-m <text> | --stdin | --attach <image-or-video>) [--no-replies]
go-signal receive [--timeout 5s] [--max N] [--follow] [--download-attachments <dir>] [--send-read-receipts]
go-signal receipts send-viewed <sender> --timestamp <ms> [--timestamp <ms>]...
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

Setup: [test account](docs/dev.md#test-account-setup). Automated by the opt-in `TestIntegrationLink`,
`…Messaging`, `…RemoteUnlink`, `…LeaveGroup` and `./cmd/` `…ReceiveInterrupt`/`…ReceiveUnlinked`;
the phone-side checks follow the [core messaging live check](docs/dev.md#core-messaging-live-check).

- [x] Link lifecycle on cgo: QR provisioning, connect, device listing, note to self and unlink
      cleanup. Confirmed by the project owner from an earlier manual run (2026-10-07).
- [x] The same full link lifecycle on pure Go (`GOSIGNAL_IT_LINK=1`), 2026-10-07: device
      names/creation times, initial sync, byte-identical attachment via sync transcript, unlink.
      Each QR code lives 45 s and is single-use; have the phone's scanner open before starting.
- [x] `devices list` (names and creation times) on both backends, 2026-10-07; server-side
      unlink via the link test's cleanup.
- [ ] Remote unlink from the phone gives exit 3 with the cleanup hint.
- [ ] Ctrl-C during `receive --follow` exits within ~1 s without losing acks; a dropped network
      connection recovers.
- [ ] Attachments, quotes and mentions render on the phone; received images land byte-identical.
      Replies and reactions using timestamps copied from plain `receive` target the right messages.
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
- [x] Plain `receive` timestamp implementation: local date plus exact milliseconds in event
      prefixes and message references, including saved media and inbox output; offline tests,
      goldens and docs on both backends. Copy-to-quote/reaction phone acceptance remains in §11.1.
- [x] Send viewed receipts: explicit `receipts send-viewed` with sender resolution, validated
      and deduplicated message timestamps, plain/JSON output, offline tests and docs on both backends.
- [ ] Viewed-receipt phone acceptance on disposable accounts and both backends
      ([procedure](docs/dev.md#viewed-receipt-live-check)); deferred until a phone session is arranged.
- [x] Respect the phone's stored read-receipt setting before sending peer READ receipts:
      preserve disabled defaults across restart; real-backend receive/inbox tests and docs on both backends.
- [ ] Read-receipt setting phone acceptance on disposable accounts and both backends
      ([procedure](docs/dev.md#read-receipt-setting-live-check)); deferred until a phone session is arranged.
- [x] QR refresh during `link`: renew unscanned codes every 45 seconds with fresh sockets,
      addresses and keys; stop refreshing after a phone submission. Offline transport tests,
      CLI golden output and docs cover both backends.
- [ ] QR-refresh phone acceptance on disposable accounts and both backends
      ([procedure](docs/dev.md#link-qr-refresh-live-check)); deferred until a phone session is arranged.
- [x] Show the last complete contacts/groups sync in `account show`, with local-time plain
      output and optional UTC `lastSync` in JSON/MCP. Missing timestamps remain unknown;
      incomplete syncs preserve the previous time. Offline backend tests, goldens and docs
      cover both backends.
- [x] Optional text-style implementation: bold, italic, spoiler, strikethrough and monospace.
  - [x] Choose explicit repeatable `--style start:length:STYLE` offsets, case-insensitive.
  - [x] Convert styles to body ranges alongside mentions, preserving UTF-16 offsets after
        mention substitution; validate ranges before connecting and support edits.
  - [x] Test overlapping ranges, non-ASCII text, direct/group/self sends, attachments,
        edit envelopes and invalid inputs; command goldens and docs cover both backends.
- [ ] Text-style phone acceptance on disposable accounts and both backends
      ([procedure](docs/dev.md#text-style-live-check)); deferred until a phone session is arranged.

**Done when:** each item has unit/golden tests and docs, and is checked on the phone.

### Phase 13 — Groups: PNI and remaining operations

- [x] PNI invitation decline implementation: `groups leave` matches selected-account typed
      ACI/PNI invitations and removes both when present; full ACI membership takes precedence.
      Offline policy/backend tests, account-selection tests, plain/JSON goldens and docs
      cover both backends. Existing promotion and last-admin guards are preserved.
- [ ] PNI invitation decline phone acceptance on disposable accounts and both backends
      ([procedure](docs/dev.md#pni-invitation-decline-live-check)); deferred until a phone session is arranged.
- [x] Global PNI self-membership reporting implementation in list/show/join: match the
      selected account's typed ACI/PNI invitations, retain offered roles and prefer full
      ACI membership. Known invitations require acceptance instead of link joining.
      Offline policy/backend tests, account-selection and permission tests, plain/JSON
      goldens and docs cover both backends.
- [ ] PNI self-membership phone acceptance on disposable accounts and both backends
      ([procedure](docs/dev.md#pni-self-membership-live-check)); deferred until a phone session is arranged.
- [x] Revoke PNI-only invitations in `remove-members`: explicit `PNI:<uuid>` targets
      and phone-number resolution remove typed invitations in one change. Full members
      and requests remain ACI-only; selected-account self guards, permissions and atomic
      validation are preserved. Offline policy/backend tests cover both backends.
- [ ] PNI invitation revocation phone acceptance on disposable accounts and both backends
      ([procedure](docs/dev.md#pni-invitation-revocation-live-check)); deferred until a phone session is arranged.
- [x] Command/output tests and docs for PNI decline, self-membership reporting and
      invitation revocation, including account selection and plain/JSON goldens.
      Live verification is tracked separately above.

**Done when:** a PNI invitation can be seen, declined and accepted from the CLI.

### Phase 14 — Stories and sticker extras

- [x] Story reception implementation: opt-in fork transport delivers incoming and sent-device
      text/media stories, with typed events, group/profile key storage and failed-handler
      acknowledgement protection. Plain/JSON output, verified media downloads, durable inbox
      retention and MCP list/wait tests cover both backends. No automatic story receipts.
- [x] Group story sending implementation: `stories send --group <id>` posts a literal text
      card or one image/video to the group's full ACI members, with reply control and a
      same-timestamp own-device transcript. Fork transport uses sealed pairwise sessions;
      facade/app preflight, account selection, media validation, plain/JSON partial peer
      and transcript outcomes, tests and docs cover both backends.
- [x] Private story sending implementation: `stories audiences` inspects freshly fetched,
      complete phone-defined distribution lists; `stories send --distribution-list <uuid>`
      or `--my-story` sends text/media to their expanded ACI audiences. Per-account snapshots
      never provide a stale send fallback. Inclusion/exclusion rules, local blocks, list reply
      policy, allowlist preflight, sealed pairwise transport, intended-recipient transcripts,
      separate peer/sync outcomes, offline tests and docs cover both backends.
- [ ] Private story sending phone acceptance on disposable accounts and both backends
      ([procedure](docs/dev.md#private-story-sending-live-check)); deferred until a phone session is arranged.
- [ ] Group story sending phone acceptance on disposable accounts and both backends
      ([procedure](docs/dev.md#group-story-sending-live-check)); deferred until a phone session is arranged.
- [ ] Story reception phone acceptance on disposable accounts and both backends
      ([procedure](docs/dev.md#story-reception-live-check)); deferred until a phone session is arranged.
- [x] Sticker extras implementation: `stickers install`/`list` atomically cache complete packs
      per account, with bounded authenticated downloads and offline reuse by ID/key. Received
      expired/absent images fall back to the pack, including cover-only stickers, without
      bypassing integrity failures. MCP list/install/send/get preserve read-only, confirmation,
      allowlist and receipt policy. Facade/store/app tests, plain/JSON goldens, docs and both
      backend checks cover implementation. Installation is local, without phone-state sync.
- [ ] Sticker extras phone acceptance on disposable accounts and both backends
      ([procedure](docs/dev.md#sticker-extras-live-check)); deferred until a phone session is arranged.
- [x] Direct-chat poll implementation: `polls create`/`vote`/`close` accept one `--recipient`
      (ACI, number, username or self), preserving send-only delivery, account selection,
      allowlist policy and explicit vote counters. `polls show --recipient <ACI>` reads
      bounded retained observations offline, matching stable chat identity. Facade/app tests,
      real wire/timer checks, plain/JSON goldens, docs and both backend checks cover implementation.
- [ ] Direct-chat poll phone acceptance on disposable accounts and both backends
      ([procedure](docs/dev.md#poll-live-check)); deferred until a phone session is arranged.
- [x] Polls: automatic vote counter implementation. Omitted `--vote-count` atomically reserves
      a durable account-local counter per chat, creator and creation timestamp; explicit positive
      overrides raise the local maximum. Reservations survive failed sends, restarts and inbox
      pruning; validated own-device votes advance it before acknowledgement. Allowlist ordering,
      key/account isolation, concurrent allocation, uint32 exhaustion, receive persistence failures,
      plain/JSON goldens, docs and both backend checks cover implementation. Unseen other-device
      activity still requires coordination; local counters are not authoritative global state.
- [ ] Automatic poll vote counter phone acceptance on disposable accounts and both backends
      ([procedure](docs/dev.md#poll-live-check)); deferred until a phone session is arranged.
- [x] Polls: durable projection implementation. `polls show --durable` reads account-local
      materialized observations independently of inbox retention. Poll evidence is deduplicated
      by canonical identity/content and stored transactionally before receive acknowledgement;
      retained inbox history seeds it once. Late creation, votes, closure and deletion use the
      shared reducer; restarts/pruning, rollback/retry, concurrent database handles, account/key
      isolation, uint64 timestamps, malformed evidence, plain/JSON goldens, docs and both backend
      checks cover implementation. Completeness remains unknown; outgoing submissions are not
      observations. Poll evidence storage grows independently of general inbox retention.
- [ ] Durable poll projection phone acceptance on disposable accounts and both backends
      ([procedure](docs/dev.md#poll-live-check)); deferred until a phone session is arranged.
- [x] Polls: MCP/daemon poll tools implementation. MCP `poll_create`/`poll_vote`/`poll_close`/
      `poll_show` and daemon `/v1/polls` create/show, vote and close routes share app validation,
      allowlist and durable counters. MCP confirmation precedes reservation; read-only omits
      writes. Daemon authentication, strict inputs and read-only policy guard writes; counter
      exhaustion returns HTTP 409. Partial delivery preserves member outcomes without retry.
      Bounded/durable reads send no receipts and retain unknown completeness. API lifecycle,
      policy, confirmation, counter, partial-delivery and pruning tests, unchanged CLI/output
      goldens, docs and full cgo/pure-Go/no-cgo checks cover implementation.
- [ ] MCP/daemon poll phone acceptance on disposable accounts and both backends
      ([procedure](docs/dev.md#poll-live-check)); deferred until a phone session is arranged.
- [ ] Facade/command/output tests, docs and live checks for each.

**Done when:** a story sent from the phone appears in `receive`, and one sent with go-signal
appears on the phone.

### Phase 15 — Security and upstream follow-ups

- [x] External review decision: the project owner deferred commissioning on 2026-10-06.
      The proposed zkgroup/attestation scope, evidence and review boundaries are recorded in
      [docs/security-review.md](docs/security-review.md). No independent audit was commissioned;
      revisit when a reviewer and budget can be chosen. The internal review is not an audit.
- [ ] Report IT-01 (cgo libsignalgo passes seconds where libsignal expects ms) to upstream mautrix.
- [x] Fix signalmeow's captured incoming-request-channel race in `SignalWebsocket.connectLoop`:
      fork `v0.2609.0-purego.22` (`fda5a06`) keeps the channel reference immutable during cleanup.
      The cancel-during-dial regression reproduces the original race and passes after the fix.
      `just test-diff` now runs zkgroup integration with `-race`; both backend race integrations,
      ordinary profile/reconnect shutdown without fixture barriers, affected fork suites and
      full cgo/pure-Go/no-cgo checks pass. Published Origin/source match the reviewed commit.
- [x] Investigate remaining websocket lifecycle hazards: immediate-cancellation `Connect`
      status-channel access, handler completion, incoming-queue cancellation and pending-response
      cleanup. [Offline investigation](docs/websocket-lifecycle.md) reproduced a status race
      and three contract failures on `.22` on both backends with `-race`. Late response registration
      was initially a source-level finding; the repairs below add a deterministic regression.
- [x] Fix `Connect` status-channel lifetime and make websocket completion wait for handler and
      channel cleanup. Define queued-request shutdown semantics and verify facade resource release.
- [x] Make incoming-request enqueue cancellation-aware, including a full queue and stalled handler;
      verify reconnect and shutdown complete without losing channel ownership.
- [x] Join websocket workers before draining pending responses and honor caller cancellation after
      enqueue. Add a deterministic late-registration regression and preserve response-channel ownership.
      Published/re-pinned `v0.2609.0-purego.23` (`24dd760`); downloaded Origin and all four
      changed fork files match the reviewed commit. Both backend race suites and deterministic
      ownership probes pass, as does the real-handler facade database-release regression.
      Shutdown waits for active handlers and discards queued requests without acknowledgment;
      full-queue reconnect proceeds while a handler is stalled. `just test-fork` / `just test-diff`
      now require the repaired offline contracts; details are in the lifecycle report.
- [x] Fix the receive key-check loop self-join: fork `v0.2609.0-purego.24` (`65ae5e4`)
      separates transport disconnection from external worker joining and reference release.
      The tracked key checker delivers logout after PNI prekey rejection, including cleanup
      failures; external shutdown waits for its callback. A real-startup regression reproduced
      the `.23` deadlock and the initial-connect channel race, both repaired on `.24`.
      Published Origin/source match the reviewed commit. Affected fork suites pass with
      `-race` on both backends; required `just test-fork` / `just test-diff` gates include
      the regression. Full cgo/pure-Go/no-cgo parent checks pass; fixture limits are recorded
      in [docs/websocket-lifecycle.md](docs/websocket-lifecycle.md).
- [x] Prevent facade restart after key-check logout: supervision cancellation is prepared
      before receive workers start, and logout cancels it before registry writes or event
      delivery. The supervisor checks cancellation after joining stopped workers.
      Deterministic facade regressions reproduce the previous restart and cover receive /
      send-only logout before supervision and during worker joining, with unbuffered
      delivery, persisted unlink and final Close preserved. Full cgo/pure-Go/no-cgo checks
      and both backend race regressions pass in an isolated checkout of this change;
      concurrent ACK-flush work is preserved separately. Fixture boundaries are documented
      in [docs/websocket-lifecycle.md](docs/websocket-lifecycle.md).
- [ ] Keep the ack of a read event when receive is interrupted: live, `receive --follow`
      lost the ack of a message read ~400 ms before SIGINT because `Close`'s keepalive flush
      overtook signalmeow's handler (delivery receipt, buffer clear) and shutdown then canceled
      its response.
  - [x] Publish and pin the repair: fork `v0.2609.0-purego.25` (`f06b75b`) moves delivery
        receipts to `SimpleResponse.AfterQueued`; `WaitResponseQueued` / `WaitRequestDone`
        let `flushAcks` wait for the ack before the keepalive and the receipt afterward,
        within `ackFlushTimeout`. Downloaded Origin and all five changed fork files match
        the reviewed commit. The facade regression reproduces the lost ordering on `.24`
        and passes ten race runs on each backend. Full cgo/pure-Go/no-cgo checks and
        `just test-fork` / `just test-diff` pass; required gates include the fork contracts
        ([details](docs/websocket-lifecycle.md#acknowledgement-flush-ordering)).
  - [ ] Rerun `TestIntegrationReceiveInterrupt` on disposable accounts with both backends.
        No test-account directory is configured; the opt-in test skipped on 2026-10-07.
        Pending an arranged account/peer session; offline checks do not prove live acceptance.
- [x] Group sends enforce ACI identity trust even for members who already hold our sender key:
      fork `v0.2609.0-purego.26` (`ac8f355`) checks recipient eligibility and the exact key
      returned to envelope encryption. Untrusted peers use the pairwise path; a late
      identity refusal falls back before ciphertext submission. Fallback retains encryption
      mutex protection. Key rotation durably installs a fresh unshared distribution ID
      before retiring the old key, preventing stale retry authorization after failures.
      Downloaded Origin and all three changed fork files match the reviewed commit.
      Selection, real encrypted-envelope replacement/restoration, durable rotation/write
      failures and fallback locking regressions pass, including ten race runs per backend.
      Full cgo/pure-Go/no-cgo checks and `just test-fork` / `just test-diff` pass; required
      gates include the trust regressions and the updated encrypted endorsement fixture.
      Offline fixture boundaries and unrun live delivery are recorded in
      [docs/maintenance.md](docs/maintenance.md).
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

- signalmeow drops null messages and the destination number of sync transcripts.
- Received stories are retained as inbox events, without automatic expiry removal or audience
  projections. Story media downloads are currently CLI-only.
- Ordinary attachments are held in memory (up to 100 MiB); failed downloads can't be retried later; no
  thumbnails, blurhash or voice-note flags on send or receive.
- The storage service is always fetched in full; contact avatars and blocked groups aren't
  handled; group blocking isn't supported. Private story audiences are fetched on demand,
  without list creation/editing; PNI audience entries and custom exclusion lists are unsupported.
- Sticker installation is local; phone installation-state sync, pack uploads and uninstall
  commands aren't supported. Pack listing currently loads cached images along with metadata.
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

| Risk                                                         | Mitigation                                                                                                                                            |
| ------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| Signal server/protocol changes break us                      | Track mautrix-signal releases; the facade limits how far changes spread; `docs/maintenance.md` bump procedure                                         |
| Two forks drift from upstream (libsignal-go, mautrix-signal) | Bounded shim/join/logging scope; backend parity and offline wire tests; harness pinned to our libsignal tag                                           |
| Bugs in the pure-Go crypto ports (zkgroup, attestation)      | Vectors, interop, differential tests, fuzzing, cgo backend kept as reference; external review deferred (Phase 15; scope in `docs/security-review.md`) |
| libsignal drift between libsignalgo and the cgo `.a`         | Submodule pinned to the tag libsignalgo expects; `just check-libsignal` fails on mismatch                                                             |
| Linked devices get unlinked after ~30 days offline           | Documented; run `receive` periodically (systemd timer in `contrib/systemd/`)                                                                          |
| Signal ToS / unofficial client                               | Same position as signal-cli. Document it and don't spam.                                                                                              |
| Prompt injection via incoming messages (MCP, hooks)          | Default-deny recipient allowlist, `--read-only`, attach-dir restriction, no automatic read receipts, `--hook-from` required                           |
