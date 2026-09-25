package cmd

import (
	"errors"
	"fmt"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

var errConfirmUnlink = errors.New("account unlink removes this device from the account and deletes " +
	"its local data (keys, messages); pass --yes to confirm")

func newAccountCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "Show or unlink the linked account",
		Args:  cobra.NoArgs,
	}

	cmd.AddCommand(
		newAccountShowCmd(clients, printers),
		newAccountUnlinkCmd(clients, printers),
	)

	return cmd
}

func newAccountShowCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show number, ACI, PNI and device of the account",
		Long:  "Show reads the account from the data dir; it doesn't contact the server.",
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

			acc, err := client.Account(cmd.Context())
			if err != nil {
				return fmt.Errorf("account show: %w", err)
			}

			return printer.Account(acc)
		},
	}
}

func newAccountUnlinkCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	var (
		yes  bool
		opts signal.UnlinkOptions
	)

	cmd := &cobra.Command{
		Use:   "unlink",
		Short: "Remove this device from the account and delete its local data",
		Long: `Unlink removes this device from the Signal account (as "Unlink" on the phone would) and
then deletes the account's local data. With --local-only it skips the server, e.g. when the
device was already removed on the phone or the server can't be reached. An account that
go-signal has seen being unlinked (see "account show") skips the server automatically.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !yes {
				return errConfirmUnlink
			}

			printer, err := printers.printer(cmd.OutOrStdout())
			if err != nil {
				return err
			}

			client, err := clients.open(cmd.Context())
			if err != nil {
				return err
			}
			defer closeClient(client)

			acc, err := client.Unlink(cmd.Context(), opts)
			if err != nil {
				return fmt.Errorf("account unlink: %w", err)
			}

			return printer.Unlinked(acc, opts.LocalOnly || acc.Unlinked())
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deleting the account's local data")
	cmd.Flags().BoolVar(&opts.LocalOnly, "local-only", false, "only delete the local data; don't contact the server")

	return cmd
}
