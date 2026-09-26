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
    "linkedAt": "2026-09-20T12:30:00Z"
  }
}
```

| Field        | Type   | Description                                                               |
| ------------ | ------ | ------------------------------------------------------------------------- |
| `number`     | string | Phone number of the account                                               |
| `aci`        | string | Account identity (ACI)                                                    |
| `pni`        | string | Phone number identity (PNI); _optional_                                   |
| `deviceId`   | number | ID of this device within the account (the phone is 1)                     |
| `deviceName` | string | Name this device was linked with; _optional_                              |
| `linkedAt`   | string | When this device was linked; _optional_ (unknown for old data)            |
| `unlinkedAt` | string | When go-signal found this device unlinked (e.g. on the phone); _optional_ |

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

## `send`

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
| `success`      | boolean | `true` if the member got the message                          |
| `unidentified` | boolean | `true` if sent with sealed sender                             |
| `error`        | string  | Why sending to the member failed; _optional_                  |

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

| Field               | Type    | Description                                                                                                                    |
| ------------------- | ------- | ------------------------------------------------------------------------------------------------------------------------------ |
| `id`                | string  | Group ID (base64)                                                                                                              |
| `title`             | string  | Title; empty if unknown                                                                                                        |
| `description`       | string  | Description; _optional_                                                                                                        |
| `revision`          | number  | Number of changes to the group so far (`0` when `error` is set)                                                                |
| `membership`        | string  | How we belong to it: `member`, `pending` (invited), `requesting` (asked to join) or `none`                                     |
| `role`              | string  | Our role: `admin` or `member` (for `pending`: the role the invitation offers); _optional_                                      |
| `timerSeconds`      | number  | Disappearing messages timer in seconds; `0` is off                                                                             |
| `announcementsOnly` | boolean | `true` if only admins can send messages                                                                                        |
| `members`           | array   | Members: recipient fields plus `role` (`admin`, `member`) and `joinedAtRevision`; _optional_ (missing when `error` is set)     |
| `pending`           | array   | Invited users: recipient fields plus `role`, `addedBy` (recipient) and `invitedAt`; _optional_ (as `members`)                  |
| `requesting`        | array   | Users asking to join: recipient fields plus `requestedAt`; _optional_ (as `members`)                                           |
| `leftAt`            | string  | When we left the group with go-signal; _optional_                                                                              |
| `error`             | string  | Why the group couldn't be fetched (e.g. we are not a member); _optional_. The other fields then only hold what go-signal knows |

The recipient fields (`aci`, `pni`, `number`, `username`) are those of a
[recipient](#common-objects). Users invited by phone number are missing from `pending` (signalmeow
can't decrypt them yet).

## `groups show`

The document is `{"version": 1, "group": {…}}`, where `group` is one group object as in
[`groups list`](#groups-list), always without `error`: a group that can't be fetched fails the
command instead.

## `groups leave`

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

| Field        | Type   | Description                                                                                            |
| ------------ | ------ | ------------------------------------------------------------------------------------------------------ |
| `id`         | string | Group ID (base64)                                                                                      |
| `title`      | string | Title of the group                                                                                     |
| `membership` | string | What we gave up: `member`, `pending` (declined the invitation) or `requesting` (cancelled the request) |
| `revision`   | number | The group's revision after leaving                                                                     |
| `promoted`   | array  | [Recipients](#common-objects) made admins in the same change (`--promote`); may be empty               |
| `leftAt`     | string | When we left                                                                                           |

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
| `message`           | A message, received or sent from another of our devices (`sync`) |
| `edit`              | An earlier message was edited                                    |
| `delete`            | An earlier message was deleted for everyone (remote delete)      |
| `reaction`          | An emoji reaction was added or removed                           |
| `typing`            | A typing indicator                                               |
| `receipt`           | A delivery, read or viewed receipt for messages we sent          |
| `readSync`          | Another of our devices marked messages as read                   |
| `unsupported`       | Content go-signal can't show yet, named in `content`             |
| `decryptionFailure` | An incoming message could not be decrypted                       |
| `queueEmpty`        | The server has delivered all messages that were queued for us    |
| `connection`        | The connection state changed                                     |

Plain output prints the same events, one line each (`[time] <sender> → <dest>: <text>`, where
`me` is this account), except `queueEmpty` and `connection`, which only go to the log (`-v`).

### Common objects

A **recipient** identifies a user. At least one field is set; names are not resolved yet.

| Field      | Type   | Description                                   |
| ---------- | ------ | --------------------------------------------- |
| `aci`      | string | Account identity (ACI); _optional_            |
| `pni`      | string | Phone number identity (PNI); _optional_       |
| `number`   | string | Phone number (E.164); _optional_              |
| `username` | string | Username, without the leading `@`; _optional_ |

A **chat** is the conversation an event belongs to. It has one of these fields, or none (`{}`)
when the event isn't about a single conversation.

| Field       | Type      | Description                                            |
| ----------- | --------- | ------------------------------------------------------ |
| `groupId`   | string    | Group ID (base64); _optional_                          |
| `recipient` | recipient | The other party of a 1:1 chat (see `sync`); _optional_ |

The **envelope fields** appear at the top level of `message`, `edit`, `delete`, `reaction`,
`typing` and `unsupported`:

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

| Field         | Type     | Description                                                                                          |
| ------------- | -------- | ---------------------------------------------------------------------------------------------------- |
| `body`        | string   | Message text; _optional_. Mentions are U+FFFC placeholders for now                                   |
| `attachments` | array    | _optional_. See below                                                                                |
| `sticker`     | object   | _optional_. `packId` (hex), `stickerId` (number) and _optional_ `emoji`                              |
| `quote`       | object   | The message this one replies to; _optional_. `author` (recipient), `timestamp` and _optional_ `text` |
| `viewOnce`    | boolean  | `true` for a view-once message; _optional_ (left out when `false`)                                   |
| `unsupported` | string[] | Parts of the message go-signal can't show yet (names as in `unsupported` below); _optional_          |

A message has a `body`, an attachment or a sticker; data messages with none of these are reported
as `unsupported`.

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

### `edit`

The envelope fields (`timestamp` is the edit's own), plus:

| Field             | Type   | Description                     |
| ----------------- | ------ | ------------------------------- |
| `targetTimestamp` | number | Timestamp of the edited message |
| `body`            | string | The new text                    |

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

### `unsupported`

The envelope fields, plus `content` (string), which names what was received:

| `content`                                   | Meaning                                                                     |
| ------------------------------------------- | --------------------------------------------------------------------------- |
| `call`                                      | A 1:1 call offer or hangup, or a group call update                          |
| `groupUpdate`                               | A group change (members, title, settings, …)                                |
| `expirationTimerUpdate`                     | The disappearing-messages timer of a 1:1 chat changed                       |
| `profileKeyUpdate`                          | The sender shared their profile key                                         |
| `endSession`                                | The sender reset the session                                                |
| `contact`                                   | A shared contact card                                                       |
| `payment`, `giftBadge`                      | A payment or a gift badge                                                   |
| `pollCreate`, `pollVote`, `pollTerminate`   | A poll, a vote, or the end of a poll                                        |
| `pinMessage`, `unpinMessage`, `adminDelete` | A message was pinned, unpinned, or deleted by a group admin                 |
| `storyReply`                                | Only in a `message`'s `unsupported`: the message replies to a story         |
| `deleteForMe`                               | Sync: messages were deleted on another of our devices only (`chat` is `{}`) |
| `messageRequestResponse`                    | Sync: a message request was accepted, blocked or deleted on another device  |
| `dataMessage`                               | A data message go-signal didn't recognise                                   |

New names can be added, and some may become event types of their own, in any release.

### `decryptionFailure`

| Field       | Type      | Description                       |
| ----------- | --------- | --------------------------------- |
| `sender`    | recipient | The sender, if known              |
| `timestamp` | number    | The message's timestamp           |
| `time`      | string    | `timestamp` as a time; _optional_ |
| `error`     | string    | Why decryption failed; _optional_ |

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
