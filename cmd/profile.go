package cmd

import "github.com/spf13/cobra"

func newProfileCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profile",
		Short: "Inspect and update your own Signal profile",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newProfileShowCmd(clients, printers), newProfileUpdateCmd(clients, printers))

	return cmd
}
