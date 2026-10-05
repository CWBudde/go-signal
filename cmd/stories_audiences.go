package cmd

import (
	"github.com/cwbudde/go-signal/internal/app"
	"github.com/spf13/cobra"
)

func newStoryAudiencesCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	return &cobra.Command{
		Use: "audiences", Short: "Fetch phone-defined private story audiences", Args: cobra.NoArgs,
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

			snapshot, err := app.New(client, appOpts...).StoryAudiences(cmd.Context())
			if err != nil {
				return err //nolint:wrapcheck // app wraps the operation
			}

			return printer.StoryAudiences(snapshot)
		},
	}
}
