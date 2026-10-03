# Group Join-request Cancellation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `groups cancel-request <group> --yes` with verified no-ops and one cancellation attempt.

**Architecture:** Add password-free preview and a strict signed single-attempt deletion to the pinned fork. Expose an owned facade result/account-aware fake, then the use case and additive command/output.

**Tech Stack:** Go 1.26, signalmeow fork, libsignalgo cgo / libsignal_go, SQLite, Cobra, Viper, slog, existing printers.

**Spec:** [Approved design](../specs/2026-10-03-group-cancel-request-design.md). User approved the complete in-chat plan and explicitly requested implementation; no additional artifact/execution approval is pending.

## Global Constraints

- Baseline e1de4e9; worktree `/tmp/go-signal-group-cancel-request`, branch feat/group-cancel-request.
- Fork baseline 6bb5f56 (v0.2609.0-purego.13); worktree `/tmp/mautrix-signal-group-cancel-request`.
- At most one PATCH, no generic UpdateGroup, automatic retry, full-state fetch or profile credential lookup.
- Fixed authenticated GET /v2/groups/join/ and PATCH /v2/groups/, no password or query; 1 MiB bounded bodies, numeric nonoverflowing GET timestamp, no redirects/replay/secret logging.
- Delete only encrypted selected-account own ACI request; source own ACI, fresh revision+1; verify exact signed group/revision/source/target/action and epoch <=7.
- Six result fields ID, Title, Revision, Changed, Accepted, Verified. Changed follows HTTP acceptance. Verified is exact signed removal at its revision, or fresh pending=false preview for a no-op.
- JSON envelope version 1 / groupCancelRequest; six lower-camel fields always present. Plain headings Join request cancelled / No pending join request; quoted title; no stdout result on operation failures.
- One group argument, --yes before account opening; 4096-byte pre-trim validation; known account-local keys only, no unknown key import.
- Preserve cgo || libsignal_go real facade, no-backend Open ErrCGORequired policy, errors.Is/As and exit codes.
- Invalidate group cache before/after; preserve keys and title/left records; preview is not cached full membership. No member notifications or linked-device sync.
- Controller owns shared records, pins, history and shipping; workers do not stage/commit/push/edit plans/run repository-wide formatters/spawn agents.
- Publish reviewed fork normally to purego with immutable v0.2609.0-purego.14, no replace/force push/tag movement/other dependency bumps; one go-signal PR, no merge.
- Read-only reference/signal-cli; preserve original unrelated changes. No production Signal calls. Live acceptance/PNI decline/global PNI/MCP/daemon remain open.

## Review Focus

- Wrong stored key/ID alias must fail before HTTP and expose no key (Task 2: TestGroupCancelRequestStoredKeyBinding).
- Administrator approval between preview and PATCH must not delete full membership or retry conflict (Task 1: TestGroupJoinRequestCancellationRace; Task 2: TestGroupCancelRequestSingleAttempt).
- Retry after accepted-but-unverified cancellation uses fresh server state and sends no second PATCH when pending=false (Task 2: TestFakeGroupCancelRequestRepeatAfterAcceptedFailure).
- Equal ACI/PNI UUIDs across accounts must never remove another identity's request (Task 2: TestFakeGroupCancelRequestAccountIsolation).
- Configuration/flag/writer/Close errors containing keys must remain redacted and preserve unlinked exit 3 (Task 3: TestGroupsCancelRequestSecretBoundaries).

## Environment and execution

Use sequential implementation and task review, then one broad final review. Controller records evidence, commits verified changes and handles the fork publication/pin after Task 1.

Environment for all Go/just checks is saved in this plan's SDD workspace env.sh:
TMPDIR/GOTMPDIR/JUST_TEMPDIR/XDG_RUNTIME_DIR=/home/christian/Code/go-signal/bin/.group-links-tmp;
XDG_CACHE_HOME=/home/christian/Code/go-signal/bin/.group-links-cache;
GOCACHE=/home/christian/Code/go-signal/bin/.group-links-go-cache; GOTOOLCHAIN=local.
Direct cgo CGO_LDFLAGS='-L /home/christian/Code/go-signal/third_party/lib'.
Loopback fixtures may require sandbox escalation. Never parallelize golangci-lint.
GOFLAGS=-buildvcs=false only when needed for builds/docs/stubgen due /tmp/.git stamping.

### Task 1: Password-free preview and signed one-shot cancellation

**Files:** Create fork pkg/signalmeow/groups_cancel_request.go and groups_cancel_request_test.go; modify groups_join.go only to share preview decoding and pkg/signalmeow/export_test.go for scoped test helpers; update fork pkg/libsignalgo/PUREGO.md for API/safety boundaries.

**Interfaces:** Produces Client.PreviewGroupJoinRequest(ctx context.Context, key types.SerializedGroupMasterKey) (GroupJoinPreview,error), Client.CancelGroupJoinRequestOnce(ctx context.Context,key types.SerializedGroupMasterKey,revision uint32) (GroupJoinRequestCancelOutcome,error). Outcome fields Attempted,Accepted,Verified bool; Revision uint32; GroupContext *signalpb.GroupContextV2; Change *GroupChange. Export ErrGroupCancellationInvalid, ErrGroupCancellationUncertain, ErrGroupCancellationTerminated.

- [x] Write TestGroupJoinRequestPreview for exact password-free GET, authenticated public-key binding, timestamp/body/attribute validation, pending true/false and disabled access; run RED on missing API.
- [x] Implement narrow preview using shared decoder; preserve existing password-required invite-link API behavior and normal auth credential caching.
- [x] Write TestGroupJoinRequestCancellationWire/ResponseBinding/Race/Failures: PATCH source own ACI, sole encrypted own deletion, no profile fetch/full GET/retry; test revision overflow, signed wrong signature/group/revision/source/target/epoch/extra fields/malformed lengths, conflict/403/404/423/429, transport uncertainty, HTTP200 invalid/unreadable/empty/oversized response, cancellation, redirects/body closing/log redaction. Run RED.
- [x] Implement one-shot cancellation with exact signed gate and owned artifacts, cancellation-specific sentinels, strict key/account validation and existing bounded membership HTTP helper. Group-state cache/store untouched, no notification.
- [x] Run GREEN and existing join/acceptance regressions: CGO_ENABLED=0 go test -tags libsignal_go -count=1 -run 'TestGroup(Join|Acceptance)' ./pkg/signalmeow/; cgo equivalent with -race. Run pure-Go signalmeow/libsignalgo suites once; disclose inherited broad bridge/lint limitations separately. Gofmt only changed Go files.
- [x] Self-review and write report to assigned path with RED/GREEN commands/output and changed files. Controller commits reviewed/verified task and publishes .14, then changes only mautrix pin/go.sum in main (no replace).

### Task 2: Cancellation facade and account-aware fake

**Files:** Create internal/signal/groups_cancel_request.go, meow_groups_cancel_request.go, meow_groups_cancel_request_test.go, groups_cancel_request_test.go, signaltest/groups_cancel_request.go and scoped fake tests; modify internal/signal/client.go, signaltest/fake.go and export_internal_test.go to expose test operation hooks where needed.

**Interfaces:** Consumes Task 1 APIs/sentinels and existing acceptance reference validation/key resolution/lifecycle/error patterns. Produces Client.CancelGroupJoinRequest(ctx context.Context,ref string) (GroupCancelRequestResult,error), GroupCancelRequestResult{ID,Title string; Revision uint32; Changed,Accepted,Verified bool}, GroupCancelRequestOperationError(err error,result GroupCancelRequestResult) error. Reference check/normalization must reuse existing acceptance behavior rather than duplicate the parser; an appropriately named delegating cancellation helper may wrap it. Fake uses account-local known keys/server state.

- [x] Write policy/result RED tests for secret-free accepted/uncertain/definite outcomes and errors.Is/As; implement owned result and safe operation wrapper without dependency types escaping.
- [x] Write TestGroupCancelRequestStoredKeyBinding/SingleAttempt/Lifecycle/Noop/PartialResults: connect/Close/lost context guards; selected-account known key+ID binding; fresh password-free preview, no-op only pending=false; errors before PATCH retain no acceptance; valid signed gate establishes verification, preserves preview title and attempted/accepted revision; no post-GET/full fetch/cache writes/notifications; final invalidation and canceled accepted outcomes. Run RED.
- [x] Implement facade via a small operations seam using guarded group client, sensitive logger policy and known reference resolution. Never mark left or replace title cache with preview data; preserve IDs only after key binding.
- [x] Write account-aware fake RED tests including TestFakeGroupCancelRequestAccountIsolation and RepeatAfterAcceptedFailure; implement exact self-ACI requesting removal, no-op false/false/true, owned committed server state on accepted failures, lifecycle checks, fake error hooks and no mutation of members/invitations/PNI requests/title/left records.
- [x] Run GREEN: CGO_ENABLED=0 go test -tags libsignal_go -count=1 -run 'Test.*GroupCancelRequest' ./internal/signal/...; cgo same with -race; shared no-backend policy tests and existing join/acceptance/leave regressions. Gofmt changed files, scoped lint as needed.
- [x] Self-review/report. Controller runs full main lint before committing; task reviewer checks actual committed range before Task 3.

### Task 3: Use case, CLI, output and integrated docs

**Files:** Create internal/app/groups_cancel_request.go and tests; cmd/groups_cancel_request.go and tests; internal/output/groups_cancel_request.go and tests; four cmd/testdata/groups_cancel_request{,_json,_noop,_noop_json}.golden. Modify cmd/groups.go for registration and leave help; internal/output printer interface/JSON mappings following acceptance; README.md, docs/json.md, docs/dev.md, docs/maintenance.md for command/contracts/live procedure/fork provenance. Controller updates PLAN.md and these design/plan status records at integration.

**Interfaces:** Consumes Task 2 Client method/result/error wrapper. Produces GroupCancelRequestRequest{Group string}.Check() error, App.GroupsCancelRequest(ctx context.Context,req GroupCancelRequestRequest) (signal.GroupCancelRequestResult,error), Printer.GroupCancelRequest(result signal.GroupCancelRequestResult) error, version1 groupCancelRequest envelope.

- [x] Write request/use-case RED tests for invalid/secret references before resolution/connect, local title resolution before one SendOnly connect, one facade call, partial results, errors.Is/As and redacted pre-submission errors. Implement existing acceptance use-case pattern.
- [x] Write CLI/output RED tests for exactly one argument, --yes before open, no-op/cancellation headings, quoted title, six always-present lower-camel JSON fields, no stdout on errors, account/env/config behavior, setup exactly once, writer/Close/flag/config redaction and device-unlinked exit3. Implement command using acceptance's safe setup pattern. Four new goldens only; existing goldens unchanged unless registration directly changes required help output.
- [x] Update leave help and public comments to direct requesters to cancel-request and avoid claiming full-state-dependent cancellation works. Keep ordinary leave and PNI decline behavior unchanged.
- [x] Document verified-at-revision meaning, no-op vs refusal, one-PATCH/no-retry inspection guidance and preview-title/cache limitations. Add opt-in two-account/both-backend live procedure; no production calls or claims.
- [x] Run GREEN: CGO_ENABLED=0 go test -tags libsignal_go -count=1 -run 'Test(GroupCancelRequest|GroupsCancelRequest|PrinterGroupCancelRequest)' ./internal/app/ ./cmd/ ./internal/output/; broader existing commands/output regressions once.
- [x] Gofmt changed files; self-review/report. Controller integrates docs/PLAN ticks only offline-completed cancellation items, retains live/PNI/global reporting open; runs just fmt, just lint, just check, just check-purego, CGO_ENABLED=0 go test -count=1 ./..., build/docs/help/man inspection and git diff --check.

## Final verification, review and shipping

- [x] Baseline recorded before implementation; self-review of plan covers all spec sections, interfaces and five review-focus cases.
- [x] Three task gates complete, controller independently verified changes; one whole-branch review includes fork and main actual ranges and any deferred findings.
- [x] Final review follow-up: one documentation clarification worker and scoped re-review; no executable findings or unresolved issues. Durable rulings record all seven declined-to-judge dispositions.
- [x] Commit durable evidence/rulings and normally publish `feat/group-cancel-request` as [PR #26](https://github.com/CWBudde/go-signal/pull/26), targeting main. No merge or live calls. Final-head CI outcomes are reported in the PR after this publication-record commit; clean only this plan's owned SDD workspace after verification and retain worktrees/branches.

## Execution record

Baseline `just check` passed before code edits: formatting unchanged, lint 0 issues, libsignal v0.102.2, full cgo race suite and tidy unchanged. `just fmt` also passed. Pure-Go fork baseline signalmeow/libsignalgo subtrees passed. Logs: `/tmp/group-cancel-request-baseline-check.log`, `/tmp/group-cancel-request-fork-baseline.log`. Task 1 review approved without findings; controller pure-Go/cgo-race regressions passed. Fork 9cd9cbd was normally published to purego and immutable v0.2609.0-purego.14. Downloaded Origin and all five changed files match reviewed source. Main pin commit ede3b02 passed just fmt and just lint. Optional full-fork pure-Go Matrix bridge build fails at sqlite3.Error/ErrCorrupt, reproduced unchanged at .13; affected subtree suites pass. Task 2 at 1182d7b passed controller fmt/lint and cgo-race membership regressions; task review approved with no findings. Task 3 review approved with no findings; all integrated checks passed. Fork CI: purego branch37137607045/tag37137625342 succeeded; Go branch37137607019/tag37137625338 test jobs succeeded, lint jobs failed only six byte-identical inherited EOF/goimports files. Controller confirmed the inherited paths are unchanged from .13; new cancellation files are not flagged.

All three task gates approved without findings. Task3 commit bc6ec42. Controller
`just check`, `just check-purego`, no-backend `CGO_ENABLED=0 go test -count=1 ./...`,
build and doc generation passed; generated cancel-request/leave help and man page
were inspected. All six AES assembly targets passed. `just fmt` reformatted one
JSON documentation file before passing integration checks; existing goldens are
unchanged. No production calls. Whole-branch review approved; published as PR #26.

Final whole-change review at main f34c269 and fork 9cd9cbd approved spec alignment
and code quality with no findings at any severity. The reviewer explicitly
distinguished operational errors before rendering from output I/O errors. The scoped re-review caught a wrong-section README replacement. The controller
restored the acceptance sentence and clarified the cancellation paragraph, with
section-aware diff checks. No executable change, fork repin or unresolved finding.

### Rulings, in chronological order

1. Empty-stdout means operational failures before rendering, matching the approved
   in-chat plan. Output I/O errors are safely propagated and may retain bytes
   already accepted by the writer; clarify the new cancellation wording only.
   A streaming writer cannot roll back bytes. Cost if wrong: consumers expecting
   atomic empty output on I/O failure could see partial output. This resolves
   final-review declined item 7.
2. Adopt final-review declined items 1–6 as approved scope/evidence limits: live
   server/phone behavior, PNI/global/other-user mutations, MCP/daemon tools,
   notification/sync and inherited fork lint/bridge remediation stay deferred.
   Publication and final-head PR CI remain mandatory controller checks. Approved
   scope and independently verified baseline evidence govern this increment.
   Cost if wrong: deferred production/device defects or inherited dependency debt
   could remain unresolved; shipping defects would escape if CI verification were
   skipped.

3. Resolve the scoped review's residual README section mismatch by restoring the
   acceptance sentence and applying the approved qualifier to cancellation. The
   reviewer identified an exact prose-only correction, verified directly by the
   controller without a second agent fix wave. Cost if wrong: the public output
   contract could remain misleading or unrelated command documentation could change.

## Publication record

[PR #26](https://github.com/CWBudde/go-signal/pull/26) targets main from
`feat/group-cancel-request`. The fork is already pinned to reviewed immutable .14.
All executable changes passed the integrated checks and broad review; subsequent
commits contain documentation only. Publication initially encountered an automatic
approval rejection for missing visible authorization; the reviewer approved the
normal push and PR after the controller supplied the approved plan and user
implementation instruction. Final-head CI is checked after pushing this record
and its actual outcome is recorded in the PR body. No merge or live calls.
