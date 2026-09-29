package cmd

import (
	"context"
	"fmt"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

func changeGroupMembers(cmd *cobra.Command, clients *clientOpener, printers *printerFactory,
	check func() error, apply func(context.Context, *app.App) (signal.Group, error),
) error {
	err := check()
	if err != nil {
		return err
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

	use := app.New(client)

	group, err := apply(cmd.Context(), use)
	if err != nil {
		return err
	}

	showNames(cmd.Context(), printer, use)

	err = printer.Group(group)
	if err != nil {
		return fmt.Errorf("print group: %w", err)
	}

	return nil
}
