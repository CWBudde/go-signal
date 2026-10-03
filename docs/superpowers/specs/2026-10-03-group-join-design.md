# Invite-link group joining

Date: 2026-10-03

Status: written spec and implementation plan approved on 2026-10-03.
Implementation, offline checks and independent whole-change review complete;
PR shipping pending. Live acceptance is tracked separately and has not been run.

## Intent and scope

Continue the Phase 4 group-management work under `PLAN.md`'s “Later / on demand”.
At baseline `930de7e`, PR #23 delivered avatar updates and Phase 4.2 left joining open.
The user approved the proposed invite-link joining approach and useful subagents.

Add `groups join <link>` for a selected linked account. An open link adds that
account as an ordinary member; a link requiring administrator approval submits
a request and reports `requesting`. Existing full membership and an existing
request are successful no-ops when established by fresh server evidence. Support
the cgo and `libsignal_go` backends and the existing no-backend fallback.

One invocation submits at most one membership PATCH. Do not retry conflicts,
refresh credentials and resubmit, or change a failed request into another kind
of membership operation. Accepted and uncertain outcomes must be distinguishable
from definite pre-submission failures. No result includes an invite password,
master key, credential or the supplied link.

Invitation acceptance, PNI invitation handling, request cancellation, a separate
preview command, avatar downloads, MCP/daemon tools and remote storage-service
writes are deferred. Production requests are outside this implementation batch.
Offline verification can close implementation criteria; server acceptance and
peer-phone behavior remain open with an opt-in live procedure.

## Architecture choice

Use a narrow extension to `cwbudde/mautrix-signal` exposing invite preview and
one-shot joining. Its pinned version, `v0.2609.0-purego.9`, has private password
PATCH support but no public preview/join API. `EncryptAndSignGroupChange` submits
its own password-free PATCH and cannot serve as a join-action encoder.

The fork owns group authorization, the existing production public parameters,
credential presentation, encrypted attributes, protobuf actions and signed-change
validation. Keep those details there; do not copy public parameters or implement
crypto in go-signal. The alternative of facade-owned HTTP would duplicate these
details. Waiting for upstream would leave the CLI feature unavailable.

This deliberately extends the fork beyond its current libsignal-shim scope.
Update the fork documentation and go-signal maintenance description accordingly.
Keep the existing upstream and libsignal pins. Work on isolated branches, verify
the change, publish a new `v0.2609.0-purego.N` tag on the fork's `purego` branch
following its maintenance policy, and pin that immutable tag in go-signal.
No `replace` directive or local-path dependency may land. Select the next unused
tag after checking remote tags; never move an existing tag or force-push.

Layering remains `cmd` → `internal/app` → `internal/signal`, with
`internal/output` rendering facade-owned types. Fork types remain inside the
facade. No database migration is required.

## Facade and application contract

Add `Client.JoinGroup(ctx context.Context, link string) (GroupJoinResult, error)`.
The string is sensitive input. It is parsed again at the facade boundary even
when a caller performed preflight. Do not resolve it as a title or group reference.

Expose facade-owned types:

```go
type GroupJoinStatus string // "member" or "requesting"; empty when unverified

type GroupJoinResult struct {
    ID       string
    Title    string
    Revision uint32
    Status   GroupJoinStatus
    Changed  bool
    Accepted bool
    Verified bool
}
```

`Changed` and `Accepted` are true only after a successful mutation status.
`Verified` means fresh full state proved membership, or fresh preview/signed
response proved the pending request. A no-op has both mutation flags false and
`Verified=true`. Before verification, leave `Title` and `Status` empty. A partial
result can contain ID and revision alongside an error; ID alone is never proof
of membership. The revision on an uncertain failure is the attempted revision,
which the accompanying error must explicitly label as such.

An accepted direct join can be followed by a concurrent removal. Return the
accepted result with a verification error and no purported membership if the
fresh fetch no longer proves membership. A later verified revision is permitted;
do not require the fetched group to equal the submitted snapshot.

Add `app.GroupsJoin` with a typed request containing the link. Preflight runs
before CLI account opening and before application connection. Connect in
send-only mode, call the facade once, and preserve the result and error.
Use the existing account lock, operation/Close lifecycle, cancellation,
closed-client, connection-loss and remote-unlink semantics. Group authorization
refusals must not be mistaken for device unlinking.

## Input validation and secrecy

Accept `https://signal.group/#<fragment>` and `sgnl://signal.group/#<fragment>`,
with the root path either empty or `/`. Scheme/host comparisons are case
insensitive. Reject credentials, ports, queries, unrelated hosts/paths, missing
fragments and whitespace inside the URL. Trim surrounding whitespace only.
Limit the entire input to 4096 bytes before parsing or decoding.

Decode URL-safe base64, accepting padded and unpadded forms, and protobuf v1
invite contents. Require exactly 32 master-key bytes and 16 password bytes.
Reject unsupported versions, malformed/truncated protobuf, multiple/conflicting
contents fields and malformed secret lengths. Own the prepared byte slices.
Provide a secret-free validation helper for CLI/app preflight.

Neither errors nor verbose logs may print input URLs, decoded keys, passwords,
presentations, authorization headers, response bodies or password-bearing request
paths. Preserve sentinel/cancellation identity through wrappers with redacted
`Error()` text. Cover Cobra usage/errors and dependency logging, not only output
renderers. Sanitization must also cover errors containing a nested `url.Error`.

## Fork preview and join transport

Expose these narrow fork contracts, using existing fork types for keys/access:

```go
type GroupJoinPreview struct {
    Revision             uint32
    Access               AccessControl
    PendingAdminApproval bool
    Title                string
    Description          string
}

type GroupJoinOutcome struct {
    Attempted    bool
    Accepted     bool
    Verified     bool
    Revision     uint32
    Requesting   bool
    GroupContext *signalpb.GroupContextV2
    Change       *GroupChange
}

func (cli *Client) PreviewGroupJoin(ctx context.Context,
    key types.SerializedGroupMasterKey, password []byte) (GroupJoinPreview, error)
func (cli *Client) JoinGroupOnce(ctx context.Context,
    key types.SerializedGroupMasterKey, password []byte,
    preview GroupJoinPreview) (GroupJoinOutcome, error)
```

`Attempted` records submission rather than validation/credential work; it allows
the facade to classify transport uncertainty without guessing from error text.
`Verified` on this fork outcome means the signed response passed validation, not
that a full-state fetch occurred. Context/change are populated only after that
validation, owned by the result, and remain private to facade implementation.
The request kind reflects the submitted action; only a verified outcome proves
it. Errors retain their underlying identity while rendering without secrets.

Preview uses authenticated `GET /v2/groups/join/<password>`; joining uses
`PATCH /v2/groups/?inviteLinkPassword=<password>`. Both encodings are URL-safe
base64 without padding. Use existing group authentication with the supplied
master key. Validate that preview public parameters match the derived group,
decrypt title/description using existing attribute primitives, and check their
expected oneof and UTF-8. Require a valid response `X-Signal-Timestamp` header.

Treat disabled/unknown access conservatively. Map documented inactive/banned
preview refusals to secret-free errors; HTTP 423 represents a terminated group.
HTTP 409 on mutation is a conflict with no retry. Unknown forbidden-reason values
must not invent a more specific diagnosis. Never echo arbitrary header/body text.

For direct membership, create one self expiring-profile-key credential presentation
with role DEFAULT; leave `joinFromInviteLink` false as the official request
builder does. For approval, create one self pending-admin-approval action with a
presentation only: no role, timestamp, user ID or profile-key fields. Set the
request source to the plaintext self ServiceID bytes, leave `group_id` unset
(the server supplies it), and set revision to fresh preview revision + 1.
Reject revision overflow. Own credential failure is a pre-PATCH
failure; there is no pending-profile-key invitation fallback for joining oneself.

The joining helper receives the fresh preview policy/revision and performs one
PATCH. It does not call ordinary `UpdateGroup`, which fetches inaccessible full
state and has credential retries. Ordinary group mutations retain their current
behavior; new transport code must not silently alter those operations.

Use a dedicated secret-safe request path, or an explicitly redacting extension
of the HTTP helper. The current `web.SendHTTPRequest` logs full URLs and client
errors. Copy the configured HTTP client to reject redirects without modifying
the shared client. Use a fixed production storage host and existing TLS,
authentication, content-type and agent headers. Do not install a separate
transport, weaken TLS checks, or use websocket auto-retry helpers.

Bound new preview/PATCH response bodies to 1 MiB with limit-plus-one reads, close
every body, and reject oversized, missing or malformed required messages. This is
a local defensive limit, not a claimed Signal protocol limit. Do not claim a
bound on unchanged dependency readers.

Record acceptance immediately on HTTP 200, before reading or decoding its body.
After that, a read, signature or semantic validation failure is an accepted
follow-up error. Verify the returned change's server signature, exact group-ID
binding, supported epoch, expected revision and self membership/request action
using the fork's existing verification/decryption primitives. Add the group-ID
check explicitly: the current decrypt helper verifies the signature but omits
this binding. Check decrypted meaning, since the server can normalize the
presentation into encrypted identity/profile-key fields and set the invite-link
marker. The signed response source is an encrypted ServiceID, unlike the request
source; require its decrypted identity to match the selected account's ACI.
Reject unrelated actions or contradictory membership before propagating
the signed change or claiming a verified status.
Do not propagate unvalidated response data as a notification.

Transport failures without a success status, unexpected success codes and server
failures after submission are uncertain. Return an inspect-before-retry error
without claiming acceptance. Documented explicit rejection codes remain definite
rejections. A pre-PATCH failure is never uncertain. No failure triggers another
PATCH, redirect or background recovery request.

## Orchestration and retained state

Derive the group identifier without requiring an existing local group. If the
store already has its master key, evict the in-memory cache and attempt a fresh
full-state read. Verified self full membership returns a no-op even if the link
has since been disabled; existing membership does not depend on link activity.
Nonmembership/unknown-group refusal permits the preview flow. Other fetch errors
stop the operation. If fetched state identifies self as invited, return a clear
invitation-acceptance-required error before preview/PATCH; this operation does
not convert an existing invitation into invitation acceptance.

Fetch fresh preview for nonmembers. An existing request returns a verified no-op.
Otherwise authorize only recognized open/approval policies and reject overflow.
Persist the validated master key after successful preview and before a possible
PATCH (also for the pending-request no-op). Failure to persist stops before PATCH.
This retains an inspectable group after uncertain outcomes. Storing a key does
not assert membership, erase a left marker, or write preview data as full state.

After a validated approval response, return `requesting` without requiring a
full-group fetch: a requester can receive 403 on that endpoint. After a direct
join, evict cache and fetch authoritative full state, requiring self full
membership before returning `member`. Cache only verified full state using the
existing title cache. Cleanup evicts transient cache entries on every path.

Notify other members after a validated direct change, using the accepted signed
change and verified recipient state through the existing `SendGroupUpdate` path.
Retain the accepted ID/revision on fetch/cache/notification failure and never
retry the membership mutation. Check notification outcomes rather than relying
on logging alone. Approval requests cannot send ordinary member notifications;
the server/admin workflow handles them. No claim of storage-service sync or
other-device persistence is made.

All stages use the caller's context; cancellation does not start detached
follow-ups. Retained keys allow subsequent `groups show` after direct joining or
later approval. While approval is pending, show/list can report nonmembership;
they do not reconstruct pending status from a stored key. Inspection guidance
for approval/uncertain requests points to an administrator or the phone as well
as explaining that `groups show` may be unavailable until approval.

## CLI, fake and output

Register `groups join <link>` with exactly one positional argument. Joining or
requesting is the explicit command action; no additional confirmation flag is
required. Help explains direct/request outcomes, no retries and link secrecy.

Successful plain output distinguishes `Joined group`, `Join requested`,
`Already a member` and `Already requested`, followed by ID, escaped title and
revision. JSON is `{version, groupJoin: {id, title, revision, status, changed,
accepted, verified}}`. Schema version 1 remains valid because this document is
additive. On error, stdout stays empty; stderr explains accepted/uncertain state
and inspection. Generic group/MCP output retains its existing secret policy.

Extend the account-aware fake to model valid links independently from stored
membership, fresh access/password checks, direct/request actions, both no-ops,
key retention and definite/uncertain/accepted follow-up failures. Clone caller
input where retained. Mirror account selection, locking and connection behavior.
Do not make stored-key existence imply membership in the fake.

## Verification, planning and shipping

Use tests first and external test packages, with narrow export-test adapters.
Fork tests cover exact endpoint/password/action bytes, signature and semantic
validation, credentials/overflow before PATCH, timestamp and preview validation,
response bounds, redirect refusal and secret-free logs/errors. Test successful
status followed by malformed/body-read failure separately from uncertain
transport/5xx and definite 403/409 outcomes. Count PATCH calls to prove no retries.

Facade/app/fake tests cover account selection, fresh member/request no-ops,
preflight before account opening/Connect, retained keys before uncertain writes,
failed persistence preventing PATCH, pending requests despite full-fetch refusal,
direct fresh membership validation, cancellation, unlink and accepted follow-up
failures. CLI plain/JSON goldens cover all four success outcomes and empty stdout
on failure. Include secret-bearing malformed inputs and nested URL errors.

Run the fork's scoped cgo and pure-Go suites and backend API parity checks before
publishing its tag. Independently verify the pinned integration with `just fmt`,
`just check`, `just check-purego`, no-cgo/no-backend-tag tests, `just build` and
`just docs-gen`. Check all changed paths, dependency integrity and no `replace`.
Only repeat broader checks after relevant changes or failures.

The controller owns go.mod/go.sum, PLAN.md, documentation, history and shipping.
Implement the shared contract first; then reassess disjoint backend versus
app/CLI work for subagents. Request independent review before shipping. Follow
the fork's branch/tag policy and create one go-signal PR for the integrated work.

Update README, JSON docs, maintenance docs and PLAN.md with verified behavior.
Keep joining live acceptance unchecked. Document an opt-in procedure with two
disposable linked accounts and groups: both link policies, repeated/no-op joins,
admin approval, denied/banned/disabled/reset links, conflict/failure inspection,
notifications and phone membership on both backends. Never silently relax live
criteria because offline checks pass.

## Evidence

- Baseline `930de7e` `PLAN.md:787` leaves joining open. Existing group operations
  establish the facade/app/output boundary and accepted-versus-uncertain policy.
- Pinned signalmeow `groups.go:352`, `:731`, `:743`, `:1169`, `:1379`, `:1452`
  expose auth/storage/verification while keeping action preparation/password
  transport private. `misc.go` owns production parameters. `web/web.go:104–154`
  logs request URLs and errors. Its `Groups.proto` includes preview/request types.
- Read-only signal-cli `GroupInviteLinkUrl.java`, `GroupV2Helper.java:98–107` and
  `:446–470`, and `GroupHelper.java:406–434` establish parsing, own credentials,
  direct/request selection and requester limitations. Never edit that submodule.
- Official [Signal-Android PushServiceSocket.java](https://raw.githubusercontent.com/signalapp/Signal-Android/b377bd213ad370dea96c46f75491b598983ebf7f/lib/libsignal-service/src/main/java/org/whispersystems/signalservice/internal/push/PushServiceSocket.java),
  inspected 2026-10-03, defines password URL encoding, preview timestamp and
  refusal handling. [GroupsV2Operations.java](https://raw.githubusercontent.com/signalapp/Signal-Android/b377bd213ad370dea96c46f75491b598983ebf7f/lib/libsignal-service/src/main/java/org/whispersystems/signalservice/api/groupsv2/GroupsV2Operations.java)
  supplies the direct/request action construction. Record source revisions in
  protocol tests when implementing so future drift is reviewable.
