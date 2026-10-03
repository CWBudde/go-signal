# Group join-request cancellation

Date: 2026-10-03. Baseline: `e1de4e9` (merged PR #25).
Status: approved plan implemented; three task reviews and integrated offline checks pass.
Whole-branch review approved; documentation clarification and PR publication pending.
Live acceptance remains open.

## Intent

Continue Phase 4 / Later with `groups cancel-request <group> --yes`. Cancel only
the selected account's existing pending ACI join request. Do not leave full
membership or decline an invitation. The user chose a dedicated command and a
successful no-op when a fresh authenticated preview confirms no pending request.
HTTP 403/404 does not prove absence and remains an error, including repeat calls.

Success is offline-tested support on cgo and pure Go, account isolation,
plain/JSON output, safe partial outcomes, documentation, independent review and
one go-signal PR. Live acceptance remains unchecked and separately opt-in.

## Interfaces and output

Add `Client.CancelGroupJoinRequest(ctx context.Context, ref string)
(GroupCancelRequestResult, error)` and
`App.GroupsCancelRequest(ctx context.Context, req GroupCancelRequestRequest)`.
The request has `Group string` and a `Check() error` method; CLI confirmation is
checked before opening an account, not placed in the facade.

`GroupCancelRequestResult` has `ID`, `Title`, `Revision`, `Changed`, `Accepted`,
`Verified`. ID is canonical and key-bound. Title is from the authenticated preview.
Changed follows HTTP 200 acceptance, including an unreadable/invalid signed reply.
Accepted means this invocation received HTTP 200 for its PATCH. Verified means
the exact signed deletion was verified at the requested revision, or a fresh
validated preview confirmed pending=false for a no-op. It does not promise
continuous absence of future requests or notification/device sync delivery.

Successful cancellation has all three booleans true. A no-op has false/false/true.
Plain headings are `Join request cancelled` and `No pending join request`, followed
by canonical ID, quoted title and revision. JSON is
`{"version":1,"groupCancelRequest":{"id":...,"title":...,"revision":...,"changed":...,"accepted":...,"verified":...}}`.
All six fields are always present; SchemaVersion stays 1. Operation failures leave stdout
empty; typed partial results remain available to app callers.

Reuse acceptance's 4096-byte pre-trim reference validation, four base64 forms,
group prefix handling and safe rejection of Signal HTTPS/sgnl invite URLs,
including malformed escapes. Only selected-account known keys may be resolved.
Do not import unknown keys. App resolves titles locally before one SendOnly connect.
Every input/configuration/open/resolution/network/output/Close error boundary
hides arbitrary nested secret text while preserving errors.Is/As and exit codes.

## Protocol and facade flow

The pinned fork is `v0.2609.0-purego.13` at `6bb5f56`. Generic UpdateGroup requires
full state and can retry; it is unsuitable because requesters can receive 403.
Add a narrow authenticated password-free preview and single-attempt cancellation.

1. Validate input and lifecycle before store/network access. Hold the existing
   group operation guard through resolution, preview and submission so Close
   cannot release the account store. Check caller cancellation and permanent
   connection loss at operation boundaries.
2. Resolve and validate the stored key against the canonical derived ID before
   exposing it. Apply sensitive websocket logging policy and no-op dependency
   logger to the entire operation. Invalidate group cache before and after it.
3. Fetch `GET /v2/groups/join/`, no password or query, with ordinary group auth.
   Validate public parameters, required title/optional description and numeric
   nonoverflowing X-Signal-Timestamp. Preview data is not full membership state.
4. If PendingAdminApproval is false, return a verified no-op at preview revision.
   Never infer this from a refusal. Ignore invite-link access settings for pending
   cancellation; disabled/reset links must not block an authenticated request.
5. Refuse revision overflow before PATCH. Construct fresh revision+1, plaintext
   sourceUserId own ACI and exactly one deleteMembersPendingAdminApproval action
   whose deleted user ID is encrypted own ACI. No profile credential, PNI action,
   ban, invitation deletion, full member deletion or generic mutation path.
6. Send at most one `PATCH /v2/groups/`, no password or query. Preserve attempted
   revision on rejection/uncertainty and accepted/changed immediately on HTTP 200.
7. Verify the signed response: exact signature size and validity, epoch <=7,
   derived group ID, exact requested revision, encrypted source own ACI and sole
   encrypted deletion target own ACI. Reject unrelated/unknown populated signed
   fields, missing nested actions and incorrect fixed ciphertext lengths.
   Retain signed context/change only after verification, with owned byte slices.
8. Return verified result without requiring a follow-up preview/full-state read:
   after removal, password-free preview may no longer be accessible. Do not send
   member notifications or linked-device sync. Preserve known keys and existing
   title/left records; do not write preview state to full-state/title caches.

Reuse the bounded membership HTTP transport: one Do, configured transport/TLS,
no redirects or replayable GetBody, close all bodies, 1 MiB limit with overflow
detection. PATCH needs no timestamp header. Explicit refusal/conflict/termination
is distinct from transport/unexpected-response uncertainty. HTTP 200 followed by
decode/read/signature failure remains accepted but unverified. Preserve caller
cancellation/error identities behind safe wrappers; never automatically retry.

Fork APIs: `PreviewGroupJoinRequest(ctx, key) (GroupJoinPreview, error)` and
`CancelGroupJoinRequestOnce(ctx, key, revision) (GroupJoinRequestCancelOutcome, error)`.
The outcome owns Attempted, Accepted, Verified, Revision, GroupContext and Change.
Primitives do not cache group state, persist keys, notify, or fetch full state;
normal authorization credential caching remains. Add cancellation-specific
invalid/uncertain/terminated sentinels and map them to facade policies.

## Verification and delivery

Exercise protocol binding, no-op/refusal evidence, partial outcomes, account-aware
fake state, malformed/secret preflight, cancellation/Close and notification/cache
absence. Add four command goldens and use-case/output tests. Run both backend
gates and no-backend tests, build and generated help/man checks. Correct leave
claims to direct requesters to the dedicated command. Keep reference submodule
changes untouched. No live Signal calls, merge, force push, tag movement or replace.

Sequential subagents implement/review fork, facade/fake, then app/CLI/output/docs.
Controller owns planning records, pins, commits and publication. Publish reviewed
fork normally to purego with immutable `v0.2609.0-purego.14`, then one go-signal PR.
Document inherited dependency failures separately from new failures.

## Deferred

PNI invitation decline, global PNI membership reporting, other-user PNI mutation,
MCP/daemon tools and linked-device sync are separate. Add a disposable two-account
live procedure covering approval/cancellation, repeated calls, approval races,
disabled links, administrator visibility and restoration on both backends.
Do not mark that procedure or remaining group-management live checks complete.

## Protocol sources

Read-only signal-cli and pinned fork sources were inspected. Official Signal
Android revision `b377bd213ad370dea96c46f75491b598983ebf7f` was cached during the
preceding acceptance work: PushServiceSocket.java:172,1865 implements absent-password
GET; GroupManagerV2.java:231,1318-1380 uses null password and singleton own ACI
request deletion, with sendToMembers=false at 1346. Android retries are deliberately
not adopted. The signed-deletion verification is our safety boundary.
