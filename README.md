# go-signal

A command-line client for the [Signal](https://signal.org) messenger, written in Go. It is a Java-free
alternative to [signal-cli](https://github.com/AsamK/signal-cli) with its own idiomatic CLI. It
runs as a linked device next to your phone: it sends and receives messages, reactions, receipts
and attachments, lists contacts, groups and identities, and can serve your account to AI agents
over [MCP](docs/mcp.md).

> **Status:** usable, but young. Linux, macOS and Windows on amd64 and arm64 are supported. See
> [PLAN.md](PLAN.md) for the roadmap.

## Install

### Release binaries

The [releases](https://github.com/cwbudde/go-signal/releases) have a single, self-contained
binary for Linux, macOS and Windows on amd64 and arm64. On Linux and macOS:

```sh
curl -fsSL https://github.com/cwbudde/go-signal/releases/latest/download/install.sh | sh
```

The [script](scripts/install.sh) downloads the latest release for your OS and CPU, checks it
against the release's `SHA256SUMS` and installs `go-signal` to `~/.local/bin`. Set
`GOSIGNAL_VERSION=0.2.0` for a specific release, or `GOSIGNAL_INSTALL_DIR` for another
directory (`… | sudo GOSIGNAL_INSTALL_DIR=/usr/local/bin sh`).

On Windows, download `go-signal_<version>_windows_amd64.zip` (or `_arm64`) from the release and
put `go-signal.exe` on your `PATH`.

The archives also contain man pages, shell completions and, for Linux, a systemd timer (see
[Staying linked](#staying-linked)). To check a manual download, compare it with the release's
`SHA256SUMS`, or verify its [build provenance](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations)
with `gh attestation verify <archive> --repo cwbudde/go-signal`.

### With Go

With Go 1.26 or later:

```sh
go install -tags libsignal_go github.com/cwbudde/go-signal@latest
```

This installs to `$(go env GOPATH)/bin`. Don't leave out `-tags libsignal_go`: it selects the
pure-Go backend the releases use. Without it, Go builds the cgo backend, which needs a libsignal
library built with Rust (see [Development](#development)).

### Container

`ghcr.io/cwbudde/go-signal` (linux/amd64, linux/arm64) holds only the binary. Keep the account data
in a volume mounted at `/data`:

```sh
docker run --rm -it -v go-signal:/data ghcr.io/cwbudde/go-signal link
docker run --rm -v go-signal:/data ghcr.io/cwbudde/go-signal receive
```

## Quick start

```sh
# 1. Link go-signal to your account: scan the QR code in the Signal app on your phone
#    (Settings > Linked devices > Link new device). Contacts and groups sync afterwards.
go-signal link --name laptop
go-signal account show

# 2. Send a message: to a number, @username, group or yourself.
go-signal send +4915112345678 -m "Hello from go-signal"
go-signal send self -m "Note to self" --attach notes.pdf

# Edit your own message, using the timestamp printed by its original send.
go-signal send +4915112345678 --edit 1790000000000 -m "Corrected text"

# 3. Receive what is waiting on the server, or keep streaming with --follow.
go-signal receive
go-signal receive --follow -o json   # one JSON document per event (docs/json.md)
```

`go-signal <command> --help` and the man pages (`man go-signal-send`) describe every command.
[docs/json.md](docs/json.md) documents the JSON output.

`send --edit <timestamp>` works for users, groups (`--group <id>`) and `self`, including messages
sent from your other devices when you know their timestamp. Supply the replacement content;
go-signal does not reload the original text, mentions, quote or attachments. The live integration
test covers text edits. Signal normally permits 10 edits within 24 hours; Note to Self has no
time limit. Recipients enforce eligibility, and a successful send only confirms transport.
See [Signal's editing rules](https://support.signal.org/hc/en-us/articles/6255134251546-Edit-Message).

Create a group with yourself as administrator and one or more members:

```sh
go-signal groups create "Weekend" --member +4915112345678 --member @alice.42
go-signal groups create "Private notes"   # a group containing only you
```

Members can be numbers, ACIs or usernames. Duplicates are ignored; users without available
profile credentials receive invitations. The output lists the group ID, members and pending
invitations. Members may edit group information and add members; invite links start disabled.
Each invocation creates a new group. If creation fails with a group ID, inspect it with
`groups show <id>` before retrying: the server may have created the group already. Success
confirms creation; individual notification failures may only be logged by signalmeow.

To rename a group, list it first, then use its title or ID:

```sh
go-signal groups list
go-signal groups rename "Family" "Family and friends"
```

You must be a full member with permission to edit group information. The command prints the
updated group and remembers the new title for subsequent commands. An unchanged title sends
no update; a concurrent change fails with a retry hint. A successful rename confirms the server
update; failures notifying members are logged separately.

To add members or invite users to an existing group:

```sh
go-signal groups add-members "Family" +4915112345678 @alice.42
```

You must be a full member with permission to add members. Recipients without available
profile credentials receive invitations; the output shows the server's actual members and
pending invitations. Existing members, pending invitations, yourself and duplicates are
skipped. Administrators can approve join requests with the same command. Banned ACIs are
rejected. All recipients are resolved before a single change is submitted; conflicts fail
without automatic retries. If a change succeeds but fetching the result fails, the error
reports that it was accepted: inspect `groups show` before retrying. Notification failures
after a confirmed change are logged separately.

Administrators can remove members, revoke invitations or reject join requests:

```sh
go-signal groups remove-members "Family" +4915112345678 @alice.42
```

Recipients can be numbers, ACIs or usernames; duplicates are ignored. Every recipient must
currently be a member, invited or requesting to join. All recipients are checked before the
change is submitted, and a concurrent group change fails with a retry hint. The output shows
the updated group. Removal does not ban someone from rejoining; use `groups leave` to leave
yourself. Success confirms the server update; member notification failures are logged.
Invitations known only by phone-number identity (PNI), which the current backend cannot
decrypt, cannot be removed with this command.

Read or update the selected account's own profile:

```sh
go-signal profile show
go-signal profile update --given-name "Alice Mary" --family-name "Smith"
go-signal profile update --about=""   # clear about; preserve omitted fields
go-signal --account +4915112345678 profile show -o json
```

Supply at least one update flag. `--about-emoji` sets the profile emoji. Omitted flags preserve
their current values; explicitly empty values clear them. Limits count UTF-8 bytes: the combined
name is at most 257 bytes, including one separator byte for a nonempty family name; about is
512 bytes and the emoji is 32 bytes. Spaces are retained; NUL and invalid UTF-8 are rejected.

Updates reuse your profile key and preserve the fetched avatar, payment address, phone-number
sharing preference and badges. Avatar changes and profiles v2 are not supported. The v1 server
API cannot protect against concurrent edits from your phone or another device. An unchanged
profile sends no write. Each changed update attempts one write; if an error reports acceptance
or an unknown outcome, inspect `profile show` before retrying. Errors leave stdout empty.
See [the live-check procedure](docs/dev.md#own-profile-live-check) for phone and metadata checks.

### Staying linked

Signal removes a linked device that hasn't connected for about 30 days. Run `go-signal receive`
regularly to keep go-signal linked. `contrib/systemd/` (also in the release archives and installed
by the AUR package) has a user timer that does this daily:

```sh
cp contrib/systemd/go-signal-receive.{service,timer} ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now go-signal-receive.timer
```

A cron entry works as well, e.g. `0 9 * * * go-signal receive >> ~/signal.log`. `receive`
acknowledges what it prints, so collect its output if you want to keep the messages. If the
device was unlinked anyway, commands exit with code 3 (see below).

## AI agents (MCP)

`go-signal mcp serve` makes your account available to AI agents such as Claude Code, Claude
Desktop and other [Model Context Protocol](https://modelcontextprotocol.io) clients. The agent can
look up contacts and groups, read and wait for incoming messages and fetch attachments. It can
send messages, reactions and deletes only to the recipients you allow.

```sh
# Claude Code, read-only: the agent can read messages but not send any.
claude mcp add signal -- go-signal mcp serve --read-only

# The agent may message one person and one group, and attach files only from ~/signal-out.
claude mcp add signal -- go-signal mcp serve \
  --allow-recipient +4915112345678 --allow-recipient group:<group-id> \
  --attach-dir ~/signal-out
```

While it runs, the server holds the account and receives messages into an inbox that the agent
reads; other go-signal commands for that account fail with "account in use" until it stops.
`--confirm` has the client ask you before every send, `--listen` serves HTTP on a loopback address
instead of stdio, and `--on-message` runs a program for new messages from chats you choose. [docs/mcp.md](docs/mcp.md) covers Claude Desktop and other clients, every tool and
flag, and the security model.

## Scripts and bots (daemon API)

`go-signal daemon serve` exposes a local HTTP JSON API and an SSE stream while receiving
into the persistent inbox. Scripts can read events, send text to allowed recipients and
explicitly mark messages read. Each request requires a bearer token.

```sh
go-signal daemon serve --listen 127.0.0.1:8766 \
  --token-file /absolute/path/daemon.token --allow-recipient self
```

[docs/daemon.md](docs/daemon.md) covers token setup, endpoints, reconnecting with inbox
cursors, retention and delivery limits. A [Python example](scripts/daemon-example.py)
uses only the standard library.

## Configuration

Settings resolve in this order: command-line flag, then `GOSIGNAL_*` environment variable (e.g.
`GOSIGNAL_ACCOUNT`, `GOSIGNAL_DATA_DIR`), then `$XDG_CONFIG_HOME/go-signal/config.yaml`.

```yaml
account: "+491234567890"
data-dir: /home/me/.local/share/go-signal
log-format: text
```

Logs are written to stderr. Stdout is reserved for command output.

## Exit codes

| Code | Meaning                                                                                   |
| ---- | ----------------------------------------------------------------------------------------- |
| 0    | Success                                                                                   |
| 1    | Any other error                                                                           |
| 3    | This device was unlinked from the account (e.g. on the phone): delete its data and relink |
| 130  | Forced exit by a second SIGINT (143 for SIGTERM) while shutting down                      |

The first SIGINT/SIGTERM (Ctrl-C) shuts down gracefully: `receive --follow` stops, acknowledges the
messages it has printed and exits with 0. A second signal exits right away.

After an unlink is detected, the account is marked as unlinked and later commands fail with code 3
right away. `go-signal account unlink --yes` then deletes the local data, and `go-signal link`
links the device again.

## Development

Building needs only Go. The default backend is libsignal-go, a pure-Go port of libsignal, selected
with the `libsignal_go` build tag:

```sh
git clone https://github.com/cwbudde/go-signal && cd go-signal
go build -tags libsignal_go .
```

The cgo backend on the Rust libsignal is still there, for the differential tests and as a
fallback; it needs Rust and a C toolchain (`just libsignal`, then `just build-cgo`). See
[docs/dev.md](docs/dev.md) for details, including how releases are made.

```sh
just build        # bin/go-signal with version info (pure Go)
just test         # go test -race (cgo backend; just check-purego for the pure-Go one)
just lint         # golangci-lint
just fmt          # treefmt (gofumpt, gci, prettier, taplo, yamlfmt)
just check        # all of the above + go mod tidy check
just reference    # fetch the signal-cli reference submodule
```

## Reference

`reference/signal-cli` is a shallow git submodule of the upstream Java implementation. It is used
for reference only and is not part of the build.

## License

AGPL-3.0. See [LICENSE](LICENSE). go-signal builds on
[mautrix-signal](https://github.com/mautrix/signal)'s `signalmeow`, which is licensed under AGPL-3.0.
