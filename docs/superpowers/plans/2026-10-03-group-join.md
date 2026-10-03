# Invite-link Group Joining Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

Status: approved by the user on 2026-10-03; implementation, offline verification
and independent reviews complete. PR shipping pending; live acceptance open.

**Goal:** Implement `groups join <link>` for direct membership and administrator-approval requests, with truthful partial outcomes and no mutation retries.

**Architecture:** Add narrow preview/join APIs to the pinned signalmeow fork, then expose one facade method and application use case. Keep protocol and crypto inside signalmeow, facade types inside go-signal, and rendering in output. Complete each dependency stage before its consumer stage; use subagents for implementation and independent review.

**Tech Stack:** Go, existing cgo/libsignal_go backends, signalmeow/libsignalgo, protobuf, net/http, Cobra, account-aware signaltest fake, command goldens.

**Spec:** [Approved design](../specs/2026-10-03-group-join-design.md).

## Global Constraints

- One invocation submits at most one membership PATCH. Do not retry conflicts, refresh credentials and resubmit, or change a failed request into another kind of membership operation.
- Support the cgo and `libsignal_go` backends and the existing no-backend fallback.
- Limit the entire input to 4096 bytes before parsing or decoding. Require exactly 32 master-key bytes and 16 password bytes.
- Bound new preview/PATCH response bodies to 1 MiB with limit-plus-one reads, close every body, and reject oversized, missing or malformed required messages.
- No result includes an invite password, master key, credential or the supplied link. Errors and verbose logs must also exclude them.
- Record acceptance immediately on HTTP 200, before reading or decoding its body. A pre-PATCH failure is never uncertain.
- No `replace` directive or local-path dependency may land. Keep the existing upstream and libsignal pins; never move an existing tag or force-push.
- Production requests are outside this implementation batch. Keep live joining acceptance unchecked.
- Preserve unrelated user changes; never edit `reference/signal-cli`. The controller owns shared records, dependency pins, commits, tags, pushes and PRs; workers perform no Git mutations or repository-wide auto-fixers.

## Review Focus

- A valid link for another account must join only the selected account; Task 2 tests separate known-key maps and self actions for two accounts.
- A known invited account must receive the invitation-acceptance-required error before any preview/PATCH; Task 2 covers the invitation branch.
- A nested transport error or Cobra flag/argument failure can contain the secret URL; Tasks 1 and 3 test rendered errors and captured logs for actual secret substrings.
- Another device can remove self after an accepted direct join; Task 2 tests a newer nonmember state and retains acceptance without claiming membership.
- A successful HTTP status can carry another group's signed change; Task 1 tests signature-valid wrong-group responses independently of invalid signatures.

---

## Workspace and execution

Go-signal worktree: `/tmp/go-signal-group-join`, branch `feat/group-join`, base `930de7e`.
Approved spec commit: `2740e8d`. Initial `just check` passed on this base, including
lint, pinned libsignal validation, cgo race tests and tidy. Reuse that evidence
until code/dependencies change; do not repeat a baseline for each worker.

Fork source checkout: `/home/christian/Code/mautrix-signal`, currently clean on
`purego`; `v0.2609.0-purego.9` points to `14a1d02`. The controller creates an
isolated fork worktree under `/tmp` after checking current local/remote state.
Do not modify that source checkout or publish an unreviewed fork change.

Use existing ignored workspace directories for compiler/formatter caches and
temporary files. Export all six variables before commands:

```sh
export TMPDIR=/home/christian/Code/go-signal/bin/.group-links-tmp
export GOTMPDIR="$TMPDIR" JUST_TEMPDIR="$TMPDIR" XDG_RUNTIME_DIR="$TMPDIR"
export XDG_CACHE_HOME=/home/christian/Code/go-signal/bin/.group-links-cache
export GOCACHE=/home/christian/Code/go-signal/bin/.group-links-go-cache
```

Go-signal's pinned submodule is initialized in this worktree. Its ignored
`third_party/lib` has symlinks to the original checkout's `.a` and `.sha`.
For direct cgo commands set `CGO_LDFLAGS="-L /home/christian/Code/go-signal/third_party/lib"`.
Build/docs recipes need `GOFLAGS=-buildvcs=false` here because the sandbox has a
spurious `/tmp/.git`; the justfile's explicit version metadata remains enabled.

Execute Tasks 1–3 sequentially with scoped implementers and reviewers. Reviews
cover both spec compliance and code quality. Workers read the approved spec and
their extracted task brief, follow TDD, and report changed files, RED/GREEN
evidence, final scoped commands and concerns. They cannot delegate further.
The controller verifies each result and owns all commits. Task 4 is controller
integration with one whole-change reviewer. Maintain a plan-specific ignored
progress ledger so completed work is not repeated after compaction.

### Task 1: Fork preview and single-attempt join transport

**Files (fork worktree):**

- Create: `pkg/signalmeow/groups_join.go` — public types, preview, action construction, signed-response checks.
- Create: `pkg/signalmeow/groups_join_http.go` — bounded secret-safe HTTP transport and status classification.
- Create: `pkg/signalmeow/groups_join_test.go`, `groups_join_http_test.go`, `groups_join_export_test.go` — external tests and narrow internal adapters.
- Modify: `pkg/libsignalgo/PUREGO.md` — document deliberate protocol extension.
- Modify: `.github/workflows/purego.yml` — run new offline signalmeow tests in the existing pure-Go job.

**Interfaces:** Produces `GroupJoinPreview`, `GroupJoinOutcome`,
`(*signalmeow.Client).PreviewGroupJoin(ctx, key, password)` and
`(*signalmeow.Client).JoinGroupOnce(ctx, key, password, preview)` with the exact
types/signatures in the spec. Consumes existing group authorization, self
credentials, libsignalgo primitives and configured HTTP client.

- [x] **Step 1: Establish the fork baseline.** Run `CGO_ENABLED=0 go test -tags libsignal_go ./pkg/signalmeow/... ./pkg/libsignalgo/...` and `go run ./pkg/libsignalgo/internal/stubgen -check`. Record failures before any edits; inspect source guidance and toolchain locally.
- [x] **Step 2: Write failing preview/HTTP tests.** Add `TestGroupJoinPreview`, `TestGroupJoinHTTPSecrecy` and `TestGroupJoinHTTPBounds` using serial in-memory RoundTripper fixtures. Assert exact URL-safe unpadded endpoints, Basic auth/agent headers, timestamp/public-parameter/attribute validation, 403/423 errors, no redirects, body closure and the 1 MiB + 1 rejection. Capture nested URL errors and verbose logs; assert neither contains the actual password or supplied URL.
- [x] **Step 3: Run RED.** Run `CGO_ENABLED=0 go test -tags libsignal_go -run '^TestGroupJoin' ./pkg/signalmeow`. Tests must fail because the new helpers/types are absent or intentionally incomplete, before production implementation.
- [x] **Step 4: Implement preview and HTTP helpers.** Put secret-safe requests in `groups_join_http.go`; copy the configured HTTP client and reject redirects. Implement `PreviewGroupJoin` in `groups_join.go` with key/password checks before crypto, fixed production host, existing authorization and conservative access/status mapping. Render errors without raw URL/body/header values while preserving identity.
- [x] **Step 5: Write failing action/outcome tests.** Add `TestGroupJoinActions`, `TestGroupJoinResponseBinding`, `TestGroupJoinOutcomes` and `TestGroupJoinNoRetry`. For preview revision 7 assert these wire invariants:

```go
if actions.Version != 8 || len(actions.GroupId) != 0 { t.Fatal(actions.Version) }
// Direct: one DEFAULT presentation, JoinFromInviteLink=false, plaintext self source.
// Request: one pending-admin presentation, no role/userId/profileKey/timestamp.
```

Use existing test server parameters/issuance fixtures for credential and signed-response tests; do not add production private material. Test valid signatures with wrong group ID, wrong revision/self/source/action, unsupported epoch, normalized server fields and unrelated mutations. Assert zero PATCHes for credential/overflow failures, one PATCH on conflict/transport/5xx, and accepted=true after HTTP 200 followed by body/decode/signature failure.

- [x] **Step 6: Run RED for joining, then implement `JoinGroupOnce`.** Build only the selected self action with existing crypto. Validate preview policy, reject pending/disabled/unknown/overflow before submission, classify Attempted/Accepted separately, verify signature and exact group-ID binding before decrypting/checking semantic self action. Populate GroupContext/Change only after validation. Never call ordinary UpdateGroup or retry.
- [x] **Step 7: Run GREEN and scoped verification.** Run all new signalmeow tests in pure-Go and cgo/race modes, `CGO_ENABLED=0 go vet -tags libsignal_go ./pkg/...`, the pure-Go libsignalgo suite and `stubgen -check`. Use file-scoped gofmt; controller checks that generation would introduce no unrelated diff. Expected: exit 0, no failures, no shim/API drift.
- [x] **Step 8: Review, commit and publish the fork.** Controller inspects all changed paths, obtains independent review, checks remote branch/tag state, and commits. Follow `docs/maintenance.md`: normal fast-forward push to `purego`, publish the next unused `v0.2609.0-purego.N` tag; no fork PR/force-push/tag replacement. Only reviewed, verified source may be tagged. Pin that tag with `go get`/tidy in go-signal; confirm only intended dependency changes and no `replace`.

### Task 2: Facade, validation and account-aware fake

**Files (go-signal):**

- Create: `internal/signal/groups_join.go`, `groups_join_test.go` — facade types, secret-free preflight/parser, errors.
- Create: `internal/signal/meow_groups_join.go`, `meow_groups_join_test.go` — lifecycle and orchestration against the new fork.
- Modify: `internal/signal/client.go`, `groups_export_test.go` — public method and narrow test adapters.
- Create: `internal/signal/signaltest/groups_join.go`, `groups_join_test.go`; modify `fake.go` — independent server fixtures and per-account retained keys.

**Interfaces:** Consumes the tagged Task 1 APIs. Produces
`Client.JoinGroup(context.Context, string) (GroupJoinResult, error)`,
`CheckGroupInviteLink(string) error`, status constants `GroupJoinMember` and
`GroupJoinRequesting`, and the exact GroupJoinResult fields from the spec.
Add `ErrInvalidGroupInviteLink`, `ErrGroupLinkInactive`, `ErrGroupTerminated` and
`ErrGroupInvitationRequiresAcceptance`; reuse `ErrGroupChanged` and
`ErrGroupUpdateUncertain`. Errors identify acceptance/attempted revision without
leaking causes' secrets. Fake fixture fields for Task 3: `GroupJoinServer
map[string]signal.Group` keyed by standard-base64 master key; existing
GroupLinkStates/GroupLinkPasswords keyed by ID; `GroupJoinKnownKeys
map[string]map[string]string` keyed by account ACI then master key to ID;
`JoinGroupErr` and `GroupJoinFollowUpErr` error injection. Pending membership
belongs to the fixture group's selected-account requesting members.

- [x] **Step 1: Write parser tests first.** Add `TestGroupJoinLinkValidation` with HTTPS/sgnl, padded/unpadded, surrounding whitespace, exact host/path, rejected credentials/port/query/internal whitespace, input >4096 bytes, absent/bad fragment, bad protobuf/v1 duplicate or conflicting contents, unsupported version and secret-length errors. Assert sentinel identity and no actual input/key/password in error text.
- [x] **Step 2: Run RED, then implement `CheckGroupInviteLink` and owned parsing.** Run `CGO_ENABLED=0 go test -run '^TestGroupJoinLink' ./internal/signal`, observe failure, then implement backend-independent protobuf/URL validation and the result/error types. Extend Client only alongside real/fake implementations so every build remains coherent.
- [x] **Step 3: Write orchestration/fake tests first.** Add `TestGroupJoinLifecycle`, `TestGroupJoinNoOps`, `TestGroupJoinFailureOutcomes`, `TestGroupJoinAccountIsolation` and `TestFakeGroupJoin`. Use injected transport/storage operations to assert call order and counts. Assert invited self fails before preview, fresh member/no-op despite disabled link, fresh pending/no-op without full fetch, key persistence before PATCH, persistence failure prevents PATCH, original left markers preserved until verified full membership, and cleanup on every path.

```go
if got.Status != signal.GroupJoinRequesting || !got.Verified { t.Fatal(got) }
if got.Accepted || got.Changed { t.Fatal("pending no-op mutated") }
// Accepted fetch failure: Accepted/Changed true; Status/Title empty; ID/revision retained.
// Uncertain transport: errors.Is(err, ErrGroupUpdateUncertain), Accepted false, attempted revision.
```

Cover canceled-before-preview/submission, closed/disconnected/unlinked account behavior, inactive/banned server refusals, direct membership at later revision, concurrent removal, fresh cache authorization, signature-verified response propagation only, notification failure and unchanged server state on definite rejection. No fake stored-key existence may imply membership. Test both accounts' keys and selected self actions.

- [x] **Step 4: Run RED, implement real and fake joining, then GREEN.** Use `groupClient` lifecycle and fresh cache invalidation. For nonmembers preview, persist key, submit once, distinguish approval from direct follow-up. Cache only verified full state and inspect notification outcomes. Fake mirrors typed partial outcomes, ownership and lifecycle with the Task 3 fixture fields.
- [x] **Step 5: Verify and review.** Run `CGO_ENABLED=0 go test -count=1 -tags libsignal_go ./internal/signal ./internal/signal/signaltest`, cgo/race for those packages, fallback `CGO_ENABLED=0 go test -count=1 ./internal/signal ./internal/signal/signaltest`, and nonfixing scoped lint. Controller checks every path and discriminating tests, obtains review and commits the coherent facade change.

### Task 3: Application, CLI and output

**Files:**

- Create: `internal/app/groups_join.go`, `groups_join_test.go`.
- Create: `cmd/groups_join.go`, `groups_join_test.go`; modify `cmd/groups.go`.
- Create: `internal/output/groups_join.go`, `groups_join_test.go`.
- Create: eight `cmd/testdata/groups_join_{member,requesting,member_noop,requesting_noop}{,_json}.golden` files.

**Interfaces:** Consumes Task 2 types, CheckGroupInviteLink, Client.JoinGroup and
fake fixtures. Produces `app.JoinGroupRequest{Link string}`, its `Check() error`,
`(*app.App).GroupsJoin(context.Context, JoinGroupRequest) (signal.GroupJoinResult,
error)`, `newGroupsJoinCmd(*clientOpener, *printerFactory) *cobra.Command`, and
`(*output.Printer).GroupJoin(signal.GroupJoinResult) error`.

- [x] **Step 1: Write failing app/output/command tests.** Add `TestGroupsJoinPreflight`, `TestGroupsJoinNoRetry`, `TestGroupsJoinGolden`, `TestGroupsJoinSecretErrors` and `TestGroupJoinOutput`. Reuse established fake-account and golden harnesses. Assert invalid requests do not open account/Connect, exact four success outcomes, both selected accounts, safe title quoting, additive version-1 groupJoin JSON, and no secret field. Inject accepted/uncertain errors and assert typed result survives app while CLI stdout is empty.
- [x] **Step 2: Run RED.** Run `CGO_ENABLED=0 go test -tags libsignal_go -run 'Test(GroupsJoin|GroupJoinOutput)' ./internal/app ./internal/output ./cmd`; observe absent use case/command/renderer failures.
- [x] **Step 3: Implement the use case and renderer.** Check input before connecting, use connectSendOnly, call JoinGroup once, and wrap secret-free errors with accepted versus attempted metadata and inspection guidance. Pending guidance explains administrator/phone inspection and inaccessible groups show. Implement the spec's four plain headings and exact JSON field set; schema version remains 1.
- [x] **Step 4: Register the command and produce goldens.** Exactly one positional argument; preflight before clients.open; existing printer/close patterns. Joining itself authorizes the action, so no confirmation flag. Help describes requests, no retries and secrecy. Test malformed flags/counts with secret arguments to ensure Cobra errors do not echo them; update only the eight new goldens after manual inspection.
- [x] **Step 5: Verify and review.** Run full app/output/cmd pure-Go tests, scoped cgo/race tests and nonfixing scoped lint. Controller independently runs discriminating tests, reviews scope and goldens, obtains review and commits.

### Task 4: Integrated verification, documentation and PR

**Files:** Modify `README.md`, `docs/json.md`, `docs/dev.md`,
`docs/maintenance.md`, `PLAN.md`, and spec/plan completion records.

**Interfaces:** Consumes the reviewed fork tag and Tasks 2–3; produces truthful
user docs, planning records and one go-signal PR. Controller owns this task.

- [x] **Step 1: Inspect the integrated change.** Review every modified file, interface implementation, tagged module content, dependency diff, scoped test evidence and independent findings. Check for unrelated edits, replace/local paths, secret-containing outputs, schema bumps or weakened criteria. Resolve material findings with the responsible worker and covering tests.
- [x] **Step 2: Write user docs and live procedure.** Document quoted invite-link commands, direct/request/no-op semantics, stored-key versus membership distinction, accepted/uncertain inspection, pending groups show limitations, and additive JSON. Update maintenance fork scope. Add `docs/dev.md#group-join-live-check` for disposable-account tests on both backends, explicitly opt-in and unchecked.
- [x] **Step 3: Independently run final checks.** Run `just fmt`, `just check`, `just check-purego`, `CGO_ENABLED=0 go test -count=1 ./...`, `GOFLAGS=-buildvcs=false just build`, and `GOFLAGS=-buildvcs=false just docs-gen`. Inspect `groups join --help`, fmt-check and git diff --check. Expected: all exit 0, lint 0 issues, no tests fail. Re-run affected/full checks after substantive fixes, not after unchanged code.
- [x] **Step 4: Request whole-change review.** Supply both the fork diff/tag evidence and go-signal diff plus approved spec, task reports and parked concerns. Review protocol binding/secrecy, failure-state semantics, account isolation, CLI contract and documented limits. Resolve blockers and independently verify resulting fixes.
- [x] **Step 5: Update PLAN.md honestly.** Add checked joining implementation/offline verification entries under Later; update the Phase 4.2 note and CLI design. Leave invitation acceptance/cancellation and all live criteria open. Mark spec/plan implementation status only to the extent verified; controller owns checkbox updates.
- [ ] **Step 6: Commit, push and open the PR.** Inspect branch history/existing PR, stage only this batch, commit, push normally and use `gh pr create --body-file` with an exact temporary description. Report implemented behavior, actual checks, pinned fork tag and remaining live acceptance. Do not merge or mark the whole group-management parent complete.

## Plan self-review

All spec sections map to tasks: protocol/transport/secrecy to Task 1; URL/result
contracts, retained state and fake to Task 2; use case/output/preflight to Task 3;
both-backend integration, live documentation and shipping to Task 4. The five
Review Focus conditions have explicit owning tests. Shared type names and
signatures match the approved spec; the additional fake fields and validation
helper are implementation decisions for consistent consumers. No product code
or fork edits are authorized by writing this document alone; execution begins
after the user reviews this plan.

## Execution decisions and evidence

The approved scope required these controller decisions during implementation:

| Decision                                                                       | Reason                                                                                 | Cost if wrong and verification                                                                                           |
| ------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| Carry opt-in privacy through the fork websocket queue and response correlation | Per-call logger suppression alone leaked credentials from connection-owned loops       | Reduced credential diagnostics or correlation regression; actual-loop RED/GREEN and both backend suites cover the change |
| Share the owned parser in `internal/signal/groupinvite`                        | Facade and fake need the same validation without a public secret-bearing facade helper | Extra package boundary to maintain; parser and both consumer tests cover it                                              |
| Adapt fake show/list/title/resolution for selected-account retained keys       | A retained key is account-local and does not prove membership                          | Legacy fake regression or cross-account visibility; direct ID/key/alias and complete fake suites cover it                |
| Preserve fresh verified result metadata on a later cache-write failure         | Verification comes from server evidence; persistence is a separate follow-up           | Callers could mistake verification for persistence; cache errors still propagate, with result/error assertions           |
| Add per-account title/left caches for new join fixtures                        | One member's refresh must not clear another pending/nonmember account's local marker   | More fixture setup or compatibility regression; six two-account regressions and complete fake suites cover it            |

Task 1 published `v0.2609.0-purego.11` after independent protocol/privacy reviews,
scoped cgo/race and pure-Go suites, vet, API parity and generation/build checks.
The downloaded module matches reviewed source bytes and has no `replace`.
Pure-Go fork CI passed; the upstream Go lint workflow still flags formatting in
untouched libsignalgo files. Its test jobs passed, and new join-file hook findings
were corrected in the follow-up immutable tag without moving `.10`.

Task 2 passed independent spec/quality review after closing both fake isolation
findings. Pure-Go, cgo/race and no-backend facade/fake suites, scoped vet/dual lint,
controller fresh discriminating tests, `just fmt` and `just lint` passed.
Task 3 passed full affected pure-Go suites, scoped cgo/race, both scoped linters
and controller fresh matrices. Its review identified a pre-submission connection
classification gap; the fix has actual RED/GREEN app/CLI regressions, preserves
error identity and has passed affected-suite/race/lint verification. Independent scoped
re-review approved spec compliance and quality with no residual findings.
Whole-change review approved specification compliance and code quality with no
Critical, Important or Minor findings. PR shipping remains pending.

Integrated `just fmt`, `just check`, `just check-purego`, full no-backend fallback,
build, docs generation, help inspection and diff checks passed. Both repository
linters reported zero issues; the libsignal pin matched and AES assembly was
selected on all six release targets. After the isolated connection-boundary fix,
affected app/CLI suites and fresh discriminating checks passed; unchanged suites
were not repeated. Builds/docs used `GOFLAGS=-buildvcs=false` for this linked
worktree's existing VCS-stamping environment issue.

Live Signal and peer-phone acceptance have not been run.

Final review scope dispositions confirm the approved boundaries: live acceptance
and explicitly deferred features remain open; general unchanged upstream
hardening and inherited libsignalgo formatting remain outside this bounded
extension; publication evidence and actual PR creation are controller-owned.
The controller accepted all five review limitations on that basis. Cost if wrong:
live or unchanged upstream behavior may require later fixes, or incomplete
publication evidence may require shipping rework. No live result or general
upstream certification is claimed, and shipping is checked after creation.
