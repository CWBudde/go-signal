package cmd

import (
	"github.com/cwbudde/go-signal/internal/app"
	"github.com/spf13/cobra"
)

func newGroupsCreateCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	var req app.CreateGroupRequest

	cmd := &cobra.Command{
		Use:   "create <title>",
		Short: "Create a group with you as administrator",
		Long: `Create a group and notify its members. Repeat --member for each number, ACI or
@username. You are included automatically as administrator; without --member the group
contains only you. Duplicate members are ignored. Members whose profile credentials are
unavailable are invited instead. Members may edit group information and add members;
invite links are disabled. Prints the group, including its ID and pending invitations.

Creation is not idempotent: each invocation creates a new group. If an error includes a
group ID, inspect it with groups show before retrying; creation may already have succeeded.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req.Title = args[0]

			err := req.Check()
			if err != nil {
				return err //nolint:wrapcheck // self-contained validation error
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

			group, err := use.GroupsCreate(cmd.Context(), req)
			if err != nil {
				return err //nolint:wrapcheck // app wraps it
			}

			showNames(cmd.Context(), printer, use)

			return printer.Group(group)
		},
	}

	cmd.Flags().StringArrayVar(&req.Members, "member", nil,
		"member to add: number, ACI or @username (repeatable)")

	return cmd
}
