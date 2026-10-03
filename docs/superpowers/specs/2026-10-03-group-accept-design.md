# Group invitation acceptance

Date: 2026-10-03. Baseline: `7f7247b` (merged invite-link joining, PR #24).
Status: scope and written design approved by the user. Implementation plan
written and awaiting user review; no implementation yet.

## Intent and scope

The user asked to continue Phase 4 / Later after merging PR #24 and approved
invitation acceptance, including phone-number identity (PNI) invitations, as the
next batch. Add `groups accept <group>` for the selected linked account. It
accepts an existing invitation into full membership; it does not create a new
invitation or request administrator approval. A fresh full-member state is a
successful no-op. Submit at most one acceptance PATCH and preserve accepted,
uncertain and verified outcomes independently.

Success means an account can accept its own ACI or PNI invitation through the
facade, application and CLI, with account-aware offline tests on both backends,
plain/JSON output, documentation and independent review. Production acceptance
remains an unchecked, separately opt-in criterion.

Split `PLAN.md:1973` into invitation acceptance and request cancellation when
recording verified implementation. Request cancellation is the following batch:
it needs passwordless join-preview access because requesters may receive 403
from full-state GET. PNI invitation decline through `groups leave`, broad PNI
self-membership reporting in other commands, arbitrary other-user PNI mutations,
MCP/daemon tools and live production calls are outside this batch. Acceptance
performs its own typed self-identity matching; it does not change the ACI-only
meaning of `Group.MembershipOf` or silently widen existing leave policy.

## Findings and chosen approach

The fork pin is `v0.2609.0-purego.11`. It already decrypts pending service IDs as
ACI or PNI. Facade conversion also retains PNI pending entries. The historical
PNI-skipping notes in `PLAN.md`, facade comments and fork comments are stale;
existing conversion fixtures demonstrate retention, although self-membership
recognition still compares only ACI. Correct those notes only to describe the
verified behavior and remaining limitations.

The connected device and fork store already provide both selected-account ACI
and PNI. Group authentication already presents a credential binding them.
ACI promotion encoding exists in the generic backend, and PNI promotion structs,
decryption and cache application exist, but outgoing PNI promotion encoding is
absent. Generic `UpdateGroup` fetches/cache-processes before mutation, uses a
different response-verification policy and can retry some conflicts. It is not
the acceptance submission path.

Three approaches were considered:

1. Dedicated fresh acceptance reads and a single-attempt acceptance primitive,
   reusing the existing bounded HTTP, privacy and exact signed-response patterns.
   This is selected: it makes ownership, submission and follow-up outcomes clear.
2. Extend generic `UpdateGroup` and its encoder. This creates broader changes to
   retry/cache behavior and still lacks an isolated invitation read.
3. Implement acceptance and cancellation together. They share transport but
   require different authorization/state evidence; separate PRs keep the shared
   interface change and its verification manageable.

## Public contract and output

Add `Client.AcceptGroupInvitation(ctx context.Context, ref string)
(GroupAcceptResult, error)`. The real implementation is available under
`cgo || libsignal_go`; the fallback follows existing `ErrCGORequired` behavior.
Add `app.GroupAcceptRequest{Group string}` with `Check`, and
`App.GroupsAccept(ctx, req)` for use by the CLI and future consumers.

`GroupAcceptResult` owns six ordinary fields: `ID`, `Title`, `Revision`,
`Changed`, `Accepted` and `Verified`. It contains no master key, profile key,
invite password, link, or dependency types. `ID` is the canonical derived group
ID once resolved, never an unclassified input that could be a master key.

On a successful mutation, `Changed`, `Accepted` and `Verified` are true. A
successful already-member no-op sets only `Verified` true, preserving the fresh
revision. `Accepted` means this invocation received HTTP 200 for its PATCH;
`Verified` at the facade means a fresh full read showed own ACI membership. It
does not mean notifications or persistence succeeded. `Changed` follows PATCH
acceptance, even when later verification fails. Neither ID nor revision alone
proves acceptance or membership.

Plain output says `Invitation accepted` or `Already a member`, followed by ID,
quoted title and revision. JSON is `{"version":1,"groupAccept":{...}}`,
with `id`, `title`, `revision`, `changed`, `accepted` and `verified` always present.
This additive document retains schema version 1 and the existing `version` key
(the initial design example's `schemaVersion` label was corrected during planning).
Errors produce no result on
stdout; typed partial results remain available to application callers.

The CLI takes exactly one `<group>` using existing reference conventions:
`group:<id>`, standard/URL-safe base64 ID or a known master key, or an unambiguous
cached title. An invitation must already be known to the selected account from
sync/messages. Do not import or persist an unknown key through this operation.
Reject blank input, inputs exceeding 4096 bytes before trimming, malformed
explicit `group:` references and Signal HTTPS/sgnl invite URLs before opening an
account. Other title resolution stays in `App.ResolveGroup` before connecting.
No extra confirmation flag is needed for this explicitly requested membership
operation; retain the existing selected-account and configuration behavior.

## Facade state flow

1. Validate input locally before store/network work. The App resolves the local
   title/reference before connecting once in send-only mode.
2. Use existing connection/Close lifecycle guards and the operation wait group
   before connected-store access. Check closing, cancellation and permanent
   connection loss so Close cannot release the store during key resolution.
3. Apply the sensitive websocket policy and a no-op dependency logger to the
   whole backend operation, including resolution, authorization, reads and
   notifications. Resolve its known ID/key under the selected account; keep
   arbitrary resolution/storage errors behind a redacted boundary. Validate the
   stored master key length and derived ID before exposing an ID or sending HTTP.
   Invalidate the group cache before the first read and on all exits.
4. Fetch uncached, authenticated full group state with the new fork reader.
   Require the returned identity to match the stored key/derived ID.
5. Full own ACI membership is a verified no-op, even if stale invitations remain.
   Update the local title cache without PATCH, credential fetch or notification.
6. Otherwise select a pending invitation whose typed service ID exactly matches
   own ACI, falling back to own nonzero PNI. ACI wins when both are present; never
   compare bare UUIDs across identity types. Other users' invitations, a pending
   approval request, no invitation, duplicate same-identity invitations, invalid
   invitation roles, simultaneous own invitation/requesting state or a ban of
   own ACI/PNI fail before PATCH. Return a dedicated
   `ErrGroupInvitationNotFound` for absence; malformed
   state uses the existing invalid-response policy. Revision overflow is refused
   before credential acquisition/submission. Do not require full-member or admin
   authorization to accept oneself.
7. Call the dedicated fork acceptance primitive once, with the fresh revision and
   exact invited service ID. Preserve attempted revision on definite/uncertain
   errors and acceptance immediately when the backend records it. Do not retry
   a conflict or fetch/re-submit after any submission error.
8. Only a valid signed acceptance response permits subsequent fresh membership
   fetch and propagation of its change. Require fresh state with the same ID,
   revision at least the accepted revision and own ACI as a full member. A later
   concurrent removal yields an accepted-but-unverified error. The fresh server
   role/title and revision are authoritative; do not promote a role locally or
   describe a later revision as the exact submitted revision.
9. Set verified result metadata before local cache persistence, clear a prior
   left marker, and propagate persistence failures without losing verification.
   Notify using the verified signed context/change and fresh group. Check both
   top-level and per-recipient send errors, retain accepted/verified results on
   failure, and never initiate follow-ups after caller cancellation.

The no-op and mutation flows use the same account-local title store. No changes
to shared receive acknowledgement semantics, database schema or account discovery
are required. Existing ordinary list/show/leave paths retain their current policy;
documentation must explicitly state that PNI self-invitation recognition added
here does not claim PNI decline or request cancellation support there.

## Fork read and transport boundary

Add `(*Client).FetchGroupForAcceptance(ctx, key)` returning an owned `*Group` from
full state, without consulting/populating `GroupCache`, receiving endorsements,
writing profile keys, storing keys or notifying. Use it before and after the
acceptance. Invited accounts lack send endorsements; avoiding endorsement parsing
also avoids the pre-existing cgo stderr panic diagnostic on that path.

Use fixed production storage endpoints: authenticated `GET /v2/groups/` for full
state and `PATCH /v2/groups/` for acceptance, without a link password/query.
Reuse/factor only the bounded sensitive HTTP machinery needed by joining and
acceptance; keep existing password-bearing join paths unchanged. Copy the
configured HTTP client to retain transport/TLS settings, refuse redirects, clear
`GetBody` to avoid automatic body replay, perform one `Do` per operation and close
every response body. Successful response bodies are bounded to 1 MiB, detecting
overflow with limit+1. Reads validate the required numeric, nonoverflowing
`X-Signal-Timestamp`; PATCH does not require that header.

The full-state reader validates its key, response/group presence and public
parameters against the derived group parameters before decryption. Guard every
untrusted fixed-array conversion reached in members, pending/requesting members,
bans, attributes and a returned invite-link password (which is never output).
Reject missing nested records and malformed service-ID or
profile-key representations with ordinary errors, not panics. Do not silently
skip malformed pending entries: hidden entries could invalidate self-invitation
selection. Keep this stricter parsing scoped to the acceptance reader; avoid an
unrelated rewrite of ordinary reads. Unknown full-state metadata is acceptable
when it does not alter the fields used for acceptance authorization.

Represent explicit HTTP rejections (including 403 and conflict) with sentinels,
and 423 as terminated. Do not map every full-state 403 to an inactive invite link.
An unexpected PATCH response or transport failure is uncertain. Record HTTP 200
before body reading/decoding; a subsequent failure is accepted, even with an
empty, oversized or malformed response. GET failure makes no mutation claim.
Errors never echo request URLs, credential headers, bodies, profile material,
stored master keys or arbitrary nested error text; `Unwrap` preserves identity.

## Fork acceptance and signed response

Add `(*Client).AcceptGroupInvitationOnce(ctx, key, revision, invitedServiceID)`
returning `GroupInvitationAcceptOutcome`: `Attempted`, `Accepted`, `Verified`,
`Revision`, `GroupContext` and `Change`. Here `Verified` means the signed response
contains precisely the expected acceptance; the facade still verifies fresh full
membership. Context/change are nil until signed verification succeeds. The
primitive does not fetch full state, retry, cache, persist or notify.

Require selected account ACI and an invited service ID equal to that ACI or its
nonzero PNI. Obtain the own ACI expiring profile credential, create a presentation
and decrypt/check its ACI against this account before submission. Retain the
presentation's decrypted profile key as the exact expected response key.
No credential fallback or invitation-of-self path is permitted.

Construct version fresh+1 and plaintext `sourceUserId` equal to the invited
service ID. ACI acceptance carries exactly one
`promoteMembersPendingProfileKey` action with the presentation. PNI acceptance
carries exactly one `promote_members_pending_pni_aci_profile_key` action with the
presentation. Do not fill response-only `user_id`, `pni` or `profile_key` fields
in the request, add members, alter roles, or include unrelated actions/group ID.

Verification checks the server signature, supported change epoch (at most 7),
exact derived group ID, exact requested revision, action kind/cardinality and
typed own identities/profile key. Reject unknown or unrelated populated signed
action fields and invalid/contradictory identity representations. Require exact
ciphertext lengths before conversion and copy all retained bytes.

An ACI response source must decrypt to own ACI. For PNI acceptance, the signed
promotion must bind both the exact invited PNI and own ACI/profile key; allow only
that invited PNI or own ACI as source. This is a bounded compatibility choice
informed by official Android processing that represents the PNI promotion editor
as its ACI; it does not establish that the live server normalizes the signed
source. Offline tests must bind both identities for either allowed source. No
other identity is accepted. Construct the semantic PNI promotion
with both typed IDs; its signed context preserves the actual signed bytes.
Do not use the ordinary mutating `DecryptGroupChange` as a verification shortcut.

Allow the protocol's presentation form or normalized encrypted ACI/profile-key
form for ACI promotion; PNI responses must additionally contain the expected
encrypted PNI. Fields in a single identity representation must be complete and
noncontradictory. Fresh full-state verification, rather than synthetic cache
application, establishes actual membership and role.

## Fake, application and command integration

The fake implements the same invitation selection, no-op, revision and outcome
policy under the selected account's lock/lifecycle. Reuse the join fixtures'
account-local retained keys/title caches and owned server snapshots where useful.
Support known legacy group fixtures without leaking a newly introduced server
snapshot to another account. Matching uses the selected account's registered PNI,
never another fixture's PNI. Reject unknown account-local keys and wrong-type UUID
matches, and do not mutate fixtures on preflight or definite submission failure.

Accepted fake mutation removes the selected invitation and adds own ACI membership:
ACI acceptance preserves the offered role; PNI acceptance uses ordinary-member
role, following Android's PNI promotion application. This is a fixture model;
the real facade always reports freshly fetched server state. Preserve unrelated
membership, title, settings and bans. PNI promotion also removes a duplicate own ACI invitation when
represented by that promotion, matching the existing backend application rule.
Separate submission and follow-up error injection; retain committed server state
on accepted failures and isolate title/left state by account. Tests cover both
ACI-first selection and PNI fallback, cloned inputs and Close/reopen visibility.

The application preflights before opening/network work, resolves using existing
rules, connects once and calls acceptance once. Preserve typed partial results
and sentinel/cancellation/unlink identity through redacted wrappers. A connection
failure is explicitly before submission, even if a nested error happens to carry
a mutation-related sentinel. Resolution and arbitrary injected-client errors
must not reveal a possibly secret reference.

The command follows join's scoped secret-safe configuration/flag/error/Close
boundaries; run inherited configuration exactly once. Account opening, printing,
argument-count and flag errors must not echo a master-key argument. Preserve
normal exit-code mapping, including device unlinked = 3. Register only the new
`groups accept` command; existing output and command goldens remain unchanged.

## Verification, records and shipping

Use test-first changes and real production crypto with private test notary/server
parameters. Fork tests cover ACI/PNI request bytes, own credentials, signed-response
normalization/adversarial cases, fixed-array guards, fresh uncached reads with no
endorsements/profile writes, HTTP body/timestamp/status/redirect/replay boundaries,
one PATCH, context cancellation and actual sensitive websocket logging regressions.

Facade tests use an injected operations harness for ordering, strict identity and
ownership, all pre-PATCH refusals, already-member no-op, stale state, key mismatch,
accepted/uncertain outcomes, concurrent removal, cache/notification failures,
Close/unlink and canceled follow-ups. Fake/app tests exercise two accounts sharing
a server group, wrong PNI/type collisions, resolution and connection failures,
failure atomicity and account-local persistence. Command tests add four goldens:
accepted and no-op, each plain/JSON; also check preflight, account selection,
configuration exactly once, redaction, empty error stdout and exit-code behavior.

Before integration, verify the fork's affected tests on cgo and pure Go, libsignalgo
API parity and existing join/privacy regressions. Publish reviewed commits to the
fork's `purego` branch normally with a fresh immutable `-purego.N` tag following
`docs/maintenance.md`; never move existing tags or add a go.mod `replace`. Retain
upstream and libsignal pins. The controller owns dependency pins, documentation,
PLAN.md, commits and shipping; workers own only their assigned implementation.

After integration, independently run `just fmt`, `just check`, `just check-purego`,
the no-cgo/no-backend-tag suite, `just build` and `just docs-gen`; inspect generated
help/goldens and all changed paths. Use sequential implementation and review agents
because the fork/shared contract precedes facade/fake and application/CLI work.
Review the entire integrated change before creating one go-signal PR. Run required
checks before commits; document any inherited fork CI limitation accurately.

Update README, JSON/dev/maintenance docs and PLAN.md only for verified behavior.
Record a separately opt-in two-account disposable-group procedure for ACI and PNI
invitations, already-member repeats, unrelated/revoked invitations, conflicts,
follow-up inspection, notifications and fresh phone/server membership on both
backends. Leave live acceptance and cancellation/PNI decline criteria open.

Baseline verification in `/tmp/go-signal-group-accept` passed `just check` before
implementation: formatting unchanged, lint reported 0 issues, libsignal matched
`v0.102.2`, the complete cgo race suite passed and go.mod/go.sum remained tidy.
The worktree's libsignal submodule was initialized from the local checkout at
the recorded commit; its ignored library files reference the existing archive.
No product code or dependency pins were changed while preparing this design.

## Source evidence

- `PLAN.md:772–788` and `:1973` at `7f7247b` identify invitation/cancellation gaps.
  `internal/signal/meow_groups_test.go` already verifies PNI pending conversion;
  `groups.go:223`, `meow_groups.go:308` and join's pending guard remain ACI-only.
- Fork `2a6b959` (`v0.2609.0-purego.11`): `groups.go:1134` retains pending service
  IDs, `:352` authenticates both identities, `:649` performs the current side-effecting
  group read, `:1257` encodes ACI promotion, and `:937` decrypts PNI promotion.
  `groups_join*.go` supplies the bounded transport and exact signed-response pattern.
- Read-only signal-cli checkout `13d603d3b5898d7b3b527305afc23c9c7ba84c5b`,
  `GroupV2Helper.java:473–502`, selects the invited service ID as the request source
  and uses the own ACI profile credential. This is the user's existing checkout;
  do not change its revision or other original workspace state.
- Official Signal Android revision `b377bd213ad370dea96c46f75491b598983ebf7f`,
  inspected 2026-10-03: [GroupsV2Operations.java](https://raw.githubusercontent.com/signalapp/Signal-Android/b377bd213ad370dea96c46f75491b598983ebf7f/lib/libsignal-service/src/main/java/org/whispersystems/signalservice/api/groupsv2/GroupsV2Operations.java)
  defines presentation-only ACI/PNI acceptance at lines 290–305 and typed PNI-to-ACI
  promotion processing at lines 752–772.
  [PushServiceSocket.java](https://raw.githubusercontent.com/signalapp/Signal-Android/b377bd213ad370dea96c46f75491b598983ebf7f/lib/libsignal-service/src/main/java/org/whispersystems/signalservice/internal/push/PushServiceSocket.java)
  defines the password-free PATCH endpoint at lines 1794–1815.
  [GroupManagerV2.java](https://raw.githubusercontent.com/signalapp/Signal-Android/b377bd213ad370dea96c46f75491b598983ebf7f/app/src/main/java/org/thoughtcrime/securesms/groups/GroupManagerV2.java)
  establishes why later cancellation needs a distinct passwordless preview flow.
