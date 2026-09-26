package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

func newMCPCmd(clients *clientOpener, loc *time.Location, appOpts []app.Option) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Model Context Protocol (MCP) server for AI agents",
		Args:  cobra.NoArgs,
	}

	cmd.AddCommand(newMCPServeCmd(clients, loc, appOpts))

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
mcp.allow-recipient: [...]) or as GOSIGNAL_MCP_ALLOW_RECIPIENT etc. (a list separated by commas).`

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
)

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

			policy, err := loadPolicy(clients.cfg)
			if err != nil {
				return err
			}

			return serveMCP(cmd, clients, append(slices.Clone(appOpts), app.WithAllowlist(policy.allow)), mcp.Options{
				Version: Version, Logger: slog.Default(), Location: loc,
				InboxMaxAge: maxAge, InboxMaxCount: maxCount, DownloadDir: dlDir,
				ReadOnly: policy.readOnly, AttachDir: policy.attachDir, Confirm: policy.confirm,
			})
		},
	}

	cmd.Flags().DurationVar(&maxAge, "inbox-max-age", defaultInboxMaxAge,
		"delete inbox entries received longer ago than this (0: keep)")
	cmd.Flags().IntVar(&maxCount, "inbox-max-count", defaultInboxMaxCount,
		"keep at most this many inbox entries (0: no limit)")
	cmd.Flags().StringVar(&dlDir, "download-dir", "",
		"directory for attachments that attachment_get downloads (default: attachments in the account's directory)")
	addPolicyFlags(cmd, clients.cfg)

	return cmd
}

// addPolicyFlags adds the flags of the safety settings and binds them to their config keys.
func addPolicyFlags(cmd *cobra.Command, cfg *viper.Viper) {
	flags := cmd.Flags()
	flags.Bool("read-only", false, "leave out the tools that send (send_message, react, delete_message, mark_read)")
	flags.StringSlice("allow-recipient", nil,
		"user or group:<id> the tools may send to (repeatable; '*' allows everyone; default: nobody)")
	flags.String("attach-dir", "", "the only directory send_message takes attachments from (default: none)")
	flags.Bool("confirm", false, "have the user confirm every message through the client (MCP elicitation)")

	for _, key := range []string{cfgReadOnly, cfgAllowRecipient, cfgAttachDir, cfgConfirm} {
		cobra.CheckErr(cfg.BindPFlag(key, flags.Lookup(strings.TrimPrefix(key, "mcp."))))
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

	// A list from the environment is one string separated by commas.
	values := cfg.GetStringSlice(cfgAllowRecipient)

	entries := make([]string, 0, len(values))
	for _, entry := range values {
		entries = append(entries, strings.FieldsFunc(entry, func(r rune) bool { return r == ',' })...)
	}

	var err error

	out.allow, err = app.ParseAllowlist(entries)
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

	switch {
	case out.readOnly:
	case out.allow.All():
		slog.Warn("--allow-recipient '*': the MCP client may send to anyone")
	case out.allow.Empty():
		slog.Info("no --allow-recipient: send_message, react and delete_message reject every recipient")
	}

	return out, nil
}

// serveMCP opens and connects the client and runs the MCP server on the command's stdin and
// stdout.
func serveMCP(cmd *cobra.Command, clients *clientOpener, appOpts []app.Option, opts mcp.Options) error {
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

	//nolint:wrapcheck // mcp wraps it
	return mcp.Serve(ctx, app.New(client, appOpts...), client.Events(), opts, cmd.InOrStdin(), cmd.OutOrStdout())
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
