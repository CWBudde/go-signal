# Polls

Poll sends support exactly one chat per command. Use `--group` with its canonical base64 ID
from `groups list`, or `--recipient` with an ACI, E.164 number, `@username` or `self`.
Do not combine or repeat these destination flags. `self` sends an own-device transcript.
Creation, voting and closing report delivery per direct recipient or group member; partial
failures return a nonzero exit code and are not retried automatically. Delivery does not prove phone rendering.

```sh
go-signal polls create --recipient '+15550101' --question 'When?' --option 'Today' --option 'Tomorrow'
go-signal polls vote --recipient '+15550101' --target '<creator-aci>:<creation-timestamp>' --option 0
go-signal polls close --recipient '+15550101' --target '<creation-timestamp>'
go-signal polls create --group '<group-id>' --question 'When?' --option 'Today' --option 'Tomorrow'
go-signal polls create --group '<group-id>' --question 'Pick one' --option 'Yes' --option 'No' --single-choice
go-signal polls vote --group '<group-id>' --target '<creator-aci>:<creation-timestamp>' --vote-count 1 --option 0
go-signal polls vote --group '<group-id>' --target '<creator-aci>:<creation-timestamp>' --vote-count 2 --clear
go-signal polls close --group '<group-id>' --target '<creation-timestamp>'
```

Creation allows multiple selections by default. There must be 2–10 nonblank options and a
nonblank question, each at most 100 UTF-16 code units. A character outside the basic multilingual
plane, including most emoji, uses two units. Text is preserved as supplied; duplicate answer
text is permitted. Options keep their order and use zero-based indexes, so `0` selects the
first option. Repeat `--option` for multiple selections; duplicate indexes are rejected.

`--target` for voting accepts an ACI, number, `@username` or `self`, followed by the original
poll timestamp in milliseconds. JSON creation output includes `targetAuthor` and
`targetTimestamp`. Plain delivery output prints the creation timestamp; use your account's ACI
from `account show` for its author. Only the account that created a poll can close it.
The poll's original options need not be stored locally to send a vote; the receiving phone
validates it against the original poll.

Omit `--vote-count` to allocate the next positive uint32 counter automatically, starting at
1 for a poll with no local counter. It orders this account's changes, including withdrawals;
it is not a tally. The counter is stored per account, chat, creator ACI and creation timestamp.
Reservations are atomic and durable before submission: failed and partial sends consume them,
so a later command uses a larger counter. Reopening the CLI or pruning the inbox does not reset
it. Allocation at the uint32 maximum fails before sending.

Valid own-device votes observed by ordinary `receive`, daemon or MCP receiving advance the
local maximum before delivery is acknowledged. Other voters do not affect your counter.
Send-only commands leave incoming votes unread on the server. Local counters cannot guarantee
they exceed activity on devices whose votes have not been received here, including activity
before upgrading or linking. Receive pending own-device transcripts before voting, or supply
an explicit positive `--vote-count` greater than the other devices' latest counter. An explicit
counter is sent unchanged and raises the local maximum; it never lowers that maximum. Older
explicit counters may be ignored by receivers. Explicit zero is rejected. Withdrawal requires
`--clear` instead of `--option` and allocates a counter just like a selection change.
JSON `voteCount` reports the reserved or explicit value even for partial delivery failures.

Polls are standalone content and use their own command group. The [MCP poll tools](mcp.md#polls)
and [daemon poll routes](daemon.md#poll-operations) expose create, vote, close and show while
the server keeps receiving. They share the same app validation, allowlist and counter policy.
Incoming polls, votes and closures from any chat and your other devices appear in `receive`.
Creation is a `message` with `poll`; controls are `pollVote` and `pollClose` events.
Malformed poll content is reported as `unsupported` with `content:"invalidPoll"`.
The [JSON reference](json.md#polls-create-polls-vote-and-polls-close) describes these additions.

## Retained results

The daemon and MCP server collect received events in the local inbox. Stop that process to
release the account lock, then inspect a poll without connecting or sending:

```sh
go-signal polls show --recipient '<chat-aci>' --target '<creator-aci>:<creation-timestamp>' -o json
go-signal polls show --group '<group-id>' --target '<creator-aci>:<creation-timestamp>' -o json
go-signal polls show --group '<group-id>' --target '<creator-aci>:<creation-timestamp>' --scan-limit 10000
```

`show` requires a canonical creator ACI and either a canonical chat ACI or group ID; it
does not resolve numbers, usernames, `self` or group titles. For note-to-self use the selected
account’s ACI as the chat ACI. Chat identity is independent of the poll creator. Ordinary
`receive` prints and acknowledges events without saving them in this inbox, and outgoing sends do not automatically save their own poll events. To collect an
outgoing poll's creation, receive its own-device transcript if supplied by another device.
Do not expect a poll sent by this CLI to appear in `show` immediately.

The view scans the latest 1,000 retained entries in that chat by default, with a maximum of
10,000. All chat events count toward the limit. Output reports entry bounds, scanned count and
whether older retained entries were excluded. Retention pruning or events never received here
can remove creation, votes or closure even when the scan is not truncated. Completeness is
always `unknown`. Absence of a closure does not establish that the poll is open; absence of
creation does not establish that it never existed.

With known creation, the observed tally counts each voter's latest valid selections. Higher
vote counters replace lower ones; equal counters retain the first observation except a later
own-device sync timestamp can replace an equal counter. Empty selections withdraw a vote.
Invalid selections and conflicting observations are counted in the output. A closure from
the creator stops subsequent vote reduction. An observed remote deletion by the creator
suppresses totals. Without creation, retained votes and closure are shown but no tally is
computed. These are retained observations, not authoritative server results.

## Durable results

Add `--durable` to read a stored projection independently of the inbox scan and retention:

```sh
go-signal polls show --recipient '<chat-aci>' --target '<creator-aci>:<creation-timestamp>' --durable -o json
go-signal polls show --group '<group-id>' --target '<creator-aci>:<creation-timestamp>' --durable
```

Ordinary `receive`, daemon and MCP receiving now persist poll creation, votes, creator closures
and deletions before acknowledging them. A persistence failure leaves the event unread for
redelivery. Repeated identical poll evidence is counted once, even if contact metadata changes.
Outgoing submissions do not add observations; receive their transcripts when available.
`--durable` cannot be combined with `--scan-limit`. The default bounded inbox view is unchanged.

On first use, the account's still-retained inbox seeds projections in entry order before new
poll evidence is added. This happens on the first received/stored poll event or durable view.
Events pruned before that bootstrap cannot be recovered. New projections survive later inbox
pruning, process restarts and unrelated chat traffic. A late creation revalidates older votes;
only valid selections contribute to the tally. Creator closures and deletions apply as in the
bounded reducer. A closure is keyed to its sender as the creator, so another sender's closure
cannot close this poll. Completeness remains `unknown`: no local view establishes unseen votes
or that a poll is open.

JSON adds `source:"durable"` and a positive `observations` count when evidence exists. The inbox
scan fields are zero and `truncated` is false because this view reads materialized state without
an inbox scan limit. Plain output reports distinct durable observations. Poll-only evidence is
kept alongside the projection to preserve arrival-order decisions and late-creation validation;
it grows with unique poll observations, and each update replays evidence for the affected poll.
General inbox pruning does not delete it. Account removal deletes it with the account database.
A remote deletion suppresses the tally; it does not erase the local evidence archive.

Live phone rendering, voting/withdrawal, closing and device sync remain acceptance checks on
both backends. See the separately opt-in [live procedure](dev.md#poll-live-check).
