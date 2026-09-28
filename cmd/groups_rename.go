package cmd

import (
	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

func newGroupsRenameCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "rename <group> <title>",
		Short: "Rename a group and update its cached title",
		Long: `Rename changes the group's title and tells its members. You must be a full member
with permission to edit group information. An unchanged title does nothing. Quote titles
containing spaces; blank titles are rejected.

` + groupArgHelp,
		Args: cobra.ExactArgs(2), //nolint:mnd // group and title
		RunE: func(cmd *cobra.Command, args []string) error {
			err := signal.ValidateGroupTitle(args[1])
			if err != nil {
				return err //nolint:wrapcheck // validation error is self-contained
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

			group, err := use.GroupsRename(cmd.Context(), app.RenameGroupRequest{Group: args[0], Title: args[1]})
			if err != nil {
				return err //nolint:wrapcheck // app wraps it
			}

			showNames(cmd.Context(), printer, use)

			return printer.Group(group)
		},
	}
}
