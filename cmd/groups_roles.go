package cmd

import (
	"context"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

func newGroupsPromoteCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	return newGroupsMemberRoleCmd(clients, printers, "promote", signal.GroupRoleAdmin,
		"Make full group members administrators")
}

func newGroupsDemoteCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	return newGroupsMemberRoleCmd(clients, printers, "demote", signal.GroupRoleMember,
		"Make administrators ordinary group members")
}

func newGroupsMemberRoleCmd(clients *clientOpener, printers *printerFactory,
	verb string, role signal.GroupRole, short string,
) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " <group> <member>...",
		Short: short,
		Long: `Change full members' administrator roles in one group change. You must be a
full administrator. Members can be numbers, ACIs, @usernames or self; duplicates
are ignored. Every target must be a full member. Invitations and join requests
cannot have their roles changed. At least one full administrator must remain,
including in a self-only group. You may demote yourself when another admin remains.

All targets are checked against fresh server state before changing any role.
Existing requested roles are skipped; an entirely unchanged request sends nothing.
Conflicts are not retried. Inspect groups show before retrying accepted or uncertain
failures. Notification failures after a confirmed change are logged.

` + groupArgHelp,
		Args: cobra.MinimumNArgs(2), //nolint:mnd // group and at least one member
		RunE: func(cmd *cobra.Command, args []string) error {
			req := app.GroupMemberRoleRequest{Group: args[0], Members: args[1:], Role: role}

			return changeGroupMembers(cmd, clients, printers, req.Check,
				func(ctx context.Context, use *app.App) (signal.Group, error) {
					return use.GroupsSetMemberRole(ctx, req)
				})
		},
	}
}
