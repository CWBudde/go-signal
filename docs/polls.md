# Group polls

Poll sends support one group per command. Use its canonical base64 ID from `groups list`.
Creation, voting and closing report delivery per group member; partial failures return a
nonzero exit code and are not retried automatically. Delivery does not prove phone rendering.

```sh
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

`--vote-count` orders successive votes by the same account. Start at 1 and increase it for
every change, including withdrawals. It is a positive uint32 counter, not a tally of selected
options. Coordinate it with activity on your other devices. Older counters can be ignored by
receivers; incomplete local history cannot safely choose the next one. Withdrawal requires
explicit `--clear` instead of `--option`.

Polls are standalone content and use their own command group. Direct-chat and self sending,
automatic vote-counter allocation and poll write tools for MCP/daemon are deferred.
Incoming polls, votes and closures from any chat and your other devices appear in `receive`.
Creation is a `message` with `poll`; controls are `pollVote` and `pollClose` events.
Malformed poll content is reported as `unsupported` with `content:"invalidPoll"`.
The [JSON reference](json.md#polls-create-polls-vote-and-polls-close) describes these additions.

## Retained results

The daemon and MCP server collect received events in the local inbox. Stop that process to
release the account lock, then inspect a poll without connecting or sending:

```sh
go-signal polls show --group '<group-id>' --target '<creator-aci>:<creation-timestamp>' -o json
go-signal polls show --group '<group-id>' --target '<creator-aci>:<creation-timestamp>' --scan-limit 10000
```

`show` requires a canonical creator ACI and group ID; it does not resolve numbers, usernames or
group titles. Ordinary `receive` prints and acknowledges events without saving them in this
inbox, and outgoing sends do not automatically save their own poll events. To collect an
outgoing poll's creation, receive its own-device transcript if supplied by another device.
Do not expect a poll sent by this CLI to appear in `show` immediately.

The view scans the latest 1,000 retained entries in that group by default, with a maximum of
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

Live phone rendering, voting/withdrawal, closing and device sync remain acceptance checks on
both backends. See the separately opt-in [live procedure](dev.md#poll-live-check).
