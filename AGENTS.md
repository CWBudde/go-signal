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
- `reference/signal-cli/` is the upstream Java source (submodule, read-only). Consult it for
  protocol behaviour and features. Never edit it.

## Conventions

- Configuration: Viper instance created in `NewRootCmd`. Precedence is flag > `GOSIGNAL_*` env >
  config file.
- Logging: `log/slog` to stderr only. Stdout is for command output.
- Errors: wrap with `%w` and use sentinel errors (`err113`).
- Tests live in the `_test` package (`testpackage` linter).

## Commands

- `just build` / `just test` / `just lint` / `just fmt` / `just check`
- Run `just fmt` and `just lint` before committing.
