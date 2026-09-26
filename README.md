# go-signal

A command-line client for the [Signal](https://signal.org) messenger, written in Go. It is a Java-free
alternative to [signal-cli](https://github.com/AsamK/signal-cli) with its own idiomatic CLI. It
runs as a linked device next to your phone: it sends and receives messages, reactions, receipts
and attachments, lists contacts, groups and identities, and can serve your account to AI agents
over [MCP](docs/mcp.md).

> **Status:** usable, but young. Linux (amd64, arm64) and macOS (arm64) are supported. See
> [PLAN.md](PLAN.md) for the roadmap.

## Install

### Release binaries

The [releases](https://github.com/cwbudde/go-signal/releases) have a fully static Linux binary
(it runs on any distribution, glibc or musl) and a macOS arm64 binary. Each archive also contains
the man pages, shell completions and a systemd timer (see [Staying linked](#staying-linked)).

```sh
version=0.1.0 os=linux arch=amd64   # arch=arm64 for ARM; os=darwin arch=arm64 for macOS
name=go-signal_${version}_${os}_${arch}
curl -LO https://github.com/cwbudde/go-signal/releases/download/v$version/$name.tar.gz
curl -LO https://github.com/cwbudde/go-signal/releases/download/v$version/SHA256SUMS
sha256sum --ignore-missing -c SHA256SUMS   # macOS: shasum -a 256 --ignore-missing -c SHA256SUMS
tar xzf $name.tar.gz
mkdir -p ~/.local/bin && cp $name/go-signal ~/.local/bin/
```

The archives carry a [build provenance attestation](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations):
`gh attestation verify go-signal_*.tar.gz --repo cwbudde/go-signal` checks that the release
workflow built them from this repository.

### Homebrew (macOS, Linux)

```sh
brew install cwbudde/tap/go-signal
```

### Arch Linux (AUR)

```sh
yay -S go-signal-bin   # or any other AUR helper
```

### Container

`ghcr.io/cwbudde/go-signal` (linux/amd64, linux/arm64) holds only the static binary. Keep the
account data in a volume mounted at `/data`:

```sh
docker run --rm -it -v go-signal:/data ghcr.io/cwbudde/go-signal link
docker run --rm -v go-signal:/data ghcr.io/cwbudde/go-signal receive
```

### From source

See [Development](#development).

## Quick start

```sh
# 1. Link go-signal to your account: scan the QR code in the Signal app on your phone
#    (Settings > Linked devices > Link new device). Contacts and groups sync afterwards.
go-signal link --name laptop
go-signal account show

# 2. Send a message: to a number, @username, group or yourself.
go-signal send +4915112345678 -m "Hello from go-signal"
go-signal send self -m "Note to self" --attach notes.pdf

# 3. Receive what is waiting on the server, or keep streaming with --follow.
go-signal receive
go-signal receive --follow -o json   # one JSON document per event (docs/json.md)
```

`go-signal <command> --help` and the man pages (`man go-signal-send`) describe every command.
[docs/json.md](docs/json.md) documents the JSON output, and [docs/mcp.md](docs/mcp.md) shows how to
connect go-signal to Claude Code, Claude Desktop and other MCP clients.

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

Building needs Go, Rust and a C toolchain. See [docs/dev.md](docs/dev.md) for details, including
how releases are made.

```sh
git submodule update --init --depth 1 third_party/libsignal
just libsignal    # build libsignal_ffi.a (once, and after submodule bumps)
just build        # bin/go-signal with version info
just test         # go test -race
just lint         # golangci-lint
just fmt          # treefmt (gofumpt, gci, prettier, taplo, yamlfmt)
just check        # all of the above + go mod tidy check
just reference    # fetch the signal-cli reference submodule
just build-static # fully static linux binary via an Alpine container (needs Docker)
```

## Reference

`reference/signal-cli` is a shallow git submodule of the upstream Java implementation. It is used
for reference only and is not part of the build.

## License

AGPL-3.0. See [LICENSE](LICENSE). go-signal builds on
[mautrix-signal](https://github.com/mautrix/signal)'s `signalmeow`, which is licensed under AGPL-3.0.
