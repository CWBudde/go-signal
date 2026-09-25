# go-signal

A command-line client for the [Signal](https://signal.org) messenger, written in Go. It is a Java-free
alternative to [signal-cli](https://github.com/AsamK/signal-cli) with its own idiomatic CLI. It
runs as a linked device next to your phone.

> **Status:** early scaffold. See [PLAN.md](PLAN.md) for the roadmap.

## Development

Building needs Go, Rust and a C toolchain. See [docs/dev.md](docs/dev.md) for details.

```sh
git submodule update --init --depth 1 third_party/libsignal
just libsignal    # build libsignal_ffi.a (once, and after submodule bumps)
just build        # bin/go-signal with version info
just test         # go test -race
just lint         # golangci-lint
just fmt          # treefmt (gofumpt, gci, prettier, taplo, yamlfmt)
just check        # all of the above + go mod tidy check
just reference    # fetch the signal-cli reference submodule
```

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

## Reference

`reference/signal-cli` is a shallow git submodule of the upstream Java implementation. It is used
for reference only and is not part of the build.

## License

AGPL-3.0. See [LICENSE](LICENSE). go-signal builds on
[mautrix-signal](https://github.com/mautrix/signal)'s `signalmeow`, which is licensed under AGPL-3.0.
