package cmd

import (
	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

func newStickerPacksCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	parent := &cobra.Command{
		Use:   "stickers",
		Short: "Install and list account-local sticker packs",
		Args:  cobra.NoArgs,
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List installed packs and sticker IDs",
		Args:  cobra.NoArgs,
	}
	install := &cobra.Command{
		Use:   "install <signal.art-link>",
		Short: "Download and cache a complete pack locally",
		Args:  cobra.ExactArgs(1),
	}
	run := func(cmd *cobra.Command, args []string) error {
		// Reject malformed secret-bearing links before opening an account.
		if cmd == install {
			_, err := app.ParseStickerPackURL(args[0])
			if err != nil {
				return err //nolint:wrapcheck // parser returns a key-free sentinel.
			}
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

		a := app.New(client, appOpts...)

		var packs []signal.StickerPack

		if cmd == install {
			pack, installErr := a.StickerPackInstall(cmd.Context(), args[0])
			err = installErr
			packs = []signal.StickerPack{pack}
		} else {
			packs, err = a.StickerPacks(cmd.Context())
		}

		if err != nil {
			return err //nolint:wrapcheck // app identifies the operation.
		}

		return printer.StickerPacks(packs)
	}
	list.RunE = run
	install.RunE = run
	parent.AddCommand(list, install)

	return parent
}
