# Daemon API

`go-signal daemon serve` gives local scripts and bots a JSON API and an SSE stream
while receiving into the account's persistent inbox. It owns one account for its
lifetime: other commands, including `receive` and `mcp serve`, cannot use that
account until the daemon stops. Use the API to send while the daemon is receiving.

## Start the daemon

Link an account first, then create a private token file with a random token of at
least 16 characters. For example, on systems with OpenSSL:

```sh
umask 077
token_file="${XDG_CONFIG_HOME:-$HOME/.config}/go-signal/daemon.token"
mkdir -p "$(dirname "$token_file")"
openssl rand -hex 32 > "$token_file"
go-signal daemon serve --listen 127.0.0.1:8766 \
  --token-file "$token_file" --allow-recipient self
```

The address must be loopback: `127.0.0.1`, `[::1]`, or `localhost`. Every request,
including health and streaming requests, needs `Authorization: Bearer <token>`.
Tokens in URL parameters are not authentication. Plain HTTP is for local access;
remote binding is rejected. Logs go to stderr; the daemon writes nothing to stdout.

Use global `--data-dir` and `--account` to select the account. SIGINT or SIGTERM
stops receiving and HTTP requests, flushes pending acknowledgements and releases
the account lock. A second signal forces exit. Remote unlink ends the process with
exit code 3.

Sending is denied by default. Repeat `--allow-recipient` to allow numbers, ACIs,
`@usernames`, `group:<id>` or `self`; `--allow-recipient '*'` allows every recipient.
The whole recipient list is checked before sending. The allowlist restricts sends,
not reading or read receipts. `--read-only` disables both sending and mark-read.

Configuration follows flag > `GOSIGNAL_*` environment > config file. Daemon
settings use their own `daemon` section:

```yaml
daemon:
  listen: "127.0.0.1:8766"
  token-file: "/absolute/path/daemon.token"
  read-only: false
  allow-recipient: ["self"]
  inbox-max-age: "720h"
  inbox-max-count: 10000
```

Each setting also supports its corresponding environment variable, for example
`GOSIGNAL_DAEMON_LISTEN` or `GOSIGNAL_DAEMON_ALLOW_RECIPIENT` (comma-separated).
The token can instead be set as `GOSIGNAL_DAEMON_TOKEN` or `daemon.token`.
When a token file is configured, its trimmed contents take precedence. There is
no command-line token option.

## JSON requests

The examples use these shell variables:

```sh
base=http://127.0.0.1:8766
token=$(cat "$token_file")
```

| Request              | Result                                                                                                        |
| -------------------- | ------------------------------------------------------------------------------------------------------------- |
| `GET /v1/health`     | `{version, account, connection}`: software version, account ACI and latest observed Signal connection status. |
| `GET /v1/messages`   | `{messages, cursor, more}`: persistent inbox entries and the cursor for the next page.                        |
| `POST /v1/messages`  | `{ok, send, error?}`: timestamp and outcomes for every recipient.                                             |
| `POST /v1/mark-read` | `{ok, messages, senders, error?}`: stored messages marked read and successful receipt submissions per sender. |

```sh
curl --fail-with-body -H "Authorization: Bearer $token" "$base/v1/health"
curl --fail-with-body -H "Authorization: Bearer $token" \
  "$base/v1/messages?cursor=0&limit=50"
curl --fail-with-body -H "Authorization: Bearer $token" \
  -H 'Content-Type: application/json' \
  --data '{"recipients":["self"],"text":"Hello from a script"}' \
  "$base/v1/messages"
```

Recipients use the same syntax as `send`, including `group:<id>` and self.
Text mentions use the existing `@{<recipient>}` syntax. Attachments, quotes, edits
and other rich send options are outside the first daemon API.

Message queries accept `chat` (a recipient or group reference), `cursor`, `since`
(RFC3339 sender-event time) and `limit` (1–200, default 50). Without cursor or since,
the newest page is returned in chronological order. `cursor=0` starts at the oldest
retained entry. Pass each returned cursor to fetch subsequent entries; `more`
reports whether additional matching entries follow. Cursor values are opaque to
clients even though they currently encode nonnegative database IDs.

Entries use the existing [JSON event format](json.md), wrapped with `id`,
`receivedAt`, optional `unread` and `event`. The endpoint's `/v1` versions the API;
event objects retain the existing output schema version. Attachment metadata is
included, but this API does not download attachment contents.

Reading or streaming does not send read receipts. Mark-read does:

```sh
curl --fail-with-body -H "Authorization: Bearer $token" \
  -H 'Content-Type: application/json' \
  --data '{"chat":"self","cursor":"42"}' "$base/v1/mark-read"
```

The cursor limits mark-read to entries up to that ID; cursor `"0"` selects none.
Omitted chat selects all
chats; omitted cursor selects all stored unread messages. `{}` selects both.
Peer receipts respect the phone's stored read-receipt setting. When disabled, only read sync
to your other devices is attempted; local messages are still marked read. `senders` counts
successful submissions, including suppressed peer receipts, rather than confirmed delivery.
Run `account sync` after changing the phone's setting. Until it is learned, the backend permits
peer receipts. Read-sync failures are logged and do not fail local mark-read.

Requests must contain one JSON object, with only documented fields, and be no
larger than 1 MiB. Authentication errors return 401, malformed input 400, denied
writes 403, oversized bodies 413 and unavailable connections 503. Errors contain
`{error:{code,message}}`; unknown paths return 404 and unsupported methods 405.

Completed write attempts return HTTP 200 even if some or all targets failed:
check `ok`, `send.results` and `error`. Mark-read failures retain partial message
and sender counts. Neither operation retries automatically. A lost HTTP response
can leave delivery uncertain, and a failed local update can follow a transmitted
read receipt. Inspect the outcome before retrying sends.

## Streaming and reconnecting

```sh
curl --no-buffer -H "Authorization: Bearer $token" \
  "$base/v1/events?cursor=0"
```

The SSE stream first emits `event: ready`, whose ID and JSON data give the starting
cursor. Each subsequent `event: inbox` contains an inbox entry and uses its ID as
the SSE `id`. Comments provide heartbeats roughly every 15 seconds while idle.
An optional `chat` query filters the stream.

Without a cursor, a new stream starts after the current inbox tail. `cursor=0`
replays retained history. On reconnect, supply the last fully processed SSE ID:

```sh
curl --no-buffer -H "Authorization: Bearer $token" \
  -H 'Last-Event-ID: 42' "$base/v1/events"
```

`Last-Event-ID` takes precedence over a query cursor. Store the cursor only after
your application successfully handles the event; deduplicate inbox IDs because a
connection may fail after delivery but before your cursor is saved. Browser native
EventSource cannot set the bearer header; use a client capable of authenticated
HTTP streaming. Do not put the token in the URL.

The stream contains persisted additions, including messages, edits, deletes,
reactions and supported inbox error events. It does not contain typing indicators,
receipts, connection changes or read-state updates. Query health for connection
status and refresh message pages for current unread state. Slow or disconnected
stream clients do not stop the receiver or other readers.

[The Python example](../scripts/daemon-example.py) demonstrates sending, listing,
mark-read and reconnecting with a saved cursor using the standard library.

```sh
python3 scripts/daemon-example.py --token-file "$token_file" health
python3 scripts/daemon-example.py --token-file "$token_file" send self --text 'Hello'
python3 scripts/daemon-example.py --token-file "$token_file" list --cursor 0
python3 scripts/daemon-example.py --token-file "$token_file" mark-read --chat self
python3 scripts/daemon-example.py --token-file "$token_file" stream --cursor-file inbox.cursor
```

Keep separate cursor files for different accounts and chat filters. The example
prints each inbox entry before atomically saving its cursor. Applications whose
processing has side effects should also deduplicate IDs in their own durable state;
printing or acting on an entry and saving a cursor are not one transaction.

## Retention and delivery limits

The inbox survives restarts. By default, the daemon removes entries older than
30 days and retains at most 10,000 entries. `--inbox-max-age 0` or
`--inbox-max-count 0` disables that respective bound. Pruning runs on receiver
startup and after additions; age limits do not imply idle-time expiry.

Reconnects replay successfully stored events that remain in the inbox. Pruned
history cannot be recovered, and an old cursor resumes at the remaining entries
without reporting a gap. Future numeric cursors are syntactically accepted and
wait for later IDs; always use cursors returned by this daemon/account.

The Signal client acknowledges an event when the receive loop reads it, before
the database write completes. A crash or storage failure in that interval can
lose the event. This API does not promise exactly-once delivery or crash-proof
acknowledgement. A terminal receiver failure stops the daemon; temporary Signal
connection errors remain visible in health while the client reconnects.
