package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/mcp"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/spf13/cobra"
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
through the mark_read tool.`

// Defaults of the inbox's retention.
const (
	defaultInboxMaxAge   = 30 * 24 * time.Hour
	defaultInboxMaxCount = 10000
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

			if dlDir == "" {
				dlDir, err = defaultDownloadDir(ctx, clients, client)
				if err != nil {
					return err
				}
			}

			slog.Debug("mcp server ready", "download-dir", dlDir)

			return mcp.Serve(
				ctx, app.New(client, appOpts...), client.Events(),
				mcp.Options{
					Version: Version, Logger: slog.Default(), Location: loc,
					InboxMaxAge: maxAge, InboxMaxCount: maxCount, DownloadDir: dlDir,
				},
				cmd.InOrStdin(), cmd.OutOrStdout(),
			)
		},
	}

	cmd.Flags().DurationVar(&maxAge, "inbox-max-age", defaultInboxMaxAge,
		"delete inbox entries received longer ago than this (0: keep)")
	cmd.Flags().IntVar(&maxCount, "inbox-max-count", defaultInboxMaxCount,
		"keep at most this many inbox entries (0: no limit)")
	cmd.Flags().StringVar(&dlDir, "download-dir", "",
		"directory for attachments that attachment_get downloads (default: attachments in the account's directory)")

	return cmd
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
