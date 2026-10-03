package cmd

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

var errGroupJoinArgs = errors.New("groups join requires exactly one invite link")

type groupJoinCommandError struct {
	message string
	cause   error
}

func (e groupJoinCommandError) Error() string { return e.message }
func (e groupJoinCommandError) Unwrap() error { return e.cause }

func safeGroupJoinError(stage string, err error) error {
	return groupJoinCommandError{
		message: "groups join: " + stage + " failed (details hidden to protect invite secrets)",
		cause:   err,
	}
}

func newGroupsJoinCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "join <link>",
		Short: "Join a group or request administrator approval using an invite link",
		Long: `Join uses a https://signal.group/ or sgnl://signal.group/ invite link for the
selected account. An open link joins directly; an approval link requests membership.
Fresh evidence of existing membership or a pending request returns a successful no-op.

One invocation submits at most one membership change, without automatic retries.
Accepted or uncertain failures require inspection before retrying. Ask an administrator
or inspect on your phone; groups show may be unavailable until approval.
The link contains secrets. Keep it private; errors and logs hide sensitive details.`,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return errGroupJoinArgs
			}

			return (app.JoinGroupRequest{Link: args[0]}).Check()
		},
		PersistentPreRunE: groupJoinSetup,
		RunE: func(cmd *cobra.Command, args []string) error {
			req := app.JoinGroupRequest{Link: args[0]}

			printer, err := printers.printer(cmd.OutOrStdout())
			if err != nil {
				return safeGroupJoinError("output setup", err)
			}

			client, err := clients.open(cmd.Context())
			if err != nil {
				return safeGroupJoinError("open client", err)
			}
			defer closeGroupJoinClient(client)

			result, err := app.New(client).GroupsJoin(cmd.Context(), req)
			if err != nil {
				return err //nolint:wrapcheck // app preserves secret-free guidance
			}

			err = printer.GroupJoin(result)
			if err != nil {
				return safeGroupJoinError("write output", err)
			}

			return nil
		},
	}
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return safeGroupJoinError("flag parsing", err)
	})

	return cmd
}

// groupJoinSetup runs the inherited setup once, with a secret-safe error boundary.
// A local persistent hook replaces Cobra's ancestor hook for this command only.
func groupJoinSetup(cmd *cobra.Command, args []string) error {
	for parent := cmd.Parent(); parent != nil; parent = parent.Parent() {
		if parent.PersistentPreRunE != nil {
			err := parent.PersistentPreRunE(cmd, args)
			if err != nil {
				return safeGroupJoinError("configuration", err)
			}

			return nil
		}

		if parent.PersistentPreRun != nil {
			parent.PersistentPreRun(cmd, args)

			return nil
		}
	}

	return nil
}

func closeGroupJoinClient(client signal.Client) {
	err := client.Close()
	if err != nil {
		slog.Warn("close client", "error",
			fmt.Errorf("groups join: %w", signal.GroupJoinOperationError(err, signal.GroupJoinResult{})))
	}
}
