# Inherit disappearing-message timers

Date: 2026-10-03. Baseline: `9e1781d`, after merged PR #26.
Status: conversational design approved; written spec awaiting review.

## Intent and scope

Continue the first item in PLAN.md Phase 12. Messages sent by go-signal should
inherit the selected chat's known disappearing-message timer. This applies to
CLI, MCP and daemon sends through the existing Signal facade, on both cgo and
pure-Go backends. Direct-chat settings must survive restarts and remain isolated
between accounts. Group messages must use the group's authoritative settings.

The user chose Phase 12 implementation and deferred Phase 11 live link testing.
Deliver one go-signal PR, with a reviewed fork release if needed, offline tests
and documentation. Keep phone acceptance open until it has actually run.

This change adds no timer-setting command, local message deletion, expiry-start
tracking, universal default timer policy or public event/output fields. Existing
timer-update events remain `Unsupported{Type: "expirationTimerUpdate"}`. Existing
JSON meanings and SchemaVersion remain unchanged.

## Architecture and alternatives

Use an account-local direct-chat timer table in `internal/store`, learn settings
from raw events in `internal/signal`, and stamp each outgoing recipient's cloned
DataMessage. Extend the pinned mautrix-signal fork to preserve
contact-sync timer metadata in its ContactList event, and attach the group timer
where SendGroupMessage already attaches the authoritative group context.
Also propagate receive-handler failures through sent sync and contact-sync
processing so these timer sources obey the persistence-before-ack contract.

A facade-only implementation loses settings from initial contact sync because
the fork currently discards those protobuf fields. Putting direct timers in the
inbox misses ordinary receive callers and can commit after envelope delivery.
Fetching group state separately in the facade duplicates the fork's fetch and
can stamp a timer from a different revision. The selected boundaries address
these gaps without changing the public Client interface or SendRequest.

## Persistent direct-chat state

Add the next go-signal account database migration, after version 6. Each row is
keyed by a canonical, nonzero recipient ACI and stores timer seconds and timer
version as nonnegative integers covering the uint32 wire range. Account selection
already chooses the containing database; no cross-account registry is added.
Note-to-self uses the selected account's own ACI through the same table.

An absent row means unknown. A row with zero seconds explicitly disables expiry;
retain that row and its version. Use an atomic conditional upsert: insert if the
row is absent; otherwise update only when the incoming version is strictly
greater. Equal-version conflicts and older versions do not change state. A
newer version advances the stored version even when the duration is unchanged.
Do not copy signal-cli's early return for identical durations, which would lose
the newer high-water mark. Reads, merges and batch merges return storage errors.

For legacy unversioned message updates, treat a missing version as version zero.
Such an update can initialize an unknown chat, but cannot replace any existing
row, including another version-zero row. This deliberately conservative policy
prevents unordered legacy updates from replacing newer state. Contact sync has
stricter presence requirements, described below.

## Learning settings before acknowledgement

Inspect raw DataMessage events, nested EditMessage.DataMessage events, and sent
sync transcripts before converting to facade events or emitting to Events().
A direct message is eligible when it has a body field (including an explicitly
empty body) or the EXPIRATION_TIMER_UPDATE flag. Ignore group messages here:
their timer authority is decrypted group state, not message metadata. Bodyless
reactions, deletes, pins, polls, attachment-only and other controls do not learn
chat settings merely because they carry timer fields.

For eligible direct messages:

- If both timer fields are absent and the timer-update flag is absent, do not
  synthesize a reset from protobuf getters returning zero.
- If seconds are absent but a version or timer-update flag is present, seconds
  are zero: a versioned or explicit update can disable expiry through wire defaults.
- If the version is absent, apply the legacy version-zero rule above.
- For ordinary incoming messages, identify the chat by sender ACI. For sent sync
  transcripts, identify it by the destination ACI, including note-to-self. Never
  learn a peer's setting under the selected account's ACI merely because that
  account sent the original message. Reject missing, PNI-only or invalid ACI keys.

The fork's ContactList event gains additive timer metadata associated with each
successfully converted contact ACI. Preserve pointer presence for seconds and
version, with owned values; do not rely on getter defaults or slice positions.
The fork emits metadata only after the contact transaction succeeds. The facade
learns contact-sync entries only when both fields are present. Missing fields
leave existing settings untouched; an explicit zero with a newer version is a
valid disable. Ignore timer metadata from IsFromDB storage-contact events.
Merge a contact event's timer entries in one transaction, before notifying
Sync waiters that the contact list arrived.

Persist eligible settings before event delivery and acknowledgement. A storage
failure must return false from the raw handler, emit no converted event, and
leave the envelope available for redelivery. Do not signal successful contact
sync completion on failure. Repeated delivery is safe because merges are
idempotent. Keep handling inside the existing operation guard so Close cannot
close the database during persistence.

The fork currently ignores incomingDataMessage/incomingEditMessage return values
for sent sync transcripts. Forward their handler-success result from
handleSyncMessage for both envelope forms. For contact sync, attachment download,
decode and contact transaction failures must also return false rather than log
and acknowledge a successful sync. Stop at each failed stage; emit no partial
ContactList. The existing final event-handler result remains authoritative.

Send-only mode may learn settings from received raw events before declining
delivery of message events. Preserve its existing acknowledgement policy:
message events stay on the server; successfully stored contact lists can be
acknowledged as today. Learning queued events does not promise they have arrived
before a concurrent send starts.

## Outgoing direct and group messages

For each direct recipient, resolve its ACI and read the account-local timer, then
stamp a fresh DataMessage clone before wrapOutgoing constructs the ordinary or
edit envelope. Stamp both ExpireTimer and ExpireTimerVersion, including explicit
zero. Preserve learned versions. Unknown chats use zero seconds and version one,
matching the existing reference sender's default; do not persist this fallback
as learned state. Timer fields apply to all outgoing DataMessages, including
edits and controls built by this send path. Typing and receipt envelopes are
unaffected.

Read settings separately for each recipient in a multi-recipient request. A
recipient's timer must not mutate the shared template or another recipient's
message. Preserve existing timestamps, delivery results, rich content and
note-to-self sync behaviour. If timer lookup fails, report that recipient's send
error and skip sending to it; continue other recipients under existing partial
result semantics. Never silently send with zero after a read failure.

In fork SendGroupMessage, use the same retrieved group object for GroupV2 context
and ExpireTimer on ordinary and nested edit DataMessages. Group timers carry
seconds, including zero, and no direct-chat ExpireTimerVersion; clear any such
version from supplied content. Retain the existing group cache revision rules
and fetch errors. Group messages do not consult the direct-chat table. Full and
incremental decrypted group state already retain timer duration, so no second
timer cache or group table migration is needed.

## Tests and failure boundaries

All go-signal tests use external `_test` packages and narrow test exports where
needed. Verify behaviour at the store, raw-handler and actual protobuf-building
boundaries rather than relying on the request-recording fake alone.

- Store: absent versus explicit zero, strict version ordering, equal-version
  conflicts, same-duration version advancement, legacy version zero, full uint32
  range, batch atomicity, restart persistence and two-account isolation.
- Receive: messages, explicit updates, edits, destination-bound sent sync and
  note-to-self; absent fields; ignored bodyless controls and group metadata;
  contact-sync presence, ordering and IsFromDB exclusion.
- Failure handling: deterministic persistence failure prevents event emission,
  acknowledgement and contact completion; redelivery succeeds safely; Close and
  send-only semantics remain intact.
- Direct send: independently stamped recipient clones, explicit disable,
  restart, note-to-self, edit/control content preservation and lookup failure
  preventing transmission to that recipient.
- Fork: contact timer metadata remains paired with the converted ACI and retains
  zero/presence; failed contact storage emits no successful list; group timer
  and context use one state revision; both ordinary and edit envelopes are
  covered, including zero and removal of a direct timer version; sent-sync
  handler failure and contact-sync processing failure propagate to acknowledgement.
- Output: existing command goldens and Unsupported timer-update conversion
  remain valid; add a focused regression only if existing coverage misses this.

Run targeted failing tests before implementation, then affected package checks
on both backends. Before committing product changes and publishing, the
controller runs just fmt, just lint, full just check, just check-purego and the
no-backend fallback suite. Check normal builds and dependency tidiness. Review
any fork baseline failures separately and run the affected fork checks on both
backends. Original reference and fork checkouts remain untouched.

## Documentation, roadmap and delivery

Document automatic inheritance, explicit-off behaviour and persistence. Explain
that direct timers are learned from phone contact sync and received messages:
receive or sync after changes on another device before relying on a fresh timer.
An unknown chat uses the untimed fallback. Group sends use the backend's retrieved
group state and its existing freshness rules. Avoid implying local messages are
deleted or that every remote setting is synchronously refreshed before sending.

Record offline implementation separately from live acceptance in PLAN.md. Add a
disposable-account phone procedure covering two direct chats with different
timers in one send, disable updates, delayed old updates, restart, note-to-self,
edits and group changes, on both backends. Actual phone expiry checks remain
unchecked. No production Signal traffic runs during this implementation.

Use isolated worktrees, scoped implementation/review subagents where useful and
one go-signal feature branch. Publish the reviewed fork through its established
release workflow with a fresh immutable tag, then update go.mod/go.sum without a
replace directive. Do not assume the next tag is available; check before naming
it. Open the go-signal PR after local checks and independent review; do not merge.

## Protocol evidence

Read-only local sources: the pinned fork is
`github.com/cwbudde/mautrix-signal v0.2609.0-purego.14`.
SignalService.proto defines optional uint32 expireTimer/expireTimerVersion on
DataMessage and ContactDetails. Fork receiving.go retains message/edit timer
fields in events but drops contact timers during conversion; sending.go currently
attaches group context without the group's timer. The existing group cache
already processes decrypted timer duration and rejects stale revisions.
handleSyncMessage currently ignores sent-message/edit handler failures and logs
contact processing errors while its default success result remains true.

The signal-cli reference's IncomingMessageHandler.java learns direct settings
from body-present messages or explicit expiration updates and uses destination
for sent sync. SyncHelper.java requires both contact timer fields and a newer
version. SendHelper.java attaches direct seconds/version and group seconds.
ContactHelper.java rejects stale/equal versions, but also ignores newer versions
with identical duration; this design preserves those newer versions. Legacy
version-zero handling and absent-field reset protection above are explicit
go-signal policies.
