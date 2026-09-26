package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/mcp"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newMCPCmd(
	clients *clientOpener, printers *printerFactory, loc *time.Location, appOpts []app.Option,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Model Context Protocol (MCP) server for AI agents",
		Args:  cobra.NoArgs,
	}

	cmd.AddCommand(newMCPServeCmd(clients, loc, appOpts), newMCPDoctorCmd(clients, printers))

	return cmd
}

const mcpServeLong = `Serve the account to an MCP client (Claude Code, Claude Desktop, other agents) over
stdin/stdout, e.g.:

  claude mcp add signal -- go-signal mcp serve

The server connects before it answers the client and holds the account for as long as it runs,
so no other go-signal command (receive included) can use the same account meanwhile. It fails at
startup when the account is in use or unlinked. It ends when the client closes stdin or on
SIGINT/SIGTERM, and with exit code 3 when this device is unlinked while it runs. Stdout carries
only the MCP protocol; logs go to stderr.

While it runs, the server receives messages into an inbox in the account's database, where the
client reads them (messages_list, messages_wait, the signal://chats resources). Entries older than
--inbox-max-age and all but the newest --inbox-max-count entries are deleted. The inbox keeps what
was received while no client was reading, also across restarts; messages that arrive while the
server isn't running wait on Signal's server until it (or receive) runs again.

attachment_get saves attachments to --download-dir, by default "attachments" in the account's
directory in the data dir (deleted with the account on unlink). Read receipts are only sent
through the mark_read tool.

The tools send_message, react and delete_message send only to the users and groups allowed with
--allow-recipient (repeatable: a number, ACI, @username, group:<id> or self; '*' allows everyone).
Without it, they reject every recipient. send_message attaches only files from --attach-dir.
--confirm asks the user to confirm every message through the client (MCP elicitation); a client
that can't ask gets an error instead. --read-only leaves out the tools that send, mark_read
included. These four settings can also be set in the config file under "mcp" (e.g.
mcp.allow-recipient: [...]) or as GOSIGNAL_MCP_ALLOW_RECIPIENT etc. (a list separated by commas).

With --listen, the server serves MCP's streamable HTTP transport at http://<address>/mcp instead of
stdin/stdout, for clients that can't start a process; it runs until SIGINT/SIGTERM. The address
must be on the loopback interface (plain HTTP), and every request must carry a bearer token
("Authorization: Bearer <token>") of at least 16 characters, read from --token-file or from
GOSIGNAL_MCP_TOKEN (or mcp.token in the config file). See docs/mcp.md.

--on-message runs a program for every incoming message (not our own, from any device) of the
users and groups allowed with --hook-from (the same syntax as --allow-recipient; required with
--on-message), e.g. a script that has an LLM answer through this server. The program gets the
inbox entry as a line of JSON on stdin and GOSIGNAL_ENTRY_ID, GOSIGNAL_CHAT and GOSIGNAL_SENDER in
its environment. Runs happen one at a time; one that takes longer than --on-message-timeout is
killed. Message content comes from other people: see docs/mcp.md, "Hooks".

When the server doesn't start or the client can't use it, run the same command line with
"mcp doctor" instead of "mcp serve".`

// Defaults of the inbox's retention.
const (
	defaultInboxMaxAge   = 30 * 24 * time.Hour
	defaultInboxMaxCount = 10000
)

// Config keys of the safety settings, bound to the flags without the "mcp." prefix.
const (
	cfgReadOnly       = "mcp.read-only"
	cfgAllowRecipient = "mcp.allow-recipient"
	cfgAttachDir      = "mcp.attach-dir"
	cfgConfirm        = "mcp.confirm"
	cfgListen         = "mcp.listen"
	cfgTokenFile      = "mcp.token-file"
	cfgOnMessage      = "mcp.on-message"
	cfgHookFrom       = "mcp.hook-from"
	cfgHookTimeout    = "mcp.on-message-timeout"
	// cfgToken has no flag, so that the token doesn't show in the process list.
	cfgToken = "mcp.token"
)

// minTokenLength is the shortest bearer token that --listen accepts.
const minTokenLength = 16

// defaultHookTimeout is how long an --on-message run may take by default.
const defaultHookTimeout = 5 * time.Minute

func newMCPServeCmd(clients *clientOpener, loc *time.Location, appOpts []app.Option) *cobra.Command {
	var (
		maxAge   time.Duration
		maxCount int
		dlDir    string
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the account to an MCP client on stdin/stdout",
		Long:  mcpServeLong,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if maxAge < 0 || maxCount < 0 {
				return errInboxLimits
			}

			bindMCPFlags(clients.cfg, cmd)

			policy, err := loadPolicy(clients.cfg)
			if err != nil {
				return err
			}

			logPolicy(policy)

			for _, key := range []string{cfgOnMessage, cfgHookFrom, cfgHookTimeout} {
				cobra.CheckErr(clients.cfg.BindPFlag(key, cmd.Flags().Lookup(strings.TrimPrefix(key, "mcp."))))
			}

			hook, err := loadHook(clients.cfg, policy)
			if err != nil {
				return err
			}

			listen, err := loadListen(clients.cfg)
			if err != nil {
				return err
			}

			return serveMCP(cmd, clients, listen, append(slices.Clone(appOpts), app.WithAllowlist(policy.allow)), mcp.Options{
				Version: Version, Logger: slog.Default(), Location: loc,
				InboxMaxAge: maxAge, InboxMaxCount: maxCount, DownloadDir: dlDir,
				ReadOnly: policy.readOnly, AttachDir: policy.attachDir, Confirm: policy.confirm,
				OnMessage: hook.program, HookFrom: hook.from, HookTimeout: hook.timeout,
			})
		},
	}

	cmd.Flags().DurationVar(&maxAge, "inbox-max-age", defaultInboxMaxAge,
		"delete inbox entries received longer ago than this (0: keep)")
	cmd.Flags().IntVar(&maxCount, "inbox-max-count", defaultInboxMaxCount,
		"keep at most this many inbox entries (0: no limit)")
	addMCPFlags(cmd, &dlDir)
	addHookFlags(cmd)

	return cmd
}

// addHookFlags adds the flags of --on-message to `mcp serve`.
func addHookFlags(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.String("on-message", "", "program to run for every incoming message of the --hook-from chats (absolute path)")
	flags.StringSlice("hook-from", nil,
		"user or group:<id> whose messages run --on-message (repeatable; '*' allows everyone; default: nobody)")
	flags.Duration("on-message-timeout", defaultHookTimeout, "kill an --on-message run after this long (0: no limit)")
}

// addMCPFlags adds the flags that `mcp serve` and `mcp doctor` share: the safety settings,
// --listen with its token, and --download-dir (into dlDir).
func addMCPFlags(cmd *cobra.Command, dlDir *string) {
	flags := cmd.Flags()
	flags.StringVar(dlDir, "download-dir", "",
		"directory for attachments that attachment_get downloads (default: attachments in the account's directory)")
	flags.Bool("read-only", false, "leave out the tools that send (send_message, react, delete_message, mark_read)")
	flags.StringSlice("allow-recipient", nil,
		"user or group:<id> the tools may send to (repeatable; '*' allows everyone; default: nobody)")
	flags.String("attach-dir", "", "the only directory send_message takes attachments from (default: none)")
	flags.Bool("confirm", false, "have the user confirm every message through the client (MCP elicitation)")
	flags.String("listen", "",
		"serve streamable HTTP on this loopback address (e.g. 127.0.0.1:8765) instead of stdin/stdout")
	flags.String("token-file", "", "file with the bearer token that --listen requires")
}

// bindMCPFlags binds the config keys of addMCPFlags to cmd's flags. Viper keeps one flag per
// key, and `mcp serve` and `mcp doctor` have the same flags, so each binds its own when it runs.
func bindMCPFlags(cfg *viper.Viper, cmd *cobra.Command) {
	for _, key := range []string{cfgReadOnly, cfgAllowRecipient, cfgAttachDir, cfgConfirm, cfgListen, cfgTokenFile} {
		cobra.CheckErr(cfg.BindPFlag(key, cmd.Flags().Lookup(strings.TrimPrefix(key, "mcp."))))
	}
}

// policy holds the safety settings of `mcp serve`.
type policy struct {
	readOnly  bool
	allow     *app.Allowlist
	attachDir string
	confirm   bool
}

// errNotADir means that --attach-dir is not a directory.
var errNotADir = errors.New("not a directory")

// loadPolicy reads and checks the safety settings.
func loadPolicy(cfg *viper.Viper) (policy, error) {
	out := policy{readOnly: cfg.GetBool(cfgReadOnly), confirm: cfg.GetBool(cfgConfirm)}

	var err error

	out.allow, err = app.ParseAllowlist(configList(cfg, cfgAllowRecipient))
	if err != nil {
		return policy{}, fmt.Errorf("--allow-recipient: %w", err)
	}

	if dir := cfg.GetString(cfgAttachDir); dir != "" {
		out.attachDir, err = filepath.Abs(dir)
		if err != nil {
			return policy{}, fmt.Errorf("--attach-dir: %w", err)
		}

		info, err := os.Stat(out.attachDir)
		if err != nil {
			return policy{}, fmt.Errorf("--attach-dir: %w", err)
		}

		if !info.IsDir() {
			return policy{}, fmt.Errorf("--attach-dir %s: %w", out.attachDir, errNotADir)
		}
	}

	return out, nil
}

// configList returns the list under key; a list from the environment is one string separated
// by commas.
func configList(cfg *viper.Viper, key string) []string {
	values := cfg.GetStringSlice(key)

	entries := make([]string, 0, len(values))
	for _, entry := range values {
		entries = append(entries, strings.FieldsFunc(entry, func(r rune) bool { return r == ',' })...)
	}

	return entries
}

// Errors of --on-message.
var (
	errHookNotAbs     = errors.New("--on-message: the program must be an absolute path")
	errHookNotExec    = errors.New("--on-message: the program is no executable file")
	errNoHookFrom     = errors.New("--on-message needs --hook-from: the users or groups whose messages run it")
	errHookNegTimeout = errors.New("--on-message-timeout must not be negative")
)

// hookConfig is what `mcp serve --on-message` runs, and for which chats; a zero value runs nothing.
type hookConfig struct {
	program string
	from    *app.Allowlist
	timeout time.Duration
}

// checkProgram checks that program is the absolute path of an executable file.
func checkProgram(program string) error {
	if !filepath.IsAbs(program) {
		return errHookNotAbs
	}

	info, err := os.Stat(program)
	if err != nil {
		return fmt.Errorf("--on-message: %w", err)
	}

	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%w: %s", errHookNotExec, program)
	}

	return nil
}

// loadHook reads and checks --on-message, --hook-from and --on-message-timeout, and points out
// --hook-from chats that p doesn't allow to send to.
func loadHook(cfg *viper.Viper, p policy) (hookConfig, error) {
	program := cfg.GetString(cfgOnMessage)
	if program == "" {
		return hookConfig{}, nil
	}

	err := checkProgram(program)
	if err != nil {
		return hookConfig{}, err
	}

	timeout := cfg.GetDuration(cfgHookTimeout)
	if timeout < 0 {
		return hookConfig{}, errHookNegTimeout
	}

	from, err := app.ParseAllowlist(configList(cfg, cfgHookFrom))
	if err != nil {
		return hookConfig{}, fmt.Errorf("--hook-from: %w", err)
	}

	if from.Empty() {
		return hookConfig{}, errNoHookFrom
	}

	if from.All() {
		slog.Warn("--hook-from '*': a message from anyone runs --on-message")
	}

	if missing := p.allow.Missing(from); len(missing) > 0 && !p.readOnly {
		slog.Warn("--hook-from has chats that --allow-recipient doesn't list: the hook can't reply there "+
			"(unless they name the same user differently)", "chats", strings.Join(missing, ", "))
	}

	return hookConfig{program: program, from: from, timeout: timeout}, nil
}

// logPolicy points out safety settings that may not be intended.
func logPolicy(p policy) {
	switch {
	case p.readOnly:
	case p.allow.All():
		slog.Warn("--allow-recipient '*': the MCP client may send to anyone")
	case p.allow.Empty():
		slog.Info("no --allow-recipient: send_message, react and delete_message reject every recipient")
	}
}

// Errors of --listen.
var (
	errNotLoopback = errors.New("--listen: the address must be on the loopback interface (127.0.0.1, ::1 or localhost)")
	errNoToken     = errors.New("--listen needs a bearer token: --token-file or GOSIGNAL_MCP_TOKEN")
	errShortToken  = fmt.Errorf("the bearer token must have at least %d characters", minTokenLength)
)

// listenConfig is where `mcp serve --listen` serves HTTP; a zero value serves stdin/stdout.
type listenConfig struct {
	addr  string
	token string
}

// loadListen reads and checks --listen and its bearer token.
func loadListen(cfg *viper.Viper) (listenConfig, error) {
	addr := cfg.GetString(cfgListen)
	if addr == "" {
		return listenConfig{}, nil
	}

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return listenConfig{}, fmt.Errorf("--listen: %w", err)
	}

	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return listenConfig{}, errNotLoopback
	}

	token := cfg.GetString(cfgToken)

	if file := cfg.GetString(cfgTokenFile); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return listenConfig{}, fmt.Errorf("--token-file: %w", err)
		}

		token = string(data)
	}

	token = strings.TrimSpace(token)

	switch {
	case token == "":
		return listenConfig{}, errNoToken
	case len(token) < minTokenLength:
		return listenConfig{}, errShortToken
	}

	return listenConfig{addr: addr, token: token}, nil
}

// serveMCP opens and connects the client and runs the MCP server on the command's stdin and
// stdout, or on HTTP with listen.
func serveMCP(
	cmd *cobra.Command, clients *clientOpener, listen listenConfig, appOpts []app.Option, opts mcp.Options,
) error {
	ctx := cmd.Context()

	client, err := clients.open(ctx)
	if err != nil {
		return err
	}
	defer closeClient(client)

	err = client.Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	if opts.DownloadDir == "" {
		opts.DownloadDir, err = defaultDownloadDir(ctx, clients, client)
		if err != nil {
			return err
		}
	}

	slog.Debug("mcp server ready", "download-dir", opts.DownloadDir, "attach-dir", opts.AttachDir,
		"read-only", opts.ReadOnly, "confirm", opts.Confirm)

	a := app.New(client, appOpts...)

	if listen.addr == "" {
		//nolint:wrapcheck // mcp wraps it
		return mcp.Serve(ctx, a, client.Events(), opts, cmd.InOrStdin(), cmd.OutOrStdout())
	}

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listen.addr)
	if err != nil {
		return fmt.Errorf("--listen: %w", err)
	}

	slog.Info("mcp server listening", "url", "http://"+listener.Addr().String()+mcp.HTTPPath)

	return mcp.ServeHTTP(ctx, a, client.Events(), opts, listener, listen.token) //nolint:wrapcheck // mcp wraps it
}

// errInboxLimits rejects negative --inbox-max-age or --inbox-max-count.
var errInboxLimits = errors.New("--inbox-max-age and --inbox-max-count must not be negative")

// defaultDownloadDir is "attachments" in the directory of the client's account.
func defaultDownloadDir(ctx context.Context, clients *clientOpener, client signal.Client) (string, error) {
	acc, err := client.Account(ctx)
	if err != nil {
		return "", fmt.Errorf("account: %w", err)
	}

	dir, err := store.AttachmentsDir(clients.cfg.GetString("data-dir"), acc.ACI)
	if err != nil {
		return "", fmt.Errorf("download dir: %w", err)
	}

	return dir, nil
}
