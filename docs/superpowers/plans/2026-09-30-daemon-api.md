# Daemon API Implementation Plan

**Goal:** Give local scripts and bots an authenticated HTTP JSON and SSE API over one account's existing use cases and persistent inbox.

**Authority:** The user approved the daemon plan in chat and requested implementation, preserving parallel subagents where independent work is useful.

**Architecture:** Add `internal/daemon`, independent of MCP. `daemon serve` owns one connected client and its account lock. Reuse `app.App`, `app.Inbox`, `output.NewInboxEntryJSON`, and `output.NewSendJSON`; no new Signal facade methods or database schema.

## Global constraints

- Loopback HTTP only, every route requires a bearer token of at least 16 characters. Constant-time comparison and cross-origin protection; no query-string authentication.
- Configuration under `daemon.*` / `GOSIGNAL_DAEMON_*`, flag > environment > config. Required `--listen`; token-file overrides token config/environment. No token CLI argument.
- Text sends only, to users, `group:<id>` or self, using existing recipient and mention behavior. Default explicit empty send allowlist; `*` enables all. `--read-only` blocks sending and mark-read. The send allowlist does not restrict read receipts.
- Retention: 30 days / 10,000 entries, zero disables each bound. Existing pruning semantics and acknowledgement-before-store limitations remain; promise only retained, successfully stored event replay.
- Preserve write outcomes on errors. No automatic retries or idempotency guarantees. No attachments, rich sending, Unix socket, hooks, CLI-wide parity or group/profile live verification.
- `_test` packages, signaltest fake, no production calls. Coordinator owns shared records, formatting, commits, push and PR.

## Task 1: Daemon server, JSON endpoints and SSE

**Files:** Create `internal/daemon/` with server, HTTP/JSON handlers, streaming handlers and external-package tests split by responsibility. Do not edit cmd/, internal/app/, internal/signal/, docs/ or PLAN.md.

**Produces:**

```go
type Options struct {
    Version string
    Logger *slog.Logger
    InboxMaxAge time.Duration
    InboxMaxCount int
    ReadOnly bool
}
func ServeHTTP(ctx context.Context, a *app.App, events <-chan signal.Event,
    opts Options, listener net.Listener, token string) error
```

- ServeHTTP owns the listener, not the Signal client. Validate token length and bound loopback address before receiving. Command owner connects/closes client. App send allowlist is installed by the command.
- GET `/v1/health`: `{version,account,connection}` where account is selected ACI and connection includes state, since, lastEvent and optional error; no health-triggered network probe.
- GET `/v1/messages?chat=&cursor=&since=&limit=`: `{messages:[],cursor,more}`. Chat uses ResolveChat; since RFC3339; omitted limit 50, supplied limits 1..200. Existing newest-page default, chronological results, forward paging with cursor or since. Cursor a nonnegative int64 string; zero starts retained history.
- GET `/v1/events?chat=&cursor=`: Last-Event-ID overrides query cursor. Missing cursor snapshots the unfiltered tail. Validate query and initial DB read before SSE headers. Emit `ready` with starting cursor as id and `{cursor}` data, then `inbox` with entry ID and InboxEntryJSON data. Use Wait pages of 50, 15-second heartbeats, refreshed 10-second write/flush deadlines; no subscriber queues. Content-Type text/event-stream, Cache-Control no-cache. Errors after headers log and terminate.
- POST `/v1/messages`: strict `{recipients:[],text}` input; output `{ok,send,error?}` with output.SendJSON. HTTP 200 once per-target outcomes exist, even ErrSendFailed. Preserve every outcome and do not retry.
- POST `/v1/mark-read`: optional `{chat,cursor}`; empty selects all chats/all entries. Return `{ok,messages,senders,error?}`, retaining partial counts on error. Reading and streaming never send receipts.
- Errors `{error:{code,message}}`: invalid_request 400, unauthorized 401, forbidden 403, not_found 404, method_not_allowed 405, request_too_large 413, unsupported_media_type 415, unavailable 503, internal_error 500. Completed write error codes send_failed and mark_read_failed at HTTP 200. JSON bodies max 1 MiB, require one object and reject unknown fields. Authentication before route execution, JSON errors including unknown route/method and cross-origin rejection.
- Run receiver and HTTP concurrently. Any receiver termination, including nil/closed channel, cancels HTTP; HTTP termination cancels receiver. Join both; preserve receive/unlink error over normal cancellation. Base request contexts derive from server context. Five-second Shutdown then Close, no global SSE write timeout. Server returns normally on requested cancellation.
- Tests first: auth, cross-origin, malformed/oversized JSON, policy/read-only, invalid queries, paging/filtering, partial sends/read receipts without retry, health, ready/backlog/reconnect/Last-Event-ID, multiple readers, heartbeat and slow/disconnected writer isolation, cancellation with open SSE, normal receiver closure, fatal storage/unlink. Use actual ephemeral loopback listeners and meaningful boundaries.
- Scoped verification: `GOCACHE=/tmp/go-signal-daemon-cache/go-build CGO_ENABLED=0 go test -tags libsignal_go -count=1 ./internal/daemon` and cgo race tests with `CGO_LDFLAGS='-L /home/christian/Code/go-signal/third_party/lib'`.

## Task 2: Disk persistence acceptance

**Files:** Only `internal/signal/meow_inbox_test.go`, or a new adjacent external-package test with `cgo || libsignal_go` tag. No production changes or other packages.

- Read existing seeded-account and inbox fixtures. Add an offline real SQLite close/reopen acceptance test proving stored events and IDs survive closing the client, replay after an old cursor, pruning and new IDs after reopen. No Connect/production calls. Inspection showed offline inbox operations do not acquire the account lock; command lifecycle tests cover lock release and reacquisition instead.
- This is independent of Task 1: it consumes existing facade/store only and changes disjoint files.
- Scoped verification on both backends: `CGO_ENABLED=0 go test -tags libsignal_go -count=1 -run 'TestInbox' ./internal/signal` and `CGO_LDFLAGS='-L /home/christian/Code/go-signal/third_party/lib' go test -race -count=1 -run 'TestInbox' ./internal/signal` with writable GOCACHE.

## Task 3: CLI and configuration

**Files:** `cmd/daemon.go`, external-package cmd daemon tests, root command registration, necessary help goldens only. Start after Task 1 API is present. No internal/daemon edits or plan/docs changes.

- Build noun-verb `daemon serve`, no globals/init. Reuse root client opener, account flags, app options, signal handling and closeClient. Add root description/help support.
- Required nonempty listen address: loopback literal or localhost only, validate before opening client, bind TCP and confirm resolved listener IP is loopback. Token-file (trimmed) overrides daemon.token; min 16. Bind all daemon flags including retention to Viper; account/data-dir stay global. Add read-only and allow-recipient options; negative retention fails before client open. No confirmation/attachment/hook options.
- Open/connect once, build app with parsed allowlist, call Task 1 ServeHTTP, defer Close. Log ready/listening URL to stderr, stdout remains empty. Shutdown returns normal; terminal unlink maps to exit 3.
- Tests first: help and constructor shape, invalid/missing listen/token/allowlist/retention before account open, env/config/flag precedence and token-file priority, account selection and existing lock, actual authenticated HTTP use, read-only/send allowlist, cancellation/unlink and lock release. Reuse existing cmd testing patterns.
- Scoped verification: pure-Go `go test -tags libsignal_go ./cmd` and cgo `go test -race ./cmd` using cache/library environment above.

## Task 4: Integration, documentation, verification and delivery

**Owner:** Coordinator. May write docs while independent implementers work; review APIs against approved contract before publishing.

- Neutralize shared allowlist error guidance, retaining sentinel and semantics; add regression coverage as needed.
- Prerequisite discovered during implementation: synchronize outgoing timestamp allocation within one App so separate same-millisecond calls cannot share Signal message identity. Preserve a single timestamp across each call's targets and the first call's normal timestamp. Add deterministic sequential/concurrent/clock-rollback tests; no public API or schema change.
- Review prerequisite: explicit numeric zero in MarkReadRequest.Cursor is an upper bound selecting no entries, while omitted cursor selects all. Guard zero in app.Inbox.MarkRead before the store's unbounded Until=0 sentinel; add app and HTTP zero/positive-cutoff regression tests. No store schema/interface changes.
- Add `docs/daemon.md`, README usage link and a standalone script example: token-file start, curl send/list/mark-read, SSE with saved Last-Event-ID and duplicate handling. Document constraints, retention/replay limits, partial outcomes/uncertain response loss, explicit read receipts, lock exclusivity and graceful shutdown.
- Task review for each deliverable, then independent whole-branch review. Resume original implementers for corrections. Coordinator inspects all changes and runs targeted acceptance tests.
- Baseline and final `just check` and `just check-purego`, writable caches; `just fmt` before final checks. Record infrastructure limitations separately from test failures. Never claim unrun checks passed.
- Mark daemon children and parent in PLAN.md only after accepted implementation/docs/reconnect validation. Leave pending group/profile acceptance open.
- Save user-approved implementation decisions here; maintain temporary task progress ledger. Worktree `/tmp/go-signal-daemon-api`, branch `feat/daemon-api`, base `e3f14b8`; original profile branch and changed reference submodule remain untouched. Check existing PRs and base history; commit scoped changes and open separate PR after successful checks, without merging.
