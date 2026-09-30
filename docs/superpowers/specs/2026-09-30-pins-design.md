# Pin/unpin sends and retained observations

The user approved sends to users, groups and self plus a bounded offline list, following the poll approach. This batch implements the next ungated feature in PLAN.md Later / on demand (pin/unpin operations and received pinned-message state). Stories require separate fork transport changes; live phone acceptance remains separately opt-in.

## Behavior

`pins add <recipient>... --target <author>:<timestamp> (--duration <seconds>|--forever)` sends a standalone pin control. `pins remove <recipient>... --target <author>:<timestamp>` sends an unpin. Both accept repeatable `--group <id>` and ordinary users/self, reuse send-only transport, resolution, allowlists, unique timestamps, device sync and partial outcomes without retries. Target authors accept an ACI, number, username or self. All syntactic checks happen before opening a client. Explicit nil ACIs are rejected before opening; resolved nil ACIs fail before any sends.

Pin duration is exactly one of strictly positive uint32 seconds or forever. No default duration; neither zero nor a false forever value is a duration. The wire selects one duration branch. Target timestamp is positive uint64 milliseconds. Story pinning is excluded.

After recipients are resolved and allowlists checked, fetch every distinct target group's current state and the own account, enforcing full membership and group attribute-edit permission (admin or MembersCanEditAttributes). All these checks and target-author resolution finish before any sends. Failed checks send nothing. Ordinary group transport constraints still apply. Other clients ultimately decide whether a target exists, belongs to that chat, is eligible and remains pinned; successful transport is not confirmation of phone state. No target message is required locally.

## Facade and receive

Extend SendRequest with Pin *OutgoingPin and Unpin *OutgoingUnpin. OutgoingPin has TargetAuthor Recipient, TargetTimestamp uint64, DurationSeconds uint32 and Forever bool; OutgoingUnpin has only the target reference. Payload Check methods validate target and duration. Each operation must be standalone: no ordinary content, edit, reaction, delete, sticker, poll or opposite operation. Users/groups/self are supported. Pin/unpin does not raise requiredProtocolVersion.

Received Pin and Unpin are envelope controls embedding their outbound payloads. Convert a raw 16-byte non-nil author ACI and positive target timestamp. Reject missing/zero/false duration, multiple pin operations, mixed ordinary/control content and edited pin messages as Unsupported type invalidPin. Legitimate group/profile/timer metadata is allowed; control flags and story context are rejected. Handle interactions with poll conversion so neither mixed payload is silently lost. Pinned signalmeow's existing receive routing boundary remains unchanged, including interception of group-call updates.

Persist both typed controls in the current inbox codec with no database migration. They are controls, never unread messages or candidates for automatic read receipts. Fake Send/Sent defensively copy the new payload pointers while preserving existing unrelated request behavior. JSON receive event types are pin and unpin with targetAuthor/targetTimestamp; pin also carries durationSeconds/forever. Schema version remains 1 (additions only).

## Retained list

`pins list --chat <canonical-ACI|group:canonical-base64-ID> [--scan-limit <n>]` reads one local inbox snapshot without connecting or resolving users. Default scan limit 1000, maximum 10000, request zero selects default. Reject noncanonical/nil ACI and noncanonical group references before opening. Stop daemon/MCP first because the account lock is required. Ordinary receive and outgoing sends do not populate this history.

Fetch newest limit+1 chat entries once, sort by inbox ID, discard the oldest extra entry and report scanned count, first/last IDs and truncation. Reduce matching chat events in ascending ID order. For each target retain the latest valid distinct pin/unpin observation, preserving sender, sender timestamp, inbox ID and local receipt time. Sort final observations by target author then target timestamp. Do not reorder controls by sender timestamps. A retained creator Delete sets TargetDeleted for that target even if another pin arrives later. Edits and unsupported admin deletes are not projected.

Within the scanned snapshot, deduplicate controls by operation sender ACI and sender timestamp. Identical repeated payloads keep their first receipt and do not extend expiry; differing payloads with the same identity increment Conflicts and retain the first valid observation. Invalid typed controls (including nil/noncanonical sender/target, zero envelope/target timestamp, invalid duration and missing receipt time) increment IgnoredInvalid and never replace valid state. Conversion-stage invalidPin has no target payload and increments IgnoredInvalid for the selected chat. Events from another chat are ignored defensively.

For finite pins, ExpiresAt = inbox ReceivedAt + DurationSeconds seconds. ExpiryReached compares it with one captured App clock value; forever and unpin have no expiry. This is this client's receipt-based observation, not synchronized phone expiry. Retain unpins, expired observations and deletion flags in the list; never assert an authoritative active set. Completeness is always unknown. Do not imitate phone eviction at three pins: unseen events, membership, eligibility, disappearing timers and phone limits are not available. No durable projection, migration, automatic outgoing history, permission reconstruction, MCP or daemon write tools.

## Output, tests and acceptance

Plain sends reuse the send outcome table. JSON sends expose version and pin object with normal send outcomes, operation, target reference and add-only duration fields. Plain list explicitly calls results retained observations and states unknown completeness; JSON exposes pinState with chat, observations, scan bounds, truncation, ignoredInvalid and conflicts. Escape user-controlled names in plain output with existing helpers.

Tests must cover literal wire fields and oneof ownership; sparse/malformed/mixed/edited inbound messages; both incoming chat kinds and sync; legacy inbox decode plus real SQLite close/reopen; fake payload ownership; syntactic preflight and author resolution; allowlist order and fresh group permissions before all mutations; partial member/recipient outcomes without retries; bounded offline query and cancellation; arrival order and duplicate expiry preservation; invalid events, deletes, forever/unpin/expiry boundary; plain/JSON command goldens and additive receive output. Run just fmt, just check, just check-purego and no-cgo/no-backend-tag tests. Keep live acceptance unchecked and document a disposable-account procedure for both backends.

## Primary evidence

Pinned fork SignalService.proto:397 and read-only reference/signal-cli ManagerImpl:1150–1193 define the wire and normal message transport. The reference checkout has an existing user change and is never edited.

Official Desktop pin modifier checks group attributes and uses local receipt time: https://github.com/signalapp/Signal-Desktop/blob/abe80d32445e53b047b42d10c5b751c4fbfbbfc0/ts/messageModifiers/PinnedMessages.preload.ts#L190

Official Android storage also anchors expiry to receipt: https://github.com/signalapp/Signal-Android/blob/d8d36376f480eb4669efe8bfee94b2c196a9f7bd/app/src/main/java/org/thoughtcrime/securesms/database/MessageTable.kt#L3957

Protocol version remains zero for standalone pins: https://github.com/signalapp/Signal-Desktop/blob/abe80d32445e53b047b42d10c5b751c4fbfbbfc0/ts/textsecure/SendMessage.preload.ts#L594

Official feature limits and permission description: https://support.signal.org/hc/en-us/articles/10270961459226-Signal-Pinned-Messages
