package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDevicesCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "devices",
		Short: "Manage the devices of the account",
		Args:  cobra.NoArgs,
	}

	cmd.AddCommand(newDevicesListCmd(clients, printers))

	return cmd
}

func newDevicesListCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all devices of the account; * marks this one",
		Args:  cobra.NoArgs,
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

			devices, err := client.Devices(cmd.Context())
			if err != nil {
				return fmt.Errorf("devices list: %w", err)
			}

			return printer.Devices(devices)
		},
	}
}
