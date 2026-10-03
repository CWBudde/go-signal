package cmd

import (
	"errors"
	"log/slog"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

var errGroupCancelRequestArgs = errors.New("groups cancel-request requires exactly one group")

type groupCancelRequestCommandError struct {
	message string
	cause   error
}

func (e groupCancelRequestCommandError) Error() string { return e.message }
func (e groupCancelRequestCommandError) Unwrap() error { return e.cause }

func safeGroupCancelRequestError(stage string, err error) error {
	return groupCancelRequestCommandError{
		message: "groups cancel-request: " + stage + " failed (details hidden to protect group secrets)",
		cause:   err,
	}
}

var errConfirmCancelRequest = errors.New("groups cancel-request removes your pending join request; " +
	"pass --yes to confirm")

//nolint:funlen // sensitive lifecycle boundaries belong with this command
func newGroupsCancelRequestCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "cancel-request <group>",
		Short: "Cancel the selected account's pending group join request",
		Long: `Cancel only the selected account's pending ACI join request for a known group.
This does not leave full membership or decline an invitation. A fresh authenticated
preview showing no pending request is a successful no-op; server refusals are errors.
Unknown master keys are not imported; invite links belong to groups join.

One invocation submits at most one cancellation, without automatic retries. Verified
means the signed deletion was checked at the reported revision, or a fresh preview
showed no pending request; it does not promise future absence or device sync.
After an accepted or uncertain failure, inspect groups show or ask your phone or an
administrator before retrying. Requesters may be unable to use groups show.
References can contain secrets; errors and logs hide sensitive details.

` + groupArgHelp,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return errGroupCancelRequestArgs
			}

			return (app.GroupCancelRequestRequest{Group: args[0]}).Check()
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return errConfirmCancelRequest
			}

			// Reuse the inherited setup traversal; hide the join-specific outer text.
			err := groupJoinSetup(cmd, args)
			if err != nil {
				return safeGroupCancelRequestError("configuration", err)
			}

			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			printer, err := printers.printer(cmd.OutOrStdout())
			if err != nil {
				return safeGroupCancelRequestError("output setup", err)
			}

			client, err := clients.open(cmd.Context())
			if err != nil {
				return safeGroupCancelRequestError("open client", err)
			}
			defer closeGroupCancelRequestClient(client)

			result, err := app.New(client).GroupsCancelRequest(cmd.Context(), app.GroupCancelRequestRequest{Group: args[0]})
			if err != nil {
				return err //nolint:wrapcheck // app preserves secret-free stage and outcome guidance
			}

			err = printer.GroupCancelRequest(result)
			if err != nil {
				return safeGroupCancelRequestError("write output", err)
			}

			return nil
		},
	}
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return safeGroupCancelRequestError("flag parsing", err)
	})

	cmd.Flags().BoolVar(&yes, "yes", false, "confirm cancelling your join request")

	return cmd
}

func closeGroupCancelRequestClient(client signal.Client) {
	err := client.Close()
	if err != nil {
		slog.Warn("close client", "error", safeGroupCancelRequestError("close client", err))
	}
}
