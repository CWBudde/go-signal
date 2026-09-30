package cmd

import (
	"github.com/cwbudde/go-signal/internal/app"
	"github.com/spf13/cobra"
)

func newProfileShowCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Fetch your current profile from the server",
		Long: `Show fetches the selected account's own server profile, retaining the given and
family name split. It displays text and the avatar path without downloading the avatar.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			printer, err := printers.printer(cmd.OutOrStdout())
			if err != nil {
				return err
			}

			client, err := clients.open(cmd.Context())
			if err != nil {
				return err
			}
			defer closeClient(client)

			profile, err := app.New(client).ProfileShow(cmd.Context())
			if err != nil {
				return err //nolint:wrapcheck // app wraps it
			}

			return printer.Profile(profile)
		},
	}
}
