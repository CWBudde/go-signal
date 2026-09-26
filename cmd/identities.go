package cmd

import (
	"github.com/cwbudde/go-signal/internal/app"
	"github.com/spf13/cobra"
)

func newIdentitiesCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identities",
		Short: "List, verify and trust the identity keys (safety numbers) of other users",
		Long: `Every Signal user has an identity key; the safety number of two users is derived from
both their keys. go-signal trusts the first key it sees for a user. When that key changes later
(they reinstalled Signal, or someone is impersonating them), go-signal warns, reports an
identity-changed event in receive, and refuses to send to them until you trust the new key with
"identities trust". Compare the safety number ("identities show") with the one in the Signal app
on your phone to verify it.

Recipients are E.164 numbers, ACIs or @usernames. These commands don't connect, so a number only
works once go-signal has looked it up (e.g. by sending to it); the ACI always works.`,
		Args: cobra.NoArgs,
	}

	cmd.AddCommand(
		newIdentitiesListCmd(clients, printers),
		newIdentitiesShowCmd(clients, printers),
		newIdentitiesTrustCmd(clients, printers),
	)

	return cmd
}

func newIdentitiesListCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "list [<recipient>]",
		Short: "List the stored identity keys with their trust level",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			printer, err := printers.printer(cmd.OutOrStdout())
			if err != nil {
				return err
			}

			client, err := clients.open(cmd.Context())
			if err != nil {
				return err
			}
			defer closeClient(client)

			use := app.New(client)

			req := app.IdentitiesListRequest{}
			if len(args) > 0 {
				req.Recipient = args[0]
			}

			ids, err := use.IdentitiesList(cmd.Context(), req)
			if err != nil {
				return err //nolint:wrapcheck // app wraps it
			}

			showNames(cmd.Context(), printer, use)

			return printer.Identities(ids)
		},
	}
}

func newIdentitiesShowCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "show <recipient>",
		Short: "Show the safety number with a user, as a number and a QR code",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			printer, err := printers.printer(cmd.OutOrStdout())
			if err != nil {
				return err
			}

			client, err := clients.open(cmd.Context())
			if err != nil {
				return err
			}
			defer closeClient(client)

			use := app.New(client)

			number, err := use.IdentitiesShow(cmd.Context(), args[0])
			if err != nil {
				return err //nolint:wrapcheck // app wraps it
			}

			showNames(cmd.Context(), printer, use)

			return printer.SafetyNumber(number)
		},
	}
}

func newIdentitiesTrustCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	var safetyNumber string

	cmd := &cobra.Command{
		Use:   "trust <recipient>",
		Short: "Trust the current identity key of a user, so that sending to them works again",
		Long: `Trust the current identity key of a user. With --safety-number, the key is marked as
verified if the number matches the current safety number (spaces are ignored); otherwise nothing
changes and the command fails. Without it, the key is trusted unverified.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			printer, err := printers.printer(cmd.OutOrStdout())
			if err != nil {
				return err
			}

			client, err := clients.open(cmd.Context())
			if err != nil {
				return err
			}
			defer closeClient(client)

			use := app.New(client)

			identity, err := use.IdentitiesTrust(cmd.Context(), app.IdentitiesTrustRequest{
				Recipient: args[0], SafetyNumber: safetyNumber,
			})
			if err != nil {
				return err //nolint:wrapcheck // app wraps it
			}

			showNames(cmd.Context(), printer, use)

			return printer.TrustedIdentity(identity)
		},
	}

	cmd.Flags().StringVar(&safetyNumber, "safety-number", "",
		"verify the key: the 60-digit safety number from the Signal app")

	return cmd
}
