package cmd

import (
	"context"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

func newGroupsAddMembersCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "add-members <group> <member>...",
		Short: "Add members, invite users or approve join requests",
		Long: `Add ordinary members in one group change and notify the group. You must be a full
member with permission to add members. Members can be numbers, ACIs or @usernames.
Duplicates, existing members and pending invitations are skipped. Users whose profile
credentials are unavailable receive an invitation instead. Only administrators can
approve join requests. Banned users are rejected; this command does not unban them.
The output shows the group's current members and pending invitations after the change.

If the command fails, inspect the group before retrying: a network error may leave
the outcome uncertain. Notification failures after a confirmed change are logged.

` + groupArgHelp,
		Args: cobra.MinimumNArgs(2), //nolint:mnd // group and at least one member
		RunE: func(cmd *cobra.Command, args []string) error {
			req := app.AddGroupMembersRequest{Group: args[0], Members: args[1:]}

			return changeGroupMembers(cmd, clients, printers, req.Check,
				func(ctx context.Context, use *app.App) (signal.Group, error) {
					return use.GroupsAddMembers(ctx, req)
				})
		},
	}
}
