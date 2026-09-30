# Pinned messages

Pin a message for a positive number of seconds or forever, using its author and sent timestamp
from received JSON. The command works for users, groups and Note to Self:

```sh
go-signal pins add +4915112345678 --target +4915112345678:1790000000000 --duration 86400
go-signal pins add --group '<base64 group ID>' --target self:1790000000000 --forever
go-signal pins remove --group '<base64 group ID>' --target '<author ACI>:1790000000000'
```

Recipients use the same syntax as `send`: number, ACI, `@username`, `group:<ID>` or `self`.
Repeat recipients and `--group` to address multiple chats. The target author accepts numbers,
ACIs, usernames and `self`. All target chats should contain the referenced message; go-signal
needs no local copy of it.

`--duration` is a positive integer in seconds, up to 4294967295. Common choices are 86400 (24
hours), 604800 (7 days) and 2592000 (30 days). Supply either `--duration` or `--forever`; there
is no default. Zero, negative values and both flags together are rejected. Removing a pin has
no duration flag. A forever pin can still disappear if its target is deleted or expires.

Group pinning and unpinning use current membership and attribute-edit permissions. You must
be a full member and either an administrator or in a group that allows members to edit
attributes. The command fetches and checks every group's state before any sends. These
checks cannot prevent a concurrent permission change. Recipient allowlists are enforced.

Results show delivery to each target, including individual group-member failures. Partial
failure exits nonzero and is never retried automatically. Inspect the result before retrying.
Successful delivery does not confirm that another device applied the operation: it may reject
an unavailable, ineligible or wrong-chat target. Controls sync to your other devices; incoming
messages stay available for a later receive. Story pinning is not supported.

## Receive and inspect retained observations

`receive -o json` emits `pin` and `unpin` events with their envelope, target author and target
sent timestamp. Pins also include finite seconds or a forever mode. Malformed or mixed pin
payloads delivered to the facade become `unsupported` with `content: "invalidPin"`.
This implementation also rejects zero-second finite pins and false forever values; incoming
controls must use a positive duration or true forever, matching outgoing validation.

Daemon and MCP receiving retain these controls in the account inbox. They do not count as unread
messages and do not trigger automatic read receipts. Ordinary `receive` and outgoing sends do
not populate this history.

After stopping the daemon/MCP receiver to release the account lock, inspect one chat offline:

```sh
go-signal pins list --chat 'group:<canonical base64 ID>'
go-signal -o json pins list --chat '<canonical chat ACI>' --scan-limit 1000
```

The chat must be a canonical, non-nil ACI or `group:` plus a canonical base64 ID. Numbers,
usernames and `self` are not resolved by this offline command; use the account's own ACI for
Note to Self. No connection is made. The default scan covers the newest 1000 retained chat
entries; `--scan-limit` allows at most 10000, and zero selects the default. The count includes
ordinary messages and unrelated controls. The output reports scan bounds and truncation.

The list shows the latest distinct retained pin/unpin for each target, in local inbox order.
It preserves unpins, locally expired pin observations and observed target deletions. Finite
expiry uses the pin's local receipt time, as official clients do. Identical control redelivery
within the scanned snapshot keeps the first receipt time; conflicting controls with the same
sender/timestamp keep the first valid observation and increase `conflicts`. A pruned first
receipt cannot be recovered.

Completeness is always **unknown**. Missed events, pruning, group membership and permissions,
target eligibility and disappearing-message timers can change phone state without a usable
local observation. The list does not apply the phone's three-pin eviction rule or claim an
active pin set. `expiryReached` describes this client's receipt-based clock, not synchronized
expiry on every device. Creator remote deletes set `targetDeleted`; admin deletes and edited
target remapping are not reconstructed. Invalid observations are counted and ignored.

See [JSON output](json.md) and the separately opt-in [live procedure](dev.md#pin-live-check).
No live production operations run as part of the offline tests.

Primary references: [Signal's pinning guide](https://support.signal.org/hc/en-us/articles/10270961459226-Signal-Pinned-Messages),
[Desktop group permission checks](https://github.com/signalapp/Signal-Desktop/blob/abe80d32445e53b047b42d10c5b751c4fbfbbfc0/ts/messageModifiers/PinnedMessages.preload.ts#L190),
[Android receipt-based expiry](https://github.com/signalapp/Signal-Android/blob/d8d36376f480eb4669efe8bfee94b2c196a9f7bd/app/src/main/java/org/thoughtcrime/securesms/database/MessageTable.kt#L3957).
