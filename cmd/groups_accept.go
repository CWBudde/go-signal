package cmd

import (
	"errors"
	"log/slog"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

var errGroupAcceptArgs = errors.New("groups accept requires exactly one group")

type groupAcceptCommandError struct {
	message string
	cause   error
}

func (e groupAcceptCommandError) Error() string { return e.message }
func (e groupAcceptCommandError) Unwrap() error { return e.cause }

func safeGroupAcceptError(stage string, err error) error {
	return groupAcceptCommandError{
		message: "groups accept: " + stage + " failed (details hidden to protect group secrets)",
		cause:   err,
	}
}

func newGroupsAcceptCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "accept <group>",
		Short: "Accept a known group invitation for the selected account",
		Long: `Accept an existing ACI or phone-number identity (PNI) invitation already known to
the selected account from sync or messages. Fresh full membership is a successful no-op.
Unknown master keys are not imported; invite links belong to groups join.

One invocation submits at most one acceptance, without automatic retries. After an
accepted or uncertain failure, inspect groups show or ask your phone or an administrator
before retrying. References can contain secrets; errors and logs hide sensitive details.

` + groupArgHelp,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return errGroupAcceptArgs
			}

			return (app.GroupAcceptRequest{Group: args[0]}).Check()
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Reuse the inherited setup traversal; hide the join-specific outer text.
			err := groupJoinSetup(cmd, args)
			if err != nil {
				return safeGroupAcceptError("configuration", err)
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			printer, err := printers.printer(cmd.OutOrStdout())
			if err != nil {
				return safeGroupAcceptError("output setup", err)
			}

			client, err := clients.open(cmd.Context())
			if err != nil {
				return safeGroupAcceptError("open client", err)
			}
			defer closeGroupAcceptClient(client)

			result, err := app.New(client).GroupsAccept(cmd.Context(), app.GroupAcceptRequest{Group: args[0]})
			if err != nil {
				return err //nolint:wrapcheck // app preserves secret-free stage and outcome guidance
			}

			err = printer.GroupAccept(result)
			if err != nil {
				return safeGroupAcceptError("write output", err)
			}

			return nil
		},
	}
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return safeGroupAcceptError("flag parsing", err)
	})

	return cmd
}

func closeGroupAcceptClient(client signal.Client) {
	err := client.Close()
	if err != nil {
		slog.Warn("close client", "error", safeGroupAcceptError("close client", err))
	}
}
