package cmd

import (
	"context"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

func newGroupsRemoveMembersCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "remove-members <group> <member>...",
		Short: "Remove members, revoke invitations or reject join requests",
		Long: `Remove members in one group change and notify the group. You must be a group
administrator. Members can be numbers, ACIs, @usernames or PNI:<uuid>; duplicates
are ignored. Explicit PNI targets revoke invitations only. Numbers may resolve
to both ACI and PNI, removing matching membership and invitations in one change.
Invited members have their invitations revoked, and join requests are rejected.
Every recipient must belong to the group. To remove yourself, use groups leave.

If the command fails, inspect the group before retrying: a network error may leave
the outcome uncertain. Notification failures after a confirmed change are logged.

` + groupArgHelp,
		Args: cobra.MinimumNArgs(2), //nolint:mnd // group and at least one member
		RunE: func(cmd *cobra.Command, args []string) error {
			req := app.RemoveGroupMembersRequest{Group: args[0], Members: args[1:]}

			return changeGroupMembers(cmd, clients, printers, req.Check,
				func(ctx context.Context, use *app.App) (signal.Group, error) {
					return use.GroupsRemoveMembers(ctx, req)
				})
		},
	}
}
