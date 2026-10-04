# Repository Guidelines

## Project

go-signal is a Java-free Signal CLI in Go, inspired by signal-cli. It is not a drop-in replacement:
commands are idiomatic noun-verb subcommands in kebab-case (see PLAN.md §3). The roadmap and architecture decisions live in
`PLAN.md`. Keep that file up to date and tick items as they land.

## Structure

- `main.go` calls `cmd.Execute()`.
- `cmd/` holds the Cobra commands. `NewRootCmd()` builds the tree, with no package globals and no
  `init()`. Each command (group) gets its own file and a `newXxxCmd()` constructor.
- `internal/` holds the implementation packages (see PLAN.md §2).
- `scripts/` holds build and release helpers: `package.sh`, `render-packaging.sh` and `gendocs/` (man pages and completions).
  `packaging/` has the Homebrew/AUR templates, `contrib/systemd/` the receive timer, and the
  root `Dockerfile` the scratch image. Releases: release-please plus `.github/workflows/release.yaml`
  (docs/dev.md, "Releases").
- `reference/signal-cli/` is the upstream Java source (submodule, read-only). Consult it for
  protocol behaviour and features. Never edit it.

## Architecture

Layering, top to bottom:

- `cmd/`: parses flags, calls `internal/app`, renders with `internal/output`. `NewRootCmd(opts...)`
  takes `WithClientFactory` (tests pass `signaltest.Fake.Factory`) and `WithLocation`.
  `ExitCode` maps errors to exit codes (3 = device unlinked, 128+signal on forced exit).
- `internal/app/`: use cases (typed request → typed result, no printing, no Cobra). Shared by the
  CLI and the planned MCP server.
- `internal/signal/`: facade over `signalmeow` behind the `Client` interface (`client.go`). All
  signalmeow/zerolog types stay inside this package and are converted to our own types
  (`convert.go`). The real client (`meow*.go`) is `//go:build cgo || libsignal_go`; `meow_nocgo.go`
  (`!cgo && !libsignal_go`) returns `ErrCGORequired`. signalmeow's zerolog output is bridged to slog (`logbridge.go`).
  Invitation acceptance uses the fork's strict uncached full-state reader, bypassing
  cached group state and endorsement processing while retaining authorization credential
  caching. Own PNI matching is local to acceptance and leave; list/show/join self-membership remains ACI-only.
- `internal/signal/signaltest/`: in-memory fake `Client` (no cgo) that mimics account selection,
  the per-account lock and remote-unlink behaviour of the real client. Command tests use it.
- `internal/store/`: data-dir layout (`accounts.json` registry, `<aci>/account.db` SQLite with
  signalmeow's tables plus ours, `<aci>/lock` flock). Opening the DB needs cgo (mattn/go-sqlite3)
  or the `libsignal_go` tag (modernc.org/sqlite), see `sqlite_cgo.go` / `sqlite_purego.go`.
- `internal/mcp/`: MCP server (`mcp serve`, official go-sdk) whose tools call `internal/app`.
  Stdout carries only JSON-RPC; the SDK's logs are demoted to debug. While it runs, `app.Inbox`
  receives events into the account's inbox table, which the message tools and resources read.
  The write tools' safety policy lives in `internal/app` (`WithAllowlist`, `SendRequest.AttachDir`);
  `--read-only` and `--confirm` are handled in `internal/mcp`, and so is `--on-message`, which runs a
  program for incoming messages of the `--hook-from` chats (`hook.go`). `mcp serve --listen` serves the
  same server over streamable HTTP (`mcp.ServeHTTP`, bearer token, loopback only). User docs:
  `docs/mcp.md`.
- `internal/output/`: plain/JSON renderers. The JSON schema is documented in `docs/json.md` and
  versioned by `output.SchemaVersion`; bump it only when a field is removed or changes meaning.

Event semantics worth knowing before touching receive: `Client.Events()` is unbuffered, and an
event counts as acked once it is read from the channel; unread events are redelivered next time.
`Close` flushes pending acks with a keepalive round trip.

## Conventions

- Configuration: Viper instance created in `NewRootCmd`. Precedence is flag > `GOSIGNAL_*` env >
  config file.
- Logging: `log/slog` to stderr only. Stdout is for command output.
- Errors: wrap with `%w` and use sentinel errors (`err113`).
- Tests live in the `_test` package (`testpackage` linter). `internal/signal/export_test.go` (cgo)
  and `export_internal_test.go` are in-package files that expose internals to `signal_test`.
- Command output is checked against golden files in `cmd/testdata/`.
- Build tags: `libsignal_go` selects the pure-Go backend, the default for releases (PLAN.md Phases 7–10). signalmeow comes from
  the fork `github.com/cwbudde/mautrix-signal` (own module path, so go.mod has no `replace` and
  `go install` works). Besides the `pkg/libsignalgo` adaptation, the fork deliberately adds
  invite preview/single-attempt joining and opt-in websocket credential log redaction
  (see `docs/maintenance.md`). Code that calls libsignal through cgo is `cgo && !libsignal_go` and gets a
  `libsignal_go` twin; tests that need real libsignal are `cgo && !libsignal_go`; `purego_diff_test.go`
  compares both implementations. Details in `docs/dev.md` ("Pure-Go backend").

## Commands

- `just build` (pure-Go default backend) / `just test` / `just lint` / `just fmt` / `just check`
- `just check-purego` for the pure-Go build (no cgo, no Rust); `just build-cgo` for the cgo backend
- `just build-release <os> <arch>` / `just smoke-image` for release builds (the smoke test needs Docker)
- Bumping mautrix-signal or libsignal: `docs/maintenance.md`
- Run `just fmt` and `just lint` before committing.
- `just check` = fmt-check, lint, check-libsignal, test, go mod tidy check. golangci-lint runs with
  `default = 'all'` (see `.golangci.toml` for the few disabled linters).

The default build (`libsignal_go`, what `just build` and the releases use) needs only Go. The cgo
backend and most tests (`just test`, `just check`) need CGO plus `third_party/lib/libsignal_ffi.a`,
which is built from the `third_party/libsignal` submodule with Rust (`just libsignal`, once and
after submodule bumps).
The justfile exports `CGO_ENABLED=1` and `CGO_LDFLAGS=-L third_party/lib`. When you call `go`
directly, set `CGO_LDFLAGS` yourself, or use `CGO_ENABLED=0` for the pure-Go packages:

```sh
CGO_LDFLAGS="-L $PWD/third_party/lib" go test -race -run TestName ./cmd/   # single test, cgo
CGO_ENABLED=0 go test -tags libsignal_go -run TestName ./internal/store/         # no libsignal needed
UPDATE_GOLDEN=1 go test ./cmd/                                             # rewrite cmd/testdata/*.golden
```

The libsignal submodule must sit at exactly the tag `libsignalgo` was generated against;
`just check-libsignal` enforces this. Upgrade steps are in `docs/dev.md`.
