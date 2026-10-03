# Group Invitation Acceptance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `groups accept <group>` for the selected account's existing ACI or PNI invitation, with verified no-ops and one acceptance attempt.

**Architecture:** Add an uncached full-state reader and dedicated acceptance primitive to the pinned mautrix fork, then expose them through an owned facade result and account-aware fake. Application and CLI code resolve existing group references and print a dedicated additive result. All mutation errors retain acceptance/uncertainty evidence without retrying.

**Tech Stack:** Go 1.26, signalmeow fork, libsignalgo cgo / `libsignal_go`, SQLite, Cobra, Viper, slog and existing plain/JSON printers.

**Spec:** [Approved design](../specs/2026-10-03-group-accept-design.md).

## Global Constraints

- Baseline `7f7247b`; main worktree `/tmp/go-signal-group-accept`, branch `feat/group-accept`.
- Starting mautrix pin `v0.2609.0-purego.11`, commit `2a6b959`; use a separate fork worktree `/tmp/mautrix-signal-group-accept` prepared by the controller.
- At most one acceptance PATCH per invocation; no automatic conflict/transport retry or submission through generic `UpdateGroup`.
- Fixed authenticated `GET /v2/groups/` and `PATCH /v2/groups/`, without a link password/query; response limit 1 MiB with limit+1 overflow detection.
- Validate numeric, nonoverflowing `X-Signal-Timestamp` for GET; PATCH does not require the header; no redirects, body replay or secret logging.
- Real facade build tag `cgo || libsignal_go`; fallback `!cgo && !libsignal_go`; preserve both crypto backends and existing upstream/libsignal pins.
- `GroupAcceptResult` fields: `ID`, `Title`, `Revision`, `Changed`, `Accepted`, `Verified`; `Changed` follows HTTP acceptance, while facade `Verified` means fresh own ACI membership.
- JSON envelope `{"version":1,"groupAccept":{...}}`; six lower-camel-case fields always present; `output.SchemaVersion` remains 1.
- Plain headings exactly `Invitation accepted` and `Already a member`; quoted title; no result stdout on operation error.
- Reference input maximum 4096 bytes before trimming; exactly one positional argument; reject blank, malformed explicit `group:` and Signal HTTPS/sgnl invite URLs before account opening.
- Accept only invitations already known to the selected account; do not import/store an unknown key, guess identity from a number, or mutate another account's invitation.
- ACI invitation wins over PNI fallback; full own ACI membership is a verified no-op; PNI selection remains local to acceptance.
- Read-only `reference/signal-cli`; preserve the original checkout's unrelated submodule change. No production Signal calls or live mutations in this batch.
- Cancellation, PNI invitation decline, global PNI self-membership reporting, other-user PNI mutation, MCP/daemon tools and live acceptance remain open.
- Controller owns shared records, dependency pins, history and shipping; workers do not stage, commit, push, edit plans or run repository-wide formatters.
- Publish reviewed fork commits normally to `purego` with a fresh immutable tag; no force push, moving tags or go.mod `replace`; create one integrated go-signal PR.

## Review Focus

- A stored key/ID alias resolves to the wrong group: refuse before HTTP and never print an unclassified master key (Task 2, `TestGroupAcceptStoredKeyBinding`).
- A phone accepts after an earlier failed follow-up: the next fresh call must be a no-op, with no second credential fetch/PATCH (Task 2, `TestGroupAcceptRepeatAfterAcceptedFailure`).
- Own PNI UUID equals someone else's ACI UUID: typed matching must select only the actual account invitation (Task 2, `TestFakeGroupAcceptTypedAccountIsolation`).
- An invited read has no endorsements but malformed nested wire data: return an ordinary error without cache/recipient writes or cgo panic output (Task 1, `TestGroupAcceptanceReadMalformed`).
- Cobra flag/configuration errors contain the master key before or after the subcommand: stderr/Close logs must be redacted while identity/exit codes and one setup call survive (Task 3, `TestGroupsAcceptSecretBoundaries`).

---

## Execution and environment

The user chose useful subagents and approved sequential implementation/review in
the design. Execute Tasks 1–3 sequentially, reviewing each before continuing;
these consume earlier APIs and touch shared Go packages. The controller performs
the publication/pin checkpoint after Task 1 and integrated verification/docs after
Task 3. Read both this plan and the spec in each worker's fresh context.

Set this known working environment for Go/just commands; caches and compiler/just
temporary files are in ignored workspace directories to avoid `/tmp` quota issues:

```sh
export TMPDIR=/home/christian/Code/go-signal/bin/.group-links-tmp
export GOTMPDIR="$TMPDIR" JUST_TEMPDIR="$TMPDIR" XDG_RUNTIME_DIR="$TMPDIR"
export XDG_CACHE_HOME=/home/christian/Code/go-signal/bin/.group-links-cache
export GOCACHE=/home/christian/Code/go-signal/bin/.group-links-go-cache
export GOTOOLCHAIN=local
```

Direct cgo commands additionally use
`CGO_LDFLAGS='-L /home/christian/Code/go-signal/third_party/lib'`.
Offline httptest fixtures may need sandbox-approved local loopback access.
Never run two golangci-lint processes concurrently. Use `GOFLAGS=-buildvcs=false`
for builds/docs and fork stubgen if the known spurious `/tmp/.git` breaks Go VCS
stamping; preserve justfile version metadata and document the workaround.

Baseline `just check` passed in the main feature worktree before code edits:
formatting unchanged, lint 0 issues, libsignal `v0.102.2`, full cgo race suite,
tidy go.mod/go.sum. Ignored archive links use the existing local library; the
isolated libsignal submodule is exactly `8fc2113`.
Prepare the fork from the published pin after checking current branch/tag state,
read its guidance, and run the existing pure-Go signalmeow/libsignalgo suite once
as its baseline. Record inherited lint/CI debt separately from new failures.

### Task 1: Bounded fork invitation reads and single-attempt acceptance

**Files (fork worktree):**

- Create: `pkg/signalmeow/groups_accept.go` — acceptance request/response policy.
- Create: `pkg/signalmeow/groups_accept_read.go` — strict uncached full-state reader.
- Create: `pkg/signalmeow/groups_accept_test.go`, `pkg/signalmeow/groups_accept_read_test.go`, `pkg/signalmeow/groups_accept_export_test.go` — external tests and narrow test adapters.
- Create if factoring transport: `pkg/signalmeow/groups_membership_http.go` and its `_test.go`.
- Modify only as needed: `pkg/signalmeow/groups_join_http.go` — reuse the bounded transport while preserving join endpoints/errors; `groups.go` — narrowly correct stale PNI comments or extract a strict read helper.
- Controller-owned associated docs: `pkg/libsignalgo/PUREGO.md`; CI coverage already includes `./pkg/signalmeow/...`, so no new workflow is required.

**Interfaces:**

- Consumes `.11` helpers for group authorization, own expiring profile credentials,
  presentation/ID decoding, signed-response field/length checks, configured HTTP
  transport and opt-in sensitive websocket contexts.
- Produces exact APIs:

```go
type GroupInvitationAcceptOutcome struct {
    Attempted bool
    Accepted bool
    Verified bool // signed acceptance only; caller verifies full membership
    Revision uint32
    GroupContext *signalpb.GroupContextV2
    Change *GroupChange
}
func (cli *Client) FetchGroupForAcceptance(
    ctx context.Context, key types.SerializedGroupMasterKey,
) (*Group, error)
func (cli *Client) AcceptGroupInvitationOnce(
    ctx context.Context, key types.SerializedGroupMasterKey,
    revision uint32, invited libsignalgo.ServiceID,
) (GroupInvitationAcceptOutcome, error)
```

- Produces `ErrGroupAcceptanceInvalid`, `ErrGroupAcceptanceUncertain` and
  `ErrGroupAcceptanceTerminated`; explicit status rejections retain existing
  authorization/conflict/not-found/rate-limit sentinel identities. GET failures
  never acquire `ErrGroupAcceptanceUncertain`.
- Test adapters inject authorization, own ACI/PNI, credential and test public
  parameters into these production flows; they do not replace production crypto
  or change production server constants.

- [x] **Step 1: Write failing strict-read tests.** `TestGroupAcceptanceRead`
      serves a bound encrypted full group with ACI and PNI invitations, no endorsements,
      fresh revision 7, title `Test group`; assert exact GET path, fresh owned result,
      both identity types retained and zero cache/profile-key/storage writes.
      `TestGroupAcceptanceReadMalformed` tables cover nil group/nested pending member,
      invalid public parameters, short service/profile ciphertexts, malformed
      presentation, corrupt required attributes, malformed nonempty invite password,
      bad/missing/overflow timestamp, 403, terminated, oversized/empty/read-failed body.
      Assert ordinary errors, closed bodies, preserved sentinels and no panic/secret
      diagnostics on either backend. Unknown unrelated full-state metadata may survive.

- [x] **Step 2: Run the read tests RED.**
      `CGO_ENABLED=0 go test -tags libsignal_go -count=1 -run '^TestGroupAcceptanceRead' ./pkg/signalmeow/`
      must fail for missing API/behavior, not fixture setup. Save the discriminating
      failure tail in the controller's execution record.

- [x] **Step 3: Implement the uncached reader.** Add `FetchGroupForAcceptance`
      in `groups_accept_read.go` with the spec's key/public-parameter binding, strict
      nested/length/decryption guards and owned data. Add/factor only the static
      acceptance GET path in the bounded transport. Avoid `parseGroupResponse`,
      `GroupCache.Put`, recipient-store writes and silent invalid-pending filtering.
      Declare the shared acceptance sentinels in `groups_accept.go` at this point;
      the mutation method/outcome implementation follows the mutation RED tests.

- [x] **Step 4: Run strict-read tests GREEN on both backends.** Repeat Step 2;
      then run `CGO_ENABLED=1 go test -race -count=1 -run '^TestGroupAcceptanceRead' ./pkg/signalmeow/`
      with the explicit cgo library path. Expected: all pass, no stderr panic output.

- [x] **Step 5: Write failing acceptance wire/outcome tests.** Reuse private
      join credential/notary fixtures and sign adversarial responses with those keys.
      `TestGroupAcceptanceActions` asserts own ACI credential lookup, fresh+1,
      plaintext invited source, no request group ID, exactly one presentation-only
      ACI or PNI promotion, bare `/v2/groups/` PATCH and no unrelated fields.
      `TestGroupAcceptanceResponseBinding` covers both allowed PNI response sources,
      exact invited PNI/own ACI/profile binding, ACI presentation/normalized form,
      wrong ID/revision/source/profile/PNI/type, signature/epoch >7, unknown/unrelated
      fields, empty/multiple/wrong-kind promotion, short ciphertext and byte ownership.
      `TestGroupAcceptanceNoRetry` and `TestGroupAcceptanceHTTP` cover credential/auth
      failure, wrong own credential, nil/missing account, foreign invited identity,
      revision overflow, context cancellation, redirects/replay, all explicit refusal
      statuses, transport/5xx uncertainty and accepted body/signature failures.
      Pin this core assertion for a 200 followed by read failure:

```go
if !outcome.Attempted || !outcome.Accepted || outcome.Verified ||
    outcome.GroupContext != nil || outcome.Change != nil || patchCalls != 1 {
    t.Fatalf("invalid accepted partial outcome: %+v; calls=%d", outcome, patchCalls)
}
```

- [x] **Step 6: Run acceptance tests RED.**
      `CGO_ENABLED=0 go test -tags libsignal_go -count=1 -run '^TestGroupAcceptance' ./pkg/signalmeow/`
      must fail for missing acceptance behavior; record the reason.

- [x] **Step 7: Implement acceptance and response verification.** Put the API,
      outcome and sentinels in `groups_accept.go`. Prepare only the own credential
      presentation and selected promotion; perform one PATCH. Record HTTP acceptance
      before reading; signed verification binds exact group/revision/typed identities,
      complete identity representations, cardinality and allowed fields. Return copied
      context/change only after verification, without generic mutation/decrypt/cache
      side effects. Keep all contextual dependency/WS logging sensitive and errors
      redacted with `Unwrap` identity.

- [x] **Step 8: Run acceptance and existing join/privacy tests GREEN.**
      `CGO_ENABLED=0 go test -tags libsignal_go -count=1 ./pkg/signalmeow/... ./pkg/libsignalgo/...`
      and `CGO_ENABLED=1 go test -race -count=1 -run 'TestGroup(Acceptance|Join)|TestWebsocketCredentialLoggingPrivacy' ./pkg/signalmeow/...`
      must pass, including the actual websocket loop logging regressions. Also run
      `CGO_ENABLED=0 go vet -tags libsignal_go ./pkg/...` and
      `CGO_ENABLED=0 GOFLAGS=-buildvcs=false go run ./pkg/libsignalgo/internal/stubgen -check`.
      Format only touched files; verify new
      paths against fork lint/hooks without reformatting unrelated inherited files.

- [x] **Step 9: Review and commit the fork deliverable (controller).** Inspect
      allowed changed paths, acceptance evidence and both spec/quality review results.
      Resume the same implementer for findings. Run
      `CGO_ENABLED=0 GOFLAGS=-buildvcs=false go run ./pkg/libsignalgo/internal/stubgen -gen`
      and `git diff --exit-code -- pkg/libsignalgo` as controller; generated files
      must remain unchanged. After scoped checks pass, commit only
      reviewed changes as `feat(signalmeow): accept group invitations once`.

- [x] **Step 10: Publish and pin the reviewed fork (controller).** Document the
      bounded extension in `PUREGO.md`, check `purego`/remote history and next unused
      tag, publish by normal fast-forward and fresh immutable tag, inspect CI and
      download that exact module. Confirm published changed files match reviewed
      source; update only mautrix pin/go.sum without `replace` or other pin changes.
      Run main `just fmt` and `just lint`, then commit as
      `build: pin reviewed invitation acceptance fork`. New failures stop shipping;
      unchanged inherited fork lint debt is documented against baseline evidence.

### Task 2: Owned acceptance policy, facade and account-aware fake

**Files (main feature worktree):**

- Create: `internal/signal/groups_accept.go`, `internal/signal/groups_accept_test.go` — owned result, selection/error policy.
- Create: `internal/signal/meow_groups_accept.go`, `internal/signal/meow_groups_accept_test.go`, `internal/signal/groups_accept_export_test.go` — protected state flow and test adapters.
- Create: `internal/signal/signaltest/groups_accept.go`, `internal/signal/signaltest/groups_accept_test.go` — fixture mutation/account isolation.
- Modify: `internal/signal/client.go`, `internal/signal/signaltest/fake.go` — contract and error injections. Verify unchanged `internal/signal/meow_nocgo.go`: `Open` returns nil and `ErrCGORequired`; no fallback client type/method exists.
- Modify only if needed: `internal/signal/signaltest/groups.go`, `internal/signal/signaltest/groups_join.go` — reuse account-local fixture resolution/cache without changing legacy policy.
- Correct narrowly: obsolete PNI-skipping comment in `internal/signal/groups.go`;
  relocate the unchanged private `errInvalidGroupChangeResponse` declaration there
  from `internal/signal/meow_groups_remove.go`, making the same error available to
  backend-independent policy. Only remove that declaration/unused import from
  the removal file; its mutation behavior is outside this task.

**Interfaces:**

- Consumes the three fork APIs/sentinels from Task 1 and existing protected
  lifecycle, known-key resolution, account title store and `SendGroupUpdate`.
- Produces `Client.AcceptGroupInvitation(context.Context, string) (GroupAcceptResult, error)`.
- Produces `GroupAcceptResult` with the six spec fields; public
  `ErrGroupInvitationNotFound`; `GroupAcceptOperationError(error, GroupAcceptResult) error`
  for secret-safe cause-preserving operation guidance.
- Produces `(Group).CheckAcceptInvitation(self Recipient) (Recipient, bool, error)`:
  return exact invited typed recipient, already-full-member flag, or policy error.
  Full member takes the no-op branch before overflow/stale-invitation checks;
  mutation selection enforces ACI-first, PNI fallback, duplicate/role/request/ban
  refusals and nonoverflowing revision. Do not modify `MembershipOf` semantics.
- Map fork read authorization/not-found to `ErrNotAMember`/`ErrUnknownGroup`,
  acceptance invalid to the unchanged private invalid-response error, terminated
  to `ErrGroupTerminated`, conflict to `ErrGroupChanged` and uncertain PATCH to
  `ErrGroupUpdateUncertain`, preserving original cause identity. No inactive-link
  claim is made for a password-free full-state failure.
- Produces fake error hooks `AcceptGroupInvitationErr` (pre-acceptance) and
  `GroupAcceptFollowUpErr` (accepted verification failure), reusing
  `GroupJoinServer`, `GroupJoinKnownKeys`, `GroupJoinTitleCache` for account-local
  server/key/title fixtures; legacy `GroupInfo`/`GroupKeys` remain supported.
- Test adapter `GroupAcceptOperations` aliases the internal operations struct
  with `Resolve(ctx, ref) (types.GroupIdentifier, types.SerializedGroupMasterKey, error)`,
  `Invalidate(gid)`, `Fetch(ctx, key) (*signalmeow.Group, error)`,
  `Accept(ctx, key, revision, invited) (signalmeow.GroupInvitationAcceptOutcome, error)`,
  `Cache(ctx, Group) error`, and the existing join-compatible `Notify` signature.
  `AcceptGroupWithOperations(ctx context.Context, ops GroupAcceptOperations, self Recipient, ref string) (GroupAcceptResult, error)` exposes orchestration
  only in `groups_accept_export_test.go` (`cgo || libsignal_go`).

- [x] **Step 1: Write failing selection/error tests.** `TestCheckAcceptInvitation`
      tables cover ACI and PNI, same UUID/different type, zero own PNI, foreign
      invitations, ACI priority, duplicate selected identity, unknown role, own
      requester, simultaneous own request/invitation, bans and revision overflow.
      `TestGroupAcceptResultErrors` covers accepted, attempted uncertainty, conflict,
      no invitation, terminated, canceled/unlinked and arbitrary secret nested errors.
      Verify no input/master key in text and `errors.Is` for the original causes.

- [x] **Step 2: Run policy tests RED.**
      `CGO_ENABLED=0 go test -count=1 -run 'TestCheckAcceptInvitation|TestGroupAcceptResultErrors' ./internal/signal/`
      must fail for missing behavior.

- [x] **Step 3: Implement the owned policy/contract.** Add exact result/helper
      signatures in `groups_accept.go` and relocate the unchanged invalid-response
      sentinel to the backend-independent group file. Defer the Client interface
      addition and implementation methods to Steps 7–8, keeping policy GREEN runnable
      while the facade/fake methods do not yet exist.
      Redacted accepted/uncertain messages contain only validated canonical ID and
      revision with `groups show`/phone/admin inspection guidance, never nested text.

- [x] **Step 4: Run policy tests GREEN.** Repeat Step 2, requiring all cases to
      pass without libsignal or network.

- [x] **Step 5: Write failing real-flow and fake tests.** Expose a narrow
      operations adapter for protected resolution/read/submit/cache/notify. The
      harness pins cache invalidation, one fresh read before selection, one PATCH,
      signed gate, fresh >= accepted revision, cache then notify and final invalidation.
      `TestGroupAcceptStages`, `TestGroupAcceptNoop`, `TestGroupAcceptFailures`,
      `TestGroupAcceptStoredKeyBinding` and `TestGroupAcceptLifecycle` cover unknown
      account key, mismatched stored derived ID, malformed backend snapshots, no-op
      without credential/PATCH/notification, rejected/uncertain submission, bad signed
      response, concurrent removal, canceled follow-ups, cache failure preserving
      verified metadata, per-recipient failures, Close and unlink. Assert result:

```go
if !got.Accepted || !got.Changed || !got.Verified || got.ID != groupID ||
    got.Revision != 9 || got.Title != "fresh title" || !errors.Is(err, cacheErr) {
    t.Fatalf("verification must survive cache failure: %+v, %v", got, err)
}
```

`TestFakeGroupAccept`, `TestFakeGroupAcceptTypedAccountIsolation`,
`TestFakeGroupAcceptKnownKeyAliases`, `TestFakeGroupAcceptFailures` and
`TestGroupAcceptRepeatAfterAcceptedFailure` pin ACI offered-role preservation,
PNI ordinary-member modeling, independent server snapshots, selected-account
PNI/known aliases, ignored global cache, clone ownership, failure atomicity,
accepted server state, cleared local left marker and reopen/repeat no-op.
A second account with a colliding ACI UUID must neither consume the first
account's PNI invitation nor gain its key/title/left cache through legacy aliases.

- [x] **Step 6: Run the new flow/fake tests RED.**
      `CGO_ENABLED=0 go test -tags libsignal_go -count=1 -run 'TestGroupAccept|TestFakeGroupAccept' ./internal/signal/...`
      must fail for incomplete state-flow/account behavior, not fixture initialization.

- [x] **Step 7: Implement protected real acceptance.** Add the operations-driven
      algorithm in `meow_groups_accept.go`. Validate local input, acquire closing/
      connection/operation guards before any connected-store access, then resolve and
      validate selected key/ID under that guard. Read through Task 1's uncached API,
      select by both own typed IDs and handle the no-op. Submit once; copy only
      validated signed artifacts; fresh-read membership before cache/notification.
      Set verification metadata before persistence, preserve accepted/attempted
      evidence and keep every backend stage inside the sensitive caller context.
      Add the interface together with the real method and Step 8's fake implementation
      before the next compile/GREEN gate. Keep the existing no-backend `Open` refusal
      unchanged; do not introduce an unused fallback client type.

- [x] **Step 8: Implement fake acceptance.** Add the interface method in
      `signaltest/groups_accept.go` using the owned policy and selected registered
      account PNI. Operate on cloned fixture state under the fake mutex; retain
      existing account-local key visibility, preserve unrelated data, apply exact
      invitation removal and committed revision once, isolate caches and distinguish
      submission/follow-up errors. Do not add acceptance keys for unknown accounts.

- [x] **Step 9: Run real/fake tests GREEN and scoped quality checks.** Repeat
      Step 6; run focused cgo race tests with the explicit library path, the complete
      fake suite and no-cgo fallback suite for `./internal/signal/...`. Run scoped
      vet and lint on both builds sequentially. Expected: no new failures; existing
      join, ordinary PNI pending conversion and ACI-only leave behavior remain green.

- [x] **Step 10: Review and commit the facade deliverable (controller).** Inspect
      every changed path and execute discriminating policy/account/partial-outcome
      checks independently. After task spec/quality approval, run `just fmt` and
      `just lint`; commit reviewed paths as `feat(signal): accept own group invitations`.

### Task 3: Application/CLI/output and integrated documentation

**Files (main feature worktree):**

- Create: `internal/app/groups_accept.go`, `internal/app/groups_accept_test.go`.
- Create: `internal/output/groups_accept.go`, `internal/output/groups_accept_test.go`.
- Create: `cmd/groups_accept.go`, `cmd/groups_accept_test.go`; modify registration in `cmd/groups.go`.
- Create goldens: `cmd/testdata/groups_accept.golden`, `groups_accept_json.golden`, `groups_accept_noop.golden`, `groups_accept_noop_json.golden`.
- Controller-associated docs: `README.md`, `docs/json.md`, `docs/dev.md`, `docs/maintenance.md`, `AGENTS.md`, `PLAN.md`, and this plan/spec status.

**Interfaces:**

- Consumes Task 2's facade, six-field result, fake fixtures and error boundary.
- Produces `GroupAcceptRequest{Group string}` with `(GroupAcceptRequest).Check() error`;
  `(*App).GroupsAccept(context.Context, GroupAcceptRequest) (signal.GroupAcceptResult, error)`;
  `(*Printer).GroupAccept(signal.GroupAcceptResult) error` and
  `newGroupsAcceptCmd(*clientOpener, *printerFactory) *cobra.Command`.
- Uses existing `ResolveGroup`, `connectSendOnly`, `SchemaVersion`, `WithClientFactory`
  and `WithLocation`; new command's help explains known invitations, both identity
  types, one attempt and follow-up inspection. No new global configuration hook.

- [x] **Step 1: Write failing application tests.** `TestGroupAcceptRequestCheck`
      rejects blank, 4097-byte-before-trim, malformed `group:` and HTTPS/sgnl invite
      inputs; valid title/group ID/key inputs survive. `TestGroupsAccept` covers
      selected account, title/ID/key resolution, fresh member no-op, ACI/PNI acceptance,
      and one send-only connect/one operation. `TestGroupsAcceptFailures` wraps a
      counting client to pin zero operation calls on resolution/connect failures,
      especially Connect errors carrying an uncertainty sentinel; preserves typed
      accepted/verified results, `errors.Is`, unlink and context, with secret-free text.

- [x] **Step 2: Run application tests RED.**
      `CGO_ENABLED=0 go test -tags libsignal_go -count=1 -run 'TestGroupAcceptRequest|TestGroupsAccept' ./internal/app/`
      must fail for missing request/use-case behavior.

- [x] **Step 3: Implement the request and use case.** Put exact signatures in
      `internal/app/groups_accept.go`: Check first, resolve before connecting, connect
      once, call once and return partial result/error. Give resolution/connect their
      own redacted pre-submission boundary; operation guidance uses Task 2's formatter.

- [x] **Step 4: Run application tests GREEN.** Repeat Step 2, requiring all
      account/preflight/partial-result cases to pass.

- [x] **Step 5: Write failing output and command tests.** `TestPrinterGroupAccept`
      checks both headings, quoted title and all six JSON fields under `version:1`.
      `TestGroupsAcceptCommand` uses selected-account fake invitations for ACI/PNI
      acceptance and unchanged full membership. New four goldens use revision 8 and
      all three flags true for acceptance; revision 7, false/false/true for no-op.
      `TestGroupsAcceptSecretBoundaries` checks argument/flag/config/open/Close/writer
      errors, flags before/after subcommand, arbitrary master-key-containing causes,
      exactly one inherited setup call, zero account open on invalid preflight, empty
      operation-error stdout and preserved ExitCode 3. No existing golden is rewritten.

- [x] **Step 6: Run output/command tests RED.**
      `CGO_ENABLED=0 go test -tags libsignal_go -count=1 -run 'TestPrinterGroupAccept|TestGroupsAccept' ./internal/output/ ./cmd/`
      must fail for missing renderer/command behavior.

- [x] **Step 7: Implement the renderer and scoped command.** Add the six-field
      `groupAccept` envelope with `version`, spec headings and existing write helpers.
      Register `groups accept <group>`, exactly one argument, scoped flag/setup/Close
      redaction boundaries and the request/use-case/renderer flow. Reuse join's setup
      pattern without changing root or sibling command behavior.

- [x] **Step 8: Run command/output tests GREEN.** Repeat Step 6 and run full
      affected app/output/cmd suites on pure Go; run focused cgo race cases, then vet
      and scoped lint sequentially. Manually inspect all four new goldens and confirm
      the existing files are unchanged. Return changed paths and test tails for review.

- [x] **Step 9: Review the task, document and verify integration (controller).**
      Complete task spec/quality review, inspect every task's diff, then document
      `groups accept` examples, known-key and PNI limitations, no-op/outcome semantics,
      `groupAccept` field table and fork extension maintenance policy. Correct the
      stale PNI-skipping note with verified conversion evidence; split PLAN's combined
      item into verified acceptance, offline checks, unchecked live acceptance and
      unchecked cancellation/PNI decline. Do not close the group-management parent.
      Add `docs/dev.md#group-invitation-live-check` for separately opted-in two-account
      ACI/PNI invitations, repeats, revoked/foreign invitations, conflict/failure
      inspection, notifications and phone/server verification on both backends.
      Independently run:

```sh
just fmt
just check
just check-purego
CGO_ENABLED=0 go test -count=1 ./...
GOFLAGS=-buildvcs=false just build
GOFLAGS=-buildvcs=false just docs-gen
git diff --check
```

Expected: formatting/lint clean; matching libsignal; all cgo race, pure-Go and
fallback packages pass; tidy unchanged; AES assembly selected on all six release
targets; build/help/man-page output includes `groups accept`. No production
requests. Relevant failures require diagnosis, correction and affected/full
integration reruns; do not claim a criterion from unrelated passing tests.

- [x] **Step 10: Commit verified command/docs deliverable (controller).** After
      the checks pass, stage only this task's reviewed paths and planning updates;
      commit as `feat(cmd): accept group invitations`. Preserve honest remaining
      live/deferred markers and spec/plan status.

## Final review and shipping gate

- [x] Request a fresh whole-change review of go-signal baseline..feature HEAD
      and published fork `.11`..new tag against this approved spec/plan, with actual
      test evidence and disclosed scope limits. Apply the selected execution skill's
      review/fix-round rules; keep factual findings and controller rulings recorded.
- [x] Resolve required findings; verify affected checks and full integration when
      fixes affect it. Complete planning evidence, inspect clean tracked state and
      dependency integrity. Existing production/live acceptance limitations remain
      explicit, with no unsupported completion claims.
- [ ] Inspect remote/branch PR state; push the feature normally and create one
      PR against main. Write the exact final description to a temporary file and use
      `gh pr create --body-file`; report implemented behavior and actual verification.
      No merge or force push. Check head/remote hashes and CI status; record PR URL
      and truthful pending/failing status in the plan.
- [ ] Report the PR, controller-verified acceptance, remaining cancellation/PNI
      decline/live work, and every required ruling. Retain worktrees/branch for review
      feedback and clean only owned execution scratch after durable records exist.

## Planning self-review

All spec sections map to Tasks 1–3 or the final gate. Contracts use the same
method/result names throughout; backend signed verification and facade membership
verification are distinct. Strict-read and private signed fixture tests cover
crypto/HTTP boundaries; typed two-account and repeat-after-failure tests cover
the review-focus cases; actual command tests cover privacy/configuration.

Two clarifications preserve repository behavior without expanding scope: JSON's
existing `version` envelope corrects the initial example label; the operation
guard must precede connected-store resolution so Close cannot close that store
during key access. The spec is updated to make that order explicit.
The private invalid-response sentinel is relocated without changing its identity
or text so that the same policy compiles under the no-backend/fake build.

Status: user-approved plan implemented and checked offline; whole-change review
approved; shipping pending. Live acceptance and deferred operations remain open.

## Offline execution evidence

Task 1 fork review approved spec compliance and quality; published commit
`44abf3b405db809cca9778332e0ddb93fb871b95` as immutable
`v0.2609.0-purego.12`. Downloaded module origin and all nine changed file bytes
matched the reviewed source. Main pin commit: `daf9dd3`. Fork private
acceptance/join cryptographic and transport fixtures passed on both backends;
API parity/stub generation passed without generated changes.

Task 2 review found malformed Signal URL preflight, fresh metadata loss on
cancellation and duplicated real/fake normalization. One worker fix round
reproduced the failures, corrected all three and passed scoped re-review;
controller regressions and lint passed. Facade commit: `8027ca1`.

Task 3 review approved spec compliance and quality with no findings. Controller
verified all eleven frozen command/App/output/golden hashes immediately before
committing the deliverable. Existing goldens remained byte-identical.

Controller integrated verification passed: `just check` (fmt, lint, matching
libsignal, full cgo race tests and unchanged tidy), `just check-purego` (vet, lint,
all pure-Go tests and AES assembly on six release targets),
`CGO_ENABLED=0 go test -count=1 ./...`, `GOFLAGS=-buildvcs=false just build`,
`GOFLAGS=-buildvcs=false just docs-gen`, command help/man-page inspection and
`git diff --check`. Associated docs were followed by `just fmt` and `just lint`
(0 issues), with the reviewed source hashes unchanged. No live Signal calls.

Published fork pure-Go CI passed for branch and tag; both Go backend test jobs
passed. The broad Go lint jobs failed on six inherited EOF/goimports findings
in unchanged libsignalgo files, confirmed byte-identical against `.11`. Baseline
local bridge builds also require unavailable olm headers/pure-Go SQLite support,
and staticcheck has sixteen identical inherited findings. These baseline limits
do not replace the passing affected-suite and main integration checks.

Task 1 deferred cache-wording Minor was triaged in final review and corrected
in the single final fix wave below; no finding remains open.

## Controller rulings (chronological)

1. Review frozen working-tree diff packages before controller commits, including
   untracked files and fingerprints. The plan gives workers no Git mutation
   authority, while the skill's range-only script requires commits first. Cost
   if wrong: a stale candidate could be committed without review. Workers freeze
   after reports, controller hashes are checked before commits, and final review
   uses actual whole-branch commit ranges.
2. Keep no-backend Open unchanged instead of adding an acceptance fallback
   method: it has no client type and already returns nil/ErrCGORequired before
   operations. Cost if wrong: acceptance would lack an operation-level fallback.
   Full no-backend tests and the existing command Open assertion passed.

## Final review and documentation fix

Final whole-change review of main `7f7247b..78a8402` and fork
`2a6b959..44abf3b` approved spec compliance and quality, ready to merge, with no
Critical or Important findings. It independently confirmed the deferred cache-
wording Minor in four API/comment descriptions. One final fix worker corrected
all four, with a single scoped re-review approving the complete fix and finding
no new breakage or outside observations. Controller verified frozen file hashes
and AST equality with comments omitted: executable source is unchanged.

Fork documentation fix commit `6bb5f56c9e9d6a1811fcf760f58a89be84f423a0` was
published by normal fast-forward as immutable `v0.2609.0-purego.13`; `.12` still
points to `44abf3b`. The main dependency pin advances only to that documentation
release. No functional implementation or live acceptance claim changed.

3. Adopt the final review's nine declined-to-judge dispositions against approved
   scope and evidence limits: production server/phone effects and actual PNI
   normalization/roles stay opt-in live gates; cancellation, PNI decline/global
   self recognition, other-user PNI mutations and MCP/daemon interfaces remain
   separate work. Ordinary reader policy is unchanged, with shared HTTP
   regressions reviewed. Inherited fork lint/bridge/staticcheck debt stays
   disclosed; no-backend handling follows ruling 2. Publication/CI must be
   assessed on actual later results, without merging. Cost if wrong: deferred
   defects or missing production/CI evidence could remain unaddressed. The
   explicit roadmap, live procedure and CI records preserve those limits.

Controller verified `.13` downloaded module origin `6bb5f56` and exact bytes
for all nine fork-changed files; the delta from `.12` contains only the three
reviewed comment/documentation paths. Only mautrix's version/checksums changed;
other pins and `replace` policy are unchanged. Repeated `go mod tidy` preserves
module files byte-for-byte. The first post-pin `just check` passed formatting,
lint, libsignal and all cgo race tests; its final tidy gate compares against
Git HEAD and flagged the intentionally uncommitted pin. Pin commit `6418874`
closes that comparison boundary before the final gate rerun.

Dependency `.13` pure-Go branch/tag CI passed (runs `37125208612` and
`37125208733`). Both Go test jobs passed on branch/tag (runs `37125208623` and
`37125208720`); their lint jobs retain the same six inherited formatting
findings, verified byte-identical to `.11`. No new dependency CI failure.

Final post-pin `just check` and `just check-purego` passed in full, including
unchanged tidy, cgo race tests, pure-Go vet/lint/tests and six-target AES assembly.
All review findings are resolved; implementation/dependency provenance is verified.
