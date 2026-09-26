package cmd

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/mcp"
	"github.com/cwbudde/go-signal/internal/signal"
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

func newMCPServeCmd(clients *clientOpener, loc *time.Location, appOpts []app.Option) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Serve the account to an MCP client on stdin/stdout",
		Long: `Serve the account to an MCP client (Claude Code, Claude Desktop, other agents) over
stdin/stdout, e.g.:

  claude mcp add signal -- go-signal mcp serve

The server connects before it answers the client and holds the account for as long as it runs,
so no other go-signal command can connect to the same account meanwhile. It fails at startup when
the account is in use or unlinked. It ends when the client closes stdin or on SIGINT/SIGTERM.
Stdout carries only the MCP protocol; logs go to stderr.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			client, err := clients.open(ctx)
			if err != nil {
				return err
			}
			defer closeClient(client)

			// Send-only until the inbox (PLAN.md 5.4) reads events: incoming messages stay on
			// the server for the next receive.
			err = client.Connect(ctx, signal.SendOnly())
			if err != nil {
				return fmt.Errorf("connect: %w", err)
			}

			slog.Debug("mcp server ready")

			return mcp.Serve(
				ctx, app.New(client, appOpts...),
				mcp.Options{Version: Version, Logger: slog.Default(), Location: loc},
				cmd.InOrStdin(), cmd.OutOrStdout(),
			)
		},
	}
}
