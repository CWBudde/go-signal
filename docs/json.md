# JSON output

With `-o json` (or `output: json` in the config file, or `GOSIGNAL_OUTPUT=json`), commands write
JSON to stdout instead of text. Logs and errors still go to stderr, so stdout can be piped into
`jq` or a script. Each command prints one document as a single line; `receive` prints one
document per event (NDJSON).

## Versioning

Every document has a top-level `version` field, currently `1`. It changes only when a field is
removed or changes its meaning. New fields can appear in any release, so scripts should ignore
fields they don't know.

## Conventions

- Field names are camelCase.
- Times are RFC 3339 strings in UTC, e.g. `"2026-09-20T12:30:00Z"`.
- Fields marked _optional_ are left out when the value is unknown; all others are always present.
- Accounts and users are identified by their ACI (a UUID); phone numbers are E.164
  (`+4915112345678`).

## `account show`

```json
{
  "version": 1,
  "account": {
    "number": "+15550100",
    "aci": "11111111-1111-1111-1111-111111111111",
    "pni": "22222222-2222-2222-2222-222222222222",
    "deviceId": 2,
    "deviceName": "laptop",
    "linkedAt": "2026-09-20T12:30:00Z",
    "lastSync": "2026-09-25T08:15:00Z"
  }
}
```

| Field        | Type   | Description                                                                |
| ------------ | ------ | -------------------------------------------------------------------------- |
| `number`     | string | Phone number of the account                                                |
| `aci`        | string | Account identity (ACI)                                                     |
| `pni`        | string | Phone number identity (PNI); _optional_                                    |
| `deviceId`   | number | ID of this device within the account (the phone is 1)                      |
| `deviceName` | string | Name this device was linked with; _optional_                               |
| `linkedAt`   | string | When this device was linked; _optional_ (unknown for old data)             |
| `lastSync`   | string | When the last complete contacts/groups sync finished; _optional_ (unknown) |
| `unlinkedAt` | string | When go-signal found this device unlinked (e.g. on the phone); _optional_  |

`lastSync` is recorded after a complete sync during `link` or `account sync`. Incomplete or
failed syncs preserve the previous value. It is omitted until a complete sync has been recorded,
including for older account data without a stored timestamp. MCP's `account_show` tool returns
the same optional field. JSON schema version 1 is unchanged.

## `devices list`

```json
{
  "version": 1,
  "devices": [
    { "id": 1, "lastSeen": "2026-09-25T00:00:00Z", "current": false },
    {
      "id": 2,
      "name": "laptop",
      "created": "2026-09-20T12:30:00Z",
      "lastSeen": "2026-09-25T00:00:00Z",
      "current": true
    }
  ]
}
```

`devices` lists every device of the account, including the phone. Each entry:

| Field      | Type    | Description                                                               |
| ---------- | ------- | ------------------------------------------------------------------------- |
| `id`       | number  | Device ID                                                                 |
| `name`     | string  | Device name; _optional_ (the phone usually has none)                      |
| `created`  | string  | When the device was linked; _optional_                                    |
| `lastSeen` | string  | Day the device last connected (the server keeps only the day); _optional_ |
| `current`  | boolean | `true` for the device go-signal runs as                                   |

## `account unlink`

```json
{
  "version": 1,
  "unlinked": {
    "number": "+15550100",
    "aci": "11111111-1111-1111-1111-111111111111",
    "localOnly": false
  }
}
```

| Field        | Type    | Description                                                                       |
| ------------ | ------- | --------------------------------------------------------------------------------- |
| `number`     | string  | Phone number of the removed account                                               |
| `aci`        | string  | ACI of the removed account                                                        |
| `localOnly`  | boolean | `true` if only local data was deleted (`--local-only`), without the server        |
| `unlinkedAt` | string  | When go-signal found the device unlinked; _optional_ (then `localOnly` is `true`) |

## `account sync`

```json
{
  "version": 1,
  "sync": {
    "contacts": 5,
    "groups": 3,
    "masterKey": true,
    "storage": true,
    "contactList": false,
    "complete": false,
    "missing": ["contact list"],
    "error": "sync incomplete (missing contact list): wait for contact list: context deadline exceeded"
  }
}
```

An incomplete sync (e.g. `--timeout` ran out before the phone answered) is not an error: the
document shows what is missing, a warning goes to stderr and the exit code is 0. The counts are
what the store holds afterwards, including what earlier syncs and received messages stored.

| Field         | Type     | Description                                                                      |
| ------------- | -------- | -------------------------------------------------------------------------------- |
| `contacts`    | number   | Known users with a name or number, not counting the account itself               |
| `groups`      | number   | Groups whose master key is known                                                 |
| `masterKey`   | boolean  | `true` if the storage service key is known                                       |
| `storage`     | boolean  | `true` if the storage service (contacts, groups, blocked list) was fetched       |
| `contactList` | boolean  | `true` if the phone's contact list arrived                                       |
| `complete`    | boolean  | `true` if every part succeeded                                                   |
| `missing`     | string[] | What didn't arrive: `storage key`, `storage service`, `contact list`; _optional_ |
| `error`       | string   | Why the sync is incomplete; _optional_ (only when `complete` is `false`)         |

## `profile show`

```json
{
  "version": 1,
  "profile": {
    "aci": "11111111-1111-1111-1111-111111111111",
    "givenName": "Alice Mary",
    "familyName": "Smith",
    "about": "",
    "aboutEmoji": "",
    "avatarPath": "profiles/example"
  }
}
```

This reads the selected account's fresh server profile. The text fields remain present when
empty; `avatarPath` identifies the existing avatar and is not an image download.

| Field        | Type   | Description                                   |
| ------------ | ------ | --------------------------------------------- |
| `aci`        | string | Selected account identity                     |
| `givenName`  | string | Given name, preserving spaces                 |
| `familyName` | string | Family name                                   |
| `about`      | string | About text                                    |
| `aboutEmoji` | string | Profile emoji                                 |
| `avatarPath` | string | Server path of the current avatar; _optional_ |

## `profile update`

```json
{
  "version": 1,
  "profile": {
    "aci": "11111111-1111-1111-1111-111111111111",
    "givenName": "Alice Mary",
    "familyName": "Smith",
    "about": "Hello",
    "aboutEmoji": ""
  },
  "changed": true,
  "accepted": true,
  "verified": true
}
```

`profile` has the same fields as `profile show`. Omitted update flags preserve text; explicit
empty values clear it. Successful output contains the fetched, verified profile.

| Field      | Type    | Description                                               |
| ---------- | ------- | --------------------------------------------------------- |
| `changed`  | boolean | A changed profile write was confirmed accepted            |
| `accepted` | boolean | The server confirmed accepting the write                  |
| `verified` | boolean | A fresh read confirmed the profile and preserved metadata |

For an unchanged request, `changed` and `accepted` are `false`, and `verified` is `true`.
Failures print no JSON, including failures after acceptance. Read stderr for acceptance or
unknown-outcome guidance and inspect `profile show` before retrying.

## `contacts list`

```json
{
  "version": 1,
  "contacts": [
    {
      "aci": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
      "number": "+15550102",
      "blocked": true,
      "messageRequestAccepted": false
    },
    {
      "aci": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
      "pni": "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee",
      "number": "+15550101",
      "name": "Alice Smith",
      "contactName": "Alice Smith",
      "profileName": "Ali",
      "blocked": false,
      "messageRequestAccepted": true
    }
  ]
}
```

`contacts` lists the users go-signal knows with a name or number, and the blocked ones, without the
account itself, sorted by display name (`name`, else `number`, else `aci`). `--blocked` and
`--query` filter the list. Each entry is a **contact**:

| Field                    | Type    | Description                                                                           |
| ------------------------ | ------- | ------------------------------------------------------------------------------------- |
| `aci`                    | string  | Account identity (ACI); _optional_                                                    |
| `pni`                    | string  | Phone number identity (PNI); _optional_                                               |
| `number`                 | string  | Phone number (E.164); _optional_                                                      |
| `name`                   | string  | The name Signal shows: `nickname`, else `contactName`, else `profileName`; _optional_ |
| `nickname`               | string  | The nickname we gave the user in Signal; _optional_                                   |
| `contactName`            | string  | The name in the phone's address book; _optional_                                      |
| `profileName`            | string  | The name the user set in their Signal profile; _optional_                             |
| `blocked`                | boolean | `true` if we blocked the user (including a block not yet confirmed by the phone)      |
| `messageRequestAccepted` | boolean | Whether we accepted the user's message request; _optional_ (unknown)                  |

go-signal stores no usernames, so contacts have no `username`.

## `contacts show`

```json
{
  "version": 1,
  "contact": {
    "aci": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    "number": "+15550101",
    "name": "Alice Smith",
    "contactName": "Alice Smith",
    "profileName": "Ali",
    "blocked": false,
    "messageRequestAccepted": true
  }
}
```

`contact` is a contact as in [`contacts list`](#contacts-list). An unknown user is an error (no
document).

## `contacts block` and `contacts unblock`

```json
{
  "version": 1,
  "block": {
    "blocked": true,
    "results": [
      {
        "aci": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
        "number": "+15550101",
        "name": "Alice Smith",
        "contactName": "Alice Smith",
        "blocked": true,
        "changed": true
      },
      {
        "aci": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
        "number": "+15550102",
        "blocked": true,
        "changed": false
      }
    ]
  }
}
```

Both commands print a `block` document. `blocked` is `true` for `contacts block` and `false` for
`contacts unblock`. `results` has one entry per user, in the order given on the command line,
without duplicates: the contact afterwards (as in [`contacts list`](#contacts-list)) plus `changed`
(boolean), which is `false` if the user already was blocked (or unblocked). When the command fails,
nothing was changed and no document is printed.

## `send`

`send --edit <timestamp>` uses the same result format. The returned `timestamp` is the new
edit's timestamp; the argument identifies the original message. A successful result means the
edit was sent, not that the receiving app applied it. No fields or schema version change.

```json
{
  "version": 1,
  "send": {
    "timestamp": 1790000000000,
    "results": [
      {
        "type": "user",
        "number": "+15550101",
        "aci": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
        "timestamp": 1790000000000,
        "success": true,
        "unidentified": true
      },
      {
        "type": "self",
        "number": "+15550100",
        "aci": "11111111-1111-1111-1111-111111111111",
        "timestamp": 1790000000000,
        "success": true,
        "unidentified": false
      },
      {
        "type": "group",
        "groupId": "Z3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXA=",
        "timestamp": 1790000000000,
        "success": false,
        "members": [
          {
            "aci": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
            "success": true,
            "unidentified": true
          },
          {
            "aci": "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
            "success": false,
            "unidentified": false,
            "error": "recipient unreachable"
          }
        ]
      }
    ]
  }
}
```

`timestamp` is the message's sent timestamp (milliseconds since the Unix epoch). All recipients
get the same one; together with the account's ACI it identifies the message (e.g. for quotes,
reactions and remote deletes). `results` has one entry per recipient, in the order given on the
command line, without duplicates. The document is also printed when some recipients failed; the
exit code is then non-zero. Errors that stop the command before anything is sent (invalid or
unknown recipients) print no document.

| Field          | Type    | Description                                                                        |
| -------------- | ------- | ---------------------------------------------------------------------------------- |
| `type`         | string  | `user`, `self` (note to self) or `group`                                           |
| `number`       | string  | Phone number of a user; _optional_                                                 |
| `username`     | string  | Username of a user, as given; _optional_                                           |
| `aci`          | string  | ACI of a user; _optional_ (always set for `user` and `self`)                       |
| `name`         | string  | Name of a user (see [`contacts list`](#contacts-list)); _optional_                 |
| `groupId`      | string  | Base64 group ID; _optional_ (only for `group`)                                     |
| `timestamp`    | number  | Sent timestamp of the message                                                      |
| `success`      | boolean | `true` if the recipient (for a group: every member) got the message                |
| `unidentified` | boolean | `true` if sent with sealed sender; _optional_ (only for `user` and `self`)         |
| `error`        | string  | Why sending to the recipient failed as a whole; _optional_                         |
| `members`      | array   | Result per group member, without us; _optional_ (only for `group`, unless `error`) |

Each entry of `members`:

| Field          | Type    | Description                                                   |
| -------------- | ------- | ------------------------------------------------------------- |
| `aci`          | string  | ACI of the member; _optional_ (members can also have a `pni`) |
| `pni`          | string  | PNI of a member known only by phone number; _optional_        |
| `name`         | string  | Name of the member (see `contacts list`); _optional_          |
| `success`      | boolean | `true` if the member got the message                          |
| `unidentified` | boolean | `true` if sent with sealed sender                             |
| `error`        | string  | Why sending to the member failed; _optional_                  |

## `stories send`

The `storySend` object contains the `send` result's `timestamp` and `results`, plus
`allowsReplies`. For an explicit group, `results` contains one group
entry with peer outcomes in `members`. Pending invitations and requests are excluded.
Success means all peer submissions and the own-device story transcript succeeded;
it does not prove phone display. A transcript failure sets the group's `error` and
`success: false` while retaining already submitted peer outcomes. A group with no
other members succeeds when its own-device transcript succeeds.

```json
{
  "version": 1,
  "storySend": {
    "timestamp": 1790000000000,
    "results": [
      {
        "type": "group",
        "groupId": "Z3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXA=",
        "timestamp": 1790000000000,
        "success": true,
        "members": [
          {
            "aci": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
            "success": true,
            "unidentified": true
          }
        ]
      }
    ],
    "allowsReplies": true
  }
}
```

Text, media keys, profile keys and the group master key are absent from submission output.
Received story events keep their separate `type: "story"` format below. Schema version remains 1.

Private sends add `distributionListId` and a nonzero `storageVersion` to `storySend`; `results`
contains one `user` entry per expanded ACI. These entries report peer submission only.
The optional `syncError` reports a failed own-device transcript independently, even when
every peer succeeded. The command exits unsuccessfully if any peer or transcript failed.
My Story uses `00000000-0000-0000-0000-000000000000`. Story text, media bytes, profile keys
and group master keys are omitted.

## `stories audiences`

The document contains `version`, `storageVersion` and `storyAudiences` (always an array).
Each audience has `id`, `name`, `isBlockList`, `allowsReplies` and `recipients` (an array of
expanded canonical ACI strings, with self and ineligible contacts excluded). `isBlockList`
records the phone's exclusion policy; `recipients` contains the resulting delivery audience,
not the excluded contacts. Listing fetches a complete fresh snapshot and fails when it cannot
be determined. An empty array means the phone's storage currently defines no active lists.
This is an additive schema-version-1 document.

## `receipts send-viewed`

```json
{
  "version": 1,
  "receipt": {
    "type": "viewed",
    "sender": {
      "aci": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
      "number": "+15550101",
      "name": "Alice"
    },
    "timestamps": [1789907400000, 1789907400001]
  }
}
```

`receipt` describes a successful submission to the backend. It does not confirm delivery or
phone rendering; the backend may skip unaccepted message requests. On submission failure, no
receipt document is printed and the command exits nonzero. Viewed state is not synced to other devices by the current backend.

| Field        | Type          | Description                                                         |
| ------------ | ------------- | ------------------------------------------------------------------- |
| `type`       | string        | Always `"viewed"`                                                   |
| `sender`     | recipient     | Resolved original sender; same recipient shape as `receive`         |
| `timestamps` | array<number> | Original sent timestamps in milliseconds; unique, in supplied order |

The timestamps are exact unsigned 64-bit integers. Scripts should preserve integer precision
when parsing them. This adds a new document without changing schema version `1`.

## `react`

```json
{
  "version": 1,
  "react": {
    "emoji": "👍",
    "remove": false,
    "targetAuthor": {
      "aci": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
      "number": "+15550101"
    },
    "targetTimestamp": 1789999999000,
    "timestamp": 1790000000000,
    "results": [
      {
        "type": "user",
        "number": "+15550101",
        "aci": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
        "timestamp": 1790000000000,
        "success": true,
        "unidentified": true
      }
    ]
  }
}
```

A reaction is a message of its own: `timestamp` and `results` are as in [`send`](#send), with one
entry per chat given on the command line. The other fields describe the reaction:

| Field             | Type      | Description                                                            |
| ----------------- | --------- | ---------------------------------------------------------------------- |
| `emoji`           | string    | The emoji                                                              |
| `remove`          | boolean   | `true` if the reaction was taken back (`--remove`)                     |
| `targetAuthor`    | recipient | Author of the message reacted to (as in `receive`); our own for `self` |
| `targetTimestamp` | number    | Sent timestamp of the message reacted to                               |

## `delete`

```json
{
  "version": 1,
  "delete": {
    "targetTimestamp": 1789999999000,
    "timestamp": 1790000000000,
    "results": [
      {
        "type": "group",
        "groupId": "Z3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXA=",
        "timestamp": 1790000000000,
        "success": true,
        "members": [
          {
            "aci": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
            "success": true,
            "unidentified": true
          }
        ]
      }
    ]
  }
}
```

A remote delete is a message of its own, too: `timestamp` and `results` are as in
[`send`](#send). `targetTimestamp` (number) is the sent timestamp of our message that was deleted.

## `pins add` and `pins remove`

These return `{"version":1,"pin":{...}}`. `timestamp` and `results` have the normal
[`send`](#send) meaning, including partial failures; delivery does not prove application on a phone.

| Field                             | Type              | Description                                                                    |
| --------------------------------- | ----------------- | ------------------------------------------------------------------------------ |
| `operation`                       | string            | `pin` for add, `unpin` for remove                                              |
| `targetAuthor`, `targetTimestamp` | recipient, number | Resolved target author and original sent timestamp in milliseconds             |
| `durationSeconds`                 | number            | Positive uint32 seconds for finite pin, zero for forever; present for pin only |
| `forever`                         | boolean           | Explicit duration mode; present for pin only                                   |

## `pins list`

Returns `{"version":1,"pinState":{...}}` from one bounded offline inbox snapshot.
History comes from daemon/MCP receiving. Ordinary receive and outgoing sends do not populate it.
The account lock is required; stop the receiver before listing.

| Field                         | Type    | Description                                                                                                         |
| ----------------------------- | ------- | ------------------------------------------------------------------------------------------------------------------- |
| `chat`                        | chat    | Canonical chat reference                                                                                            |
| `observations`                | array   | Latest distinct retained pin/unpin per target, sorted by target author then target timestamp; empty array when none |
| `completeness`                | string  | Always `unknown`; never an authoritative active phone pin set                                                       |
| `scanned`                     | number  | Number of retained chat entries examined, including unrelated events                                                |
| `firstEntryId`, `lastEntryId` | number  | Retained scan bounds, zero when empty                                                                               |
| `truncated`                   | boolean | Whether older retained entries were excluded by the scan limit                                                      |
| `ignoredInvalid`, `conflicts` | number  | Invalid controls ignored and conflicting payloads with the same sender/timestamp identity                           |

Each observation includes `operation` (`pin` or `unpin`), `targetAuthor`, `targetTimestamp`,
`sender`, `timestamp` (control's sender timestamp), `entryId`, `receivedAt` (UTC timestamp),
and `targetDeleted` (creator deletion retained). Pin observations also include `durationSeconds`
and `forever`. Finite pins add `expiresAt` and `expiryReached`, calculated from local receipt time.
Forever pins and unpins omit expiry fields. Unpins, reached expiry and deletion flags stay visible.
Events are reduced by local inbox ID order; duplicates within the snapshot keep first receipt time.
Missing/pruned events, eligibility, phone limits and timers prevent inferring current phone state.

## `polls create`, `polls vote` and `polls close`

These commands return `{"version":1,"poll":{...}}`. Its `timestamp` and `results` have the
same shape as [`send`](#send), including direct-recipient outcomes or per-member failures
for the selected group. The existing schema also covers note-to-self.

| Field             | Type      | Description                                                             |
| ----------------- | --------- | ----------------------------------------------------------------------- |
| `operation`       | string    | `create`, `vote` or `close`                                             |
| `targetAuthor`    | recipient | Poll creator; our account for creation and closure                      |
| `targetTimestamp` | number    | Creation timestamp; equals `timestamp` for creation                     |
| `creation`        | object    | Creation only: `question`, ordered `options` and `allowMultiple`        |
| `optionIndexes`   | number[]  | Vote only; zero-based selections, `[]` for withdrawal                   |
| `voteCount`       | number    | Vote only; reserved or explicit counter ordering this account's changes |

## `polls show`

Returns `{"version":1,"pollState":{...}}` from one bounded local inbox query without
connecting. The history was collected by daemon/MCP receiving; ordinary `receive` and
outgoing sends do not populate it. Stop the active receiver to release the account lock.

| Field                         | Type                    | Description                                                                               |
| ----------------------------- | ----------------------- | ----------------------------------------------------------------------------------------- |
| `source`                      | string                  | Optional `durable` for the materialized view selected by `--durable`                      |
| `observations`                | number                  | Optional positive count of distinct poll evidence in the durable view                     |
| `chat`, `author`, `timestamp` | chat, recipient, number | Poll identity: direct chat or group, creator ACI and creation timestamp                   |
| `creationPresent`             | boolean                 | Whether a valid creation was retained within the scan                                     |
| `creation`                    | object                  | Optional question/options/allowMultiple, when retained                                    |
| `tally`                       | number[]                | Optional observed counts by zero-based option; omitted without creation or after deletion |
| `votes`                       | object[]                | Latest retained valid votes, sorted by voter ACI; `[]` when none                          |
| `closureObserved`             | boolean                 | Whether a matching creator closure was observed; false does not establish an open poll    |
| `closedAt`                    | number                  | Optional observed closure timestamp                                                       |
| `deleted`                     | boolean                 | Whether a matching creator remote deletion was observed                                   |
| `completeness`                | string                  | Always `unknown`                                                                          |
| `scanned`                     | number                  | Number of retained chat entries examined, including non-poll events                       |
| `firstEntryId`, `lastEntryId` | number                  | Retained scan bounds; zero for an empty scan                                              |
| `truncated`                   | boolean                 | More retained chat entries existed outside the scan limit                                 |
| `ignoredInvalid`, `conflicts` | number                  | Invalid matching observations and conflicting creations/equal-counter selections          |

Each vote has `voter` (recipient), `optionIndexes` (number array, empty for withdrawal),
`voteCount` (number) and `timestamp` (number). Older counters are ignored; equal counters
retain the first observation unless a later own-device sync timestamp replaces it.
No tally or absence claim is authoritative: history may have been pruned or never received.
The scan limit defaults to 1,000 chat entries and is capped at 10,000.

With `--durable`, ordinary receive, daemon/MCP observations and a one-time import of retained
inbox history contribute to a persisted projection. It survives subsequent inbox pruning.
`scanned`, `firstEntryId` and `lastEntryId` are zero; `truncated` is false. The source and
observation fields are additive in schema 1. Missing creation or closure still proves nothing
about unobserved activity. This flag cannot be combined with `--scan-limit`. See
[durable results](polls.md#durable-results) for storage and bootstrap semantics.

### Poll API envelopes

MCP `poll_create`, `poll_vote` and `poll_close` return the `poll` object above directly as
structured content; `poll_show` returns `pollState` directly. Daemon poll writes wrap it in
`{ok, poll, error?}` and `GET /v1/polls` returns `{pollState}`. Fields have the same meaning
as the CLI schema version 1, including delivery outcomes, counter and unknown completeness.

## `groups list`

```json
{
  "version": 1,
  "groups": [
    {
      "id": "Z3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXA=",
      "title": "Family",
      "description": "All of us",
      "revision": 12,
      "membership": "member",
      "role": "admin",
      "timerSeconds": 604800,
      "announcementsOnly": false,
      "members": [
        {
          "aci": "11111111-1111-1111-1111-111111111111",
          "role": "admin",
          "joinedAtRevision": 0
        },
        {
          "aci": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
          "role": "member",
          "joinedAtRevision": 2
        }
      ],
      "pending": [
        {
          "aci": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
          "role": "member",
          "addedBy": { "aci": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" },
          "invitedAt": "2026-09-20T12:30:00Z"
        }
      ],
      "requesting": []
    },
    {
      "id": "Z29uZS1pZC1nb25lLWlkLWdvbmUtaWQtZ29uZS1pZC0=",
      "title": "Old club",
      "revision": 0,
      "membership": "none",
      "timerSeconds": 0,
      "announcementsOnly": false,
      "leftAt": "2026-09-26T10:00:00Z",
      "error": "not a member of the group …"
    }
  ]
}
```

`groups` has a **group** object for every group whose master key go-signal knows (from
`account sync` or a message from the group), fetched from the server and sorted by title. A group
the server no longer shows us (we left or were removed) or doesn't know is still listed, with
`error` set, the last title go-signal saw, and no member lists. The master key is never printed.

| Field                      | Type    | Description                                                                                                                       |
| -------------------------- | ------- | --------------------------------------------------------------------------------------------------------------------------------- |
| `id`                       | string  | Group ID (base64)                                                                                                                 |
| `title`                    | string  | Title; empty if unknown                                                                                                           |
| `description`              | string  | Description; _optional_                                                                                                           |
| `avatarPath`               | string  | Opaque CDN path of the current avatar; _optional_, absent when cleared or the group cannot be fetched. No image is downloaded     |
| `revision`                 | number  | Number of changes to the group so far (`0` when `error` is set)                                                                   |
| `membership`               | string  | How we belong to it: `member`, `pending` (invited), `requesting` (asked to join) or `none`                                        |
| `role`                     | string  | Our role: `admin` or `member` (for `pending`: the role the invitation offers); _optional_                                         |
| `timerSeconds`             | number  | Disappearing messages timer in seconds; `0` is off                                                                                |
| `announcementsOnly`        | boolean | `true` if only admins can send messages                                                                                           |
| `membersCanEditAttributes` | boolean | Whether ordinary members may edit group information (including the timer); present for fetched groups, absent when `error` is set |
| `membersCanAddMembers`     | boolean | Whether ordinary members may add or invite users; present for fetched groups, absent when `error` is set                          |
| `members`                  | array   | Members: recipient fields plus `role` (`admin`, `member`) and `joinedAtRevision`; _optional_ (missing when `error` is set)        |
| `pending`                  | array   | Invited users: recipient fields plus `role`, `addedBy` (recipient) and `invitedAt`; _optional_ (as `members`)                     |
| `requesting`               | array   | Users asking to join: recipient fields plus `requestedAt`; _optional_ (as `members`)                                              |
| `banned`                   | array   | Banned users: recipient fields plus optional `bannedAt` (UTC RFC3339 timestamp); _optional_ (as `members`)                        |
| `leftAt`                   | string  | When we left the group with go-signal; _optional_                                                                                 |
| `error`                    | string  | Why the group couldn't be fetched (e.g. we are not a member); _optional_. The other fields then only hold what go-signal knows    |

The recipient fields (`aci`, `pni`, `number`, `username`) are those of a
[recipient](#common-objects). PNI invitations retain their typed `pni` in `pending`.

Self-membership uses the selected account's registered ACI and PNI. An own ACI or PNI
invitation reports `membership: "pending"` with the offered `role`; pending recipients
retain their typed `aci` or `pni` field. Full ACI membership takes precedence over
invitations, and an ACI invitation determines the role when both identities are invited.
Join requests and mutation permissions remain ACI-only. The schema stays at version `1`.

## `groups show`

The document is `{"version": 1, "group": {…}}`, where `group` is one group object as in
[`groups list`](#groups-list), always without `error`: a group that can't be fetched fails the
command instead.

## `groups create`

Returns the same document as [`groups show`](#groups-show), including the new group ID, creator
as administrator, and members or pending invitations as returned by the server. The title is
cached for subsequent commands. Master keys are never printed. On failure there is no success
document; an error may include the ID to inspect before retrying a possibly completed creation.

## `groups rename`

Returns the same document as [`groups show`](#groups-show), with the updated title and revision.
Renaming to the existing title leaves the revision unchanged. Validation, permission and
conflict errors fail the command without printing a success document.

## `groups update`

Returns the same document as [`groups show`](#groups-show), fetched after the combined settings
change. An unchanged update without `--avatar` leaves the revision unchanged. Omitted settings are preserved;
explicit empty description, zero timer and false announcement mode are applied. Permission
booleans are included even when false. All supplied fields are authorized against fresh state
before any mutation. Validation, permission and conflict errors produce no success document,
including when the patch was accepted but its response or follow-up fetch failed; the error
retains the accepted group ID/revision and advises inspection before retrying.
When a transport or response-decoding error prevents establishing acceptance, the error reports
an uncertain outcome with the group ID and attempted revision, without claiming acceptance.

`--avatar` uploads a validated PNG/JPEG unchanged and sets its path in the same patch as other
settings. Setting always changes the revision; `--remove-avatar` clears the path, and removing
an absent avatar is a no-op. Omitting both preserves the avatar. A failed upload submits no
group patch. A later patch failure can leave an unused encrypted upload. Success returns the
fresh server's optional `avatarPath`; accepted follow-up failures return no success document
or inferred avatar path. The additive field retains schema version 1.

## `groups add-members`

Returns the same document as [`groups show`](#groups-show), fetched after the change. New
ordinary members appear in `members`; users invited because their profile credentials are
unavailable appear in `pending`. Approved join requests move from `requesting` to `members`.
Existing members, invitations and duplicates are skipped; when there are no changes the
revision is unchanged. Errors produce no success document, including when the server accepted
the change but fetching the resulting group failed.

## `groups remove-members`

Returns the same document as [`groups show`](#groups-show), with the new revision and the
removed recipients absent from `members`, `pending` or `requesting`. Duplicate recipients
count once. Validation, permission and conflict errors fail without a success document.
Explicit `PNI:<uuid>` targets remove only matching `pending` entries with a `pni` identity.
A phone number can resolve to both ACI and PNI, removing their matching entries together.
Unrelated typed identities are preserved, including an ACI with the same UUID as a PNI.
The document and schema version remain unchanged.

## `groups promote` / `groups demote`

Returns the same document as [`groups show`](#groups-show), freshly fetched after the
change. The affected full members have `role` set to `admin` or `member`; `role` at group
level reflects the selected account, including after self-demotion. Duplicates count once
and unchanged roles are skipped. A complete no-op preserves the revision. Validation,
permission and last-administrator failures produce no success document. Errors after an
accepted change also produce no success document; inspect the group before retrying.

## `groups ban` / `groups unban`

Returns the same document as [`groups show`](#groups-show), freshly fetched after the change.
Banning removes targets from `members`, `pending` or `requesting` and adds them to `banned`;
absent users can also be banned. Unbanning removes their ACI bans without adding membership.
Duplicates and unchanged requests are skipped, preserving the revision for a complete no-op.
Existing ban times and PNI bans are preserved; mutation targets resolve to ACIs. `bannedAt` is
omitted when the server reports no timestamp. A fetched group with no bans has `banned: []`;
an inaccessible group omits it. These additive fields retain schema version 1.

Validation, permission and conflict errors produce no success document. Accepted follow-up
failures retain the group ID/revision in the error with inspection guidance; uncertain patch
errors advise inspection without claiming acceptance. See [the live check](dev.md#group-ban-live-check)
for phone and link-joining verification.

## `groups link show` / `groups link update`

These explicit commands return a dedicated document:

```json
{
  "version": 1,
  "groupLink": {
    "id": "Z3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXA=",
    "revision": 13,
    "state": "disabled"
  }
}
```

| Field      | Type   | Description                                                                                 |
| ---------- | ------ | ------------------------------------------------------------------------------------------- |
| `id`       | string | Group ID in base64                                                                          |
| `revision` | number | Fresh group revision; unchanged state without reset preserves it                            |
| `state`    | string | `disabled`, `enabled`, `enabled-with-approval`, or `unknown` for unrecognized server access |
| `url`      | string | Active `https://signal.group/#…` invite URL; omitted for disabled or unknown state          |

The URL contains the master key and invite password. Ordinary group documents and existing
MCP group results never include it. Updates require fresh administrator permissions, including
no-ops. Disabling preserves the password; resetting replaces it without implicitly enabling.
State and reset can be combined in one patch. Validation, permission, conflict, accepted
follow-up and uncertain errors produce no success document. Accepted errors retain only the
group ID/revision and advise inspection; uncertain errors do not claim acceptance.

## `groups join`

`groups join` returns a dedicated document on success:

```json
{
  "version": 1,
  "groupJoin": {
    "id": "Z3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXA=",
    "title": "Family",
    "revision": 13,
    "status": "member",
    "changed": true,
    "accepted": true,
    "verified": true
  }
}
```

| Field      | Type    | Description                                                                   |
| ---------- | ------- | ----------------------------------------------------------------------------- |
| `id`       | string  | Group ID in base64; never the master key                                      |
| `title`    | string  | Verified group title                                                          |
| `revision` | number  | Freshly verified membership or request revision                               |
| `status`   | string  | `member` for full membership, `requesting` for administrator approval         |
| `changed`  | boolean | This invocation changed membership or submitted a request                     |
| `accepted` | boolean | The server accepted this invocation's change                                  |
| `verified` | boolean | Fresh full state or a validated preview/signed change established the outcome |

All seven fields are present. A successful no-op has `changed: false`,
`accepted: false` and `verified: true`. No document contains the invite link,
password, master key or credentials. Errors leave stdout empty, including accepted
follow-up and uncertain errors; their text distinguishes these outcomes and advises
inspection before retrying. Requesting accounts may not be able to fetch full group
state. This additive document retains schema version 1.

When fresh full state shows your own ACI or PNI invitation, `groups join` fails with
acceptance guidance and prints no document. Use `groups accept` for that invitation.

## `groups accept`

`groups accept` returns a dedicated document on success:

```json
{
  "version": 1,
  "groupAccept": {
    "id": "Z3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXA=",
    "title": "Family",
    "revision": 13,
    "changed": true,
    "accepted": true,
    "verified": true
  }
}
```

| Field      | Type    | Description                                                                            |
| ---------- | ------- | -------------------------------------------------------------------------------------- |
| `id`       | string  | Canonical group ID in base64; never the master key                                     |
| `title`    | string  | Title from freshly verified full group state                                           |
| `revision` | number  | Freshly verified membership revision, which may exceed the submitted change's revision |
| `changed`  | boolean | The server accepted this invocation's invitation promotion                             |
| `accepted` | boolean | HTTP acceptance of this invocation's change                                            |
| `verified` | boolean | Fresh full state established own ACI membership                                        |

All six fields are present. Successful acceptance has all three booleans true;
an already-member no-op has `changed: false`, `accepted: false`, `verified: true`
and does not increment the revision. Errors leave stdout empty, including accepted
changes whose follow-up fails. Library callers receive partial results preserving
acceptance and any fresh verification completed before a cache/notification error.
No document contains a master key, invitation secret or credential. This additive
document retains schema version 1.

## `groups cancel-request`

`groups cancel-request <group> --yes` returns a dedicated document on success:

```json
{
  "version": 1,
  "groupCancelRequest": {
    "id": "Z3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXA=",
    "title": "Family",
    "revision": 13,
    "changed": true,
    "accepted": true,
    "verified": true
  }
}
```

| Field      | Type    | Description                                                                     |
| ---------- | ------- | ------------------------------------------------------------------------------- |
| `id`       | string  | Canonical group ID bound to the selected account's known key, never the key     |
| `title`    | string  | Title from the fresh authenticated join preview                                 |
| `revision` | number  | Exact signed deletion revision, or fresh preview revision for a no-op           |
| `changed`  | boolean | This invocation received HTTP 200 for its cancellation PATCH                    |
| `accepted` | boolean | HTTP acceptance of this invocation's cancellation PATCH                         |
| `verified` | boolean | Exact signed own ACI request deletion, or fresh preview with no pending request |

All six fields are present, including false values and empty titles. Successful
cancellation has all three booleans true. A no-op has `changed: false`,
`accepted: false`, `verified: true` and does not increment the revision. A no-op
requires fresh authenticated preview evidence; HTTP 403/404 is an error, including
on repeat calls. Verification establishes deletion at that revision, not continuous
absence of future requests or delivery to other devices.

Operation failures leave stdout empty. Library callers receive partial results: the attempted
revision survives rejection or uncertainty; HTTP 200 preserves `changed` and
`accepted` even if response reading, decoding or signed verification fails.
Inspect the reported group on the phone or through an administrator before manually
retrying accepted or uncertain outcomes; `groups show` may be unavailable to a
requester. No member notification, linked-device sync, cache title update or
full-state fetch follows this operation. It preserves known keys and existing
title/left records. No document exposes master keys, invite secrets or credentials.
This additive document retains schema version 1.

## `groups leave`

Leave requires readable full group state. Requesters should use
`groups cancel-request <group> --yes`; its dedicated result is documented above.
The existing `left.membership` values remain compatible with older output.

```json
{
  "version": 1,
  "left": {
    "id": "Z3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXA=",
    "title": "Family",
    "membership": "member",
    "revision": 13,
    "promoted": [{ "aci": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" }],
    "leftAt": "2026-09-26T10:00:00Z"
  }
}
```

| Field        | Type   | Description                                                                                                     |
| ------------ | ------ | --------------------------------------------------------------------------------------------------------------- |
| `id`         | string | Group ID (base64)                                                                                               |
| `title`      | string | Title of the group                                                                                              |
| `membership` | string | What we gave up: `member`, `pending` (declined the invitation) or `requesting` (legacy full-state cancellation) |
| `revision`   | number | The group's revision after leaving                                                                              |
| `promoted`   | array  | [Recipients](#common-objects) made admins in the same change (`--promote`); may be empty                        |
| `leftAt`     | string | When we left                                                                                                    |

## `identities list`

```json
{
  "version": 1,
  "identities": [
    {
      "aci": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
      "fingerprint": "05ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100",
      "trust": "untrusted",
      "firstSeen": "2026-09-20T12:30:00Z",
      "changedAt": "2026-09-21T08:15:00Z"
    }
  ]
}
```

One **identity** object per typed identity whose key go-signal has stored, ordered by service ID
(ACI UUID or `PNI:<uuid>`). A recipient argument selects only that identity; `[]` if none is known.
Numbers and usernames select the ACI key; explicit `PNI:<uuid>` selects the separate PNI key.
The account's own ACI and PNI are excluded. While connected, authenticated phone verification
updates can change a known ACI key's trust. Unknown or changed keys are ignored; PNI trust is
independent. Local ACI trust decisions are queued for linked-device synchronization; PNI decisions
stay on this device.

| Field         | Type   | Description                                                                                                                                                                                                           |
| ------------- | ------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `aci`         | string | The user's ACI; empty for a PNI identity                                                                                                                                                                              |
| `pni`         | string | The PNI UUID, only for an explicit PNI identity; _optional_                                                                                                                                                           |
| `number`      | string | Phone number, if given as the recipient argument; _optional_                                                                                                                                                          |
| `username`    | string | Username, if given as the recipient argument; _optional_                                                                                                                                                              |
| `fingerprint` | string | The identity (public) key in hex: 33 bytes, starting with the key type `05`                                                                                                                                           |
| `trust`       | string | `trusted-unverified` (first key seen, or trusted by hand), `trusted-verified` (safety number compared locally or verified on the phone) or `untrusted` (changed or marked untrusted by the phone; sending is blocked) |
| `firstSeen`   | string | When go-signal first stored a key of this user; _optional_ (unknown for keys stored before go-signal tracked them)                                                                                                    |
| `changedAt`   | string | When the key last changed; _optional_                                                                                                                                                                                 |

## `identities show`

```json
{
  "version": 1,
  "identity": {
    "aci": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    "fingerprint": "05a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90",
    "trust": "trusted-unverified",
    "firstSeen": "2026-09-20T12:30:00Z",
    "safetyNumber": "847411542415762535671764553031165774357252933441622828935299",
    "scannable": "CAISIgog…"
  }
}
```

The **identity** object as in `identities list`, plus:

| Field          | Type   | Description                                                                              |
| -------------- | ------ | ---------------------------------------------------------------------------------------- |
| `safetyNumber` | string | The 60-digit safety number of this account and the user; the apps show it in blocks of 5 |
| `scannable`    | string | Base64 of what the apps' safety number QR code contains                                  |

## `identities trust`

```json
{
  "version": 1,
  "identity": {
    "aci": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    "fingerprint": "05ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100",
    "trust": "trusted-verified",
    "firstSeen": "2026-09-20T12:30:00Z",
    "changedAt": "2026-09-21T08:15:00Z"
  }
}
```

The **identity** object (as in `identities list`) after trusting it. Trust and verification are
independent for ACI and PNI keys. An offline ACI trust command atomically saves local trust and a
pending linked-device update. Its successful output means saved and queued, without confirming
phone application. The next connection or `account sync` attempts delivery; failed updates remain
queued for retry. PNI decisions stay local. Incoming phone verification can update only a matching
known ACI key and supersedes any pending local decision for that key without echoing it.

## `mcp doctor`

```json
{
  "version": 1,
  "doctor": {
    "healthy": false,
    "checks": [
      {
        "name": "policy",
        "status": "ok",
        "detail": "read-only: no tool sends"
      },
      { "name": "hook", "status": "ok", "detail": "off" },
      { "name": "transport", "status": "ok", "detail": "stdin/stdout" },
      { "name": "cpu", "status": "ok", "detail": "AES instructions available" },
      {
        "name": "account",
        "status": "fail",
        "detail": "+15550100: this device was unlinked (noticed 2026-09-21 08:00:00 UTC)",
        "hint": "link again with `go-signal link`; `go-signal account unlink` deletes the old data"
      }
    ]
  }
}
```

The MCP `doctor` tool returns the same object (without `version`).

| Field     | Type    | Description                                     |
| --------- | ------- | ----------------------------------------------- |
| `healthy` | boolean | `false` if any check has the status `fail`      |
| `checks`  | array   | The checks in the order they ran; each a check: |

| Field    | Type   | Description                                                                          |
| -------- | ------ | ------------------------------------------------------------------------------------ |
| `name`   | string | What was checked, e.g. `account`, `lock`, `server`, `inbox`, `connection` (MCP only) |
| `status` | string | `ok`, `warn` (may be intended, e.g. the account in use) or `fail`                    |
| `detail` | string | What was found, for people; not meant to be parsed                                   |
| `hint`   | string | What to do about it; _optional_                                                      |

When there is no usable account, the checks that need one are left out.

## `receive`

`receive` writes one document per event and line ([NDJSON](https://github.com/ndjson/ndjson-spec))
as soon as the event arrives. The `type` field says which event it is. New types can be added in
any release, so scripts should skip types they don't know.

```json
{"version":1,"type":"message","sender":{"aci":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},"chat":{"recipient":{"aci":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}},"timestamp":1789907401000,"time":"2026-09-20T12:30:01Z","serverTime":"2026-09-20T12:30:01.5Z","sync":false,"body":"hello"}
{"version":1,"type":"receipt","sender":{"aci":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"},"receiptType":"read","timestamps":[1789907401000]}
```

| `type`              | Event                                                            |
| ------------------- | ---------------------------------------------------------------- |
| `story`             | A received text/media story, including sent-device transcripts   |
| `message`           | A message, received or sent from another of our devices (`sync`) |
| `edit`              | An earlier message was edited                                    |
| `delete`            | An earlier message was deleted for everyone (remote delete)      |
| `pin`, `unpin`      | A message pin or unpin was observed                              |
| `reaction`          | An emoji reaction was added or removed                           |
| `typing`            | A typing indicator                                               |
| `receipt`           | A delivery, read or viewed receipt for messages we sent          |
| `readSync`          | Another of our devices marked messages as read                   |
| `unsupported`       | Content go-signal can't show yet, named in `content`             |
| `decryptionFailure` | An incoming message could not be decrypted                       |
| `identityChanged`   | A user's identity key (safety number) changed                    |
| `queueEmpty`        | The server has delivered all messages that were queued for us    |
| `connection`        | The connection state changed                                     |

Plain output prints the same events, one line each (`[time; timestamp=ms] <sender> → <dest>: <text>`, where
`me` is this account and other users show by name, else by number or ACI), except `queueEmpty` and `connection`, which only go to the log (`-v`).

`time` is the local date and `timestamp` is the exact sender timestamp in milliseconds since
the Unix epoch, for example `[2026-09-20 12:30:00 UTC; timestamp=1789907400000]`. Copy the
numeric value into `--quote <author>:<timestamp>` or `react --target <author>:<timestamp>`;
look up the author's number or ACI with `contacts list` when a name is displayed. References
in quotes, edits, deletes, reactions, receipts and read-sync events include their original
message timestamps too. Unknown timestamps have no prefix and unknown references show `-`;
values beyond the signed date conversion range show only `timestamp=<value>`. Plain inbox
output uses the same format. JSON fields and schema version remain unchanged.

### Common objects

A **recipient** identifies a user. At least one of `aci`, `pni`, `number` and `username` is set;
they are what the event said. `name` comes from go-signal's store.

| Field      | Type   | Description                                                                          |
| ---------- | ------ | ------------------------------------------------------------------------------------ |
| `aci`      | string | Account identity (ACI); _optional_                                                   |
| `pni`      | string | Phone number identity (PNI); _optional_                                              |
| `number`   | string | Phone number (E.164); _optional_                                                     |
| `username` | string | Username, without the leading `@`; _optional_                                        |
| `name`     | string | The user's name (see [`contacts list`](#contacts-list)); _optional_ (unknown, or us) |

A **chat** is the conversation an event belongs to. It has one of these fields, or none (`{}`)
when the event isn't about a single conversation.

| Field        | Type      | Description                                                                         |
| ------------ | --------- | ----------------------------------------------------------------------------------- |
| `groupId`    | string    | Group ID (base64); _optional_                                                       |
| `groupTitle` | string    | The group's title, if go-signal has fetched the group before (`groups`); _optional_ |
| `recipient`  | recipient | The other party of a 1:1 chat (see `sync`); _optional_                              |

The **envelope fields** appear at the top level of `message`, `edit`, `delete`, `reaction`, `pin`, `unpin`,
`typing`, `story` and `unsupported`:

| Field        | Type      | Description                                                                                                                                                                            |
| ------------ | --------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `sender`     | recipient | Who sent it; our own ACI when `sync` is `true`                                                                                                                                         |
| `chat`       | chat      | The conversation                                                                                                                                                                       |
| `timestamp`  | number    | The sender's timestamp in ms since the epoch; with `sender` it identifies a message (e.g. the target of a reaction). `0` when unknown                                                  |
| `time`       | string    | `timestamp` as a time; _optional_                                                                                                                                                      |
| `serverTime` | string    | When the server received it; _optional_                                                                                                                                                |
| `sync`       | boolean   | `true` if we sent it from another of our devices (a sync transcript): `chat.recipient` is then who it went to (our own ACI for note-to-self). Otherwise `chat.recipient` is the sender |

### `message`

The envelope fields, plus:

| Field         | Type     | Description                                                                                                      |
| ------------- | -------- | ---------------------------------------------------------------------------------------------------------------- |
| `body`        | string   | Original message text; _optional_. Mention ranges refer to this text, usually U+FFFC placeholders                |
| `mentions`    | array    | _optional_. Mention ranges in `body`; see below                                                                  |
| `attachments` | array    | _optional_. See below                                                                                            |
| `sticker`     | object   | _optional_. `packId` (hex), `stickerId` (number), _optional_ `emoji` and `image` (see below)                     |
| `poll`        | object   | _optional_. Creation: `question` (string), ordered `options` (string array), `allowMultiple` (boolean)           |
| `quote`       | object   | The message this one replies to; _optional_. `author` (recipient), `timestamp`, _optional_ `text` and `mentions` |
| `viewOnce`    | boolean  | `true` for a view-once message; _optional_ (left out when `false`)                                               |
| `unsupported` | string[] | Parts of the message go-signal can't show yet (names as in `unsupported` below); _optional_                      |

A message has a `body`, an attachment, a sticker or poll creation; data messages with none of these are reported
as `unsupported`.

Each `mentions` entry has `start` and `length` (numbers counting UTF-16 code units in the
original text) and `recipient` (a recipient object, including `name` when known). An emoji
outside the basic multilingual plane counts as two UTF-16 units. For example, a mention
in `"😀 ￼"` starts at 3 and has length 1. `quote.mentions` uses offsets in `quote.text`.
Edits use the same format in their replacement `body`.

Plain output expands valid ranges to `@Name`, `@me` for self, or the known number/identifier.
It ignores zero-length, out-of-bounds and surrogate-splitting ranges, and keeps the first valid
range when mentions overlap after sorting by start. JSON preserves the original offsets and
text; invalid identities and text-style ranges are excluded from mention metadata. Stored
inbox events retain these ranges for MCP and daemon output. Older inbox events without ranges
remain readable. These optional fields are additive; the schema remains version 1.

Each entry of `attachments` has:

| Field           | Type   | Description                                                                                                         |
| --------------- | ------ | ------------------------------------------------------------------------------------------------------------------- |
| `contentType`   | string | MIME type as the sender gave it                                                                                     |
| `filename`      | string | The sender's file name, unsanitized; _optional_                                                                     |
| `size`          | number | Size in bytes; _optional_                                                                                           |
| `caption`       | string | _optional_                                                                                                          |
| `path`          | string | Where `receive --download-attachments <dir>` saved it: `<dir>/<timestamp>-<n>-<name>`; _optional_                   |
| `downloadError` | string | Why `--download-attachments` couldn't save it (e.g. `attachment not found on the CDN`); _optional_, excludes `path` |

Without `--download-attachments`, neither `path` nor `downloadError` is set.

`sticker.image`, when present, has the same metadata and optional download fields as an
attachment. It is separate from `attachments`, whose numbering is unchanged. Its saved path
is `<dir>/<timestamp>-sticker-<stickerId><extension>`; collisions get a numeric suffix and
existing files are never overwritten. Pack keys and the image's cryptographic/CDN details are
not rendered. Metadata-only stickers and old inbox entries may have no `image`.

Sticker image downloads prefer the embedded attachment. A missing or expired image can fall
back to an installed cache or authenticated pack download using the privately retained pack
key. The resulting `image` metadata and path describe the downloaded image, including when the
original pointer was absent. Integrity errors and cancellation do not trigger fallback. A failed
download produces `sticker.image.downloadError`; other media and later messages still proceed.
These fields are additive; the schema remains version 1. Sticker sends use the existing send
result envelope and per-recipient outcomes.

### `story`

A story has the shared envelope fields, `allowsReplies` (boolean), and either `file`
(an attachment object) or `text` (a text card). Optional `mentions` use the same UTF-16
format as message mentions. A malformed or missing attachment becomes `unsupported`
with `content: "invalidStory"` and its original envelope.

The text card contains optional `body`, `style`, `foregroundColor`, `textBackgroundColor`,
`backgroundColor`, `gradient` and `preview`. Style is a lowercase wire name (`default`,
`regular`, `bold`, `serif`, `script`, `condensed`), or the wire enum number for a future style.
Colors are packed ARGB integers; an explicit zero is preserved. A gradient can have optional
legacy `startColor`/`endColor`, `angle` in degrees, and `colors`/`positions` arrays for modern
stops. Non-finite gradient positions are reported as `invalidStory`.

A preview contains `url` and optional `title`, `description`, `date` (wire milliseconds) and
`image` (an attachment object). `file` and preview `image` use the message attachment fields,
including `path`/`downloadError` with `--download-attachments`. Attachment encryption keys,
digests, CDN locations and group master keys are never printed.

`sync: true` identifies a story sent from another of our devices. Group stories have a group
chat; private sent stories use the account's own chat because there is no single destination.
Audience/distribution lists are not represented. Stories use the same shape in the
MCP/daemon inbox; they do not count as unread messages or generate read/viewed receipts.
Retained entries are not automatically removed at story expiry. Schema version remains 1.

### `edit`

The envelope fields (`timestamp` is the edit's own), plus:

| Field             | Type   | Description                                                              |
| ----------------- | ------ | ------------------------------------------------------------------------ |
| `targetTimestamp` | number | Timestamp of the edited message                                          |
| `body`            | string | The new text                                                             |
| `mentions`        | array  | _optional_. Mention ranges in the replacement `body`, as described above |

### `delete`

The envelope fields, plus:

| Field             | Type   | Description                      |
| ----------------- | ------ | -------------------------------- |
| `targetTimestamp` | number | Timestamp of the deleted message |

### `reaction`

The envelope fields, plus:

| Field             | Type      | Description                           |
| ----------------- | --------- | ------------------------------------- |
| `emoji`           | string    | The reaction                          |
| `remove`          | boolean   | `true` if the reaction was taken back |
| `targetAuthor`    | recipient | Author of the message reacted to      |
| `targetTimestamp` | number    | Timestamp of the message reacted to   |

### `typing`

The envelope fields, plus:

| Field    | Type   | Description            |
| -------- | ------ | ---------------------- |
| `action` | string | `started` or `stopped` |

### `receipt`

| Field         | Type      | Description                                   |
| ------------- | --------- | --------------------------------------------- |
| `sender`      | recipient | Who sent the receipt                          |
| `receiptType` | string    | `delivery`, `read`, `viewed` (or `unknown`)   |
| `timestamps`  | number[]  | Timestamps of our messages the receipt is for |

Receipts carry no time of their own.

### `readSync`

| Field       | Type   | Description                                                                 |
| ----------- | ------ | --------------------------------------------------------------------------- |
| `timestamp` | number | When the other device sent the sync message (ms since the epoch)            |
| `time`      | string | `timestamp` as a time; _optional_                                           |
| `messages`  | array  | The messages marked as read, each with `sender` (recipient) and `timestamp` |

### `pin` and `unpin`

Both carry the common envelope plus `targetAuthor` (recipient) and `targetTimestamp` (number).
`pin` also carries `durationSeconds` (uint32 seconds, zero for forever) and `forever` (boolean).
These are controls, not unread messages. Their sender timestamp identifies the control, while
the target timestamp identifies the pinned message. Finite expiry is receipt-based and is
reported only by retained list observations. These additions keep schema version 1.

### `pollVote` and `pollClose`

Both carry the common envelope. `pollVote` adds `targetAuthor` (recipient),
`targetTimestamp` (number), `optionIndexes` (number array, empty for withdrawal) and
`voteCount` (number). The counter orders selections per voter, not total votes.
`pollClose` adds `targetTimestamp`; its `sender` identifies the creator of the target poll.
Incoming direct/group events and own-device transcripts use the same format.
These fields and event types are additive; schema version remains 1.

### `unsupported`

The envelope fields, plus `content` (string), which names what was received:

| `content`                | Meaning                                                                     |
| ------------------------ | --------------------------------------------------------------------------- |
| `call`                   | A 1:1 call offer or hangup, or a group call update                          |
| `groupUpdate`            | A group change (members, title, settings, …)                                |
| `expirationTimerUpdate`  | The disappearing-messages timer of a 1:1 chat changed                       |
| `profileKeyUpdate`       | The sender shared their profile key                                         |
| `endSession`             | The sender reset the session                                                |
| `contact`                | A shared contact card                                                       |
| `payment`, `giftBadge`   | A payment or a gift badge                                                   |
| `invalidPin`             | Malformed or incompatible pin/unpin content                                 |
| `invalidPoll`            | Malformed or incompatible poll creation/vote/closure content                |
| `adminDelete`            | A message was deleted by a group admin                                      |
| `storyReply`             | Only in a `message`'s `unsupported`: the message replies to a story         |
| `deleteForMe`            | Sync: messages were deleted on another of our devices only (`chat` is `{}`) |
| `messageRequestResponse` | Sync: a message request was accepted, blocked or deleted on another device  |
| `dataMessage`            | A data message go-signal didn't recognise                                   |

New names can be added, and some may become event types of their own, in any release.

### `decryptionFailure`

| Field       | Type      | Description                       |
| ----------- | --------- | --------------------------------- |
| `sender`    | recipient | The sender, if known              |
| `timestamp` | number    | The message's timestamp           |
| `time`      | string    | `timestamp` as a time; _optional_ |
| `error`     | string    | Why decryption failed; _optional_ |

### `identityChanged`

| Field            | Type      | Description                                                        |
| ---------------- | --------- | ------------------------------------------------------------------ |
| `recipient`      | recipient | The user whose key changed                                         |
| `oldFingerprint` | string    | The key trusted before, in hex (see `identities list`); _optional_ |
| `newFingerprint` | string    | The new key, which is `untrusted`                                  |
| `time`           | string    | When go-signal noticed the change; _optional_                      |

Sending to the user fails (`error` in the `send` results) until `go-signal identities trust` is
run for them; receiving from them keeps working. The event comes right before the message that
carried the new key. A change noticed while sending, or while `receive` wasn't reading, is
reported by the next `receive`.

### `queueEmpty`

No further fields. It follows the messages that were waiting on the server when `receive`
connected, and can come again after a reconnect.

### `connection`

| Field   | Type   | Description                                                                                              |
| ------- | ------ | -------------------------------------------------------------------------------------------------------- |
| `state` | string | `connected`, `disconnected`, `error` (these recover on their own), `logged-out` or `failed` (both final) |
| `error` | string | The cause; _optional_                                                                                    |

After `logged-out` (the device was unlinked) or `failed` (reconnecting gave up), `receive` exits
with an error.

## `stickers install` / `stickers list`

Both return version 1 with a `stickerPacks` array. Installation returns the installed pack;
listing returns all locally installed packs of the selected account, sorted by ID. An empty
cache returns `[]`. Installation does not change phone installation state.

```json
{
  "version": 1,
  "stickerPacks": [
    {
      "id": "0123456789abcdef0123456789abcdef",
      "title": "Animals",
      "author": "Artist",
      "coverId": 0,
      "stickers": [
        { "id": 0, "emoji": "😀", "contentType": "image/webp", "size": 4 }
      ]
    }
  ]
}
```

`coverId` is optional; zero is a valid ID. `stickers` lists every cached item, including a
cover-only item, with ID, optional emoji, image MIME type and decoded byte size. Pack keys,
links containing keys and cached image bytes are never output. MCP's `sticker_packs_list`
and `sticker_pack_install` return the same array without `version`; `sticker_send` returns
ordinary per-recipient send results. `sticker_get` returns the saved image's `path`,
`contentType`, `filename` and decoded `size`, without pack keys or CDN locations.
