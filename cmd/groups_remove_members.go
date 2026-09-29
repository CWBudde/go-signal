package cmd

import (
	"github.com/cwbudde/go-signal/internal/app"
	"github.com/spf13/cobra"
)

func newGroupsRemoveMembersCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "remove-members <group> <member>...",
		Short: "Remove members, revoke invitations or reject join requests",
		Long: `Remove members in one group change and notify the group. You must be a group
administrator. Members can be numbers, ACIs or @usernames; duplicates are ignored.
Invited members have their invitations revoked, and join requests are rejected.
Every recipient must belong to the group. To remove yourself, use groups leave.
Invitations known only by phone-number identity (PNI) are not supported.

If the command fails, inspect the group before retrying: a network error may leave
the outcome uncertain. Notification failures after a confirmed change are logged.

` + groupArgHelp,
		Args: cobra.MinimumNArgs(2), //nolint:mnd // group and at least one member
		RunE: func(cmd *cobra.Command, args []string) error {
			req := app.RemoveGroupMembersRequest{Group: args[0], Members: args[1:]}

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

			group, err := use.GroupsRemoveMembers(cmd.Context(), req)
			if err != nil {
				return err //nolint:wrapcheck // app wraps it
			}

			showNames(cmd.Context(), printer, use)

			return printer.Group(group)
		},
	}
}
