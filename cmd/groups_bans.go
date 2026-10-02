package cmd

import (
	"context"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

func newGroupsBanCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	return newGroupsBanChangeCmd(clients, printers, "ban", true,
		"Ban users and remove their membership, invitations or join requests")
}

func newGroupsUnbanCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	return newGroupsBanChangeCmd(clients, printers, "unban", false, "Lift user bans without adding membership")
}

func newGroupsBanChangeCmd(
	clients *clientOpener, printers *printerFactory, verb string, banned bool, short string,
) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " <group> <recipient>...",
		Short: short,
		Long: `Change a group's bans in one patch. You must be a full administrator, even
for an unchanged request. Recipients can be numbers, ACIs or @usernames; all are
resolved before any mutation. You cannot ban or unban yourself.

Banning removes full membership, revokes an ACI invitation or rejects a join
request in the same change and prevents joining or requesting through a group
link. Absent users can also be banned. Unbanning permits rejoining but does not
add membership. Existing PNI bans are shown and preserved, but these commands
cannot change them or revoke PNI-only invitations.

Duplicates and unchanged bans are skipped. A complete no-op preserves the
revision and existing ban times. Conflicts are not retried. Success prints fresh
server state. Inspect groups show before retrying accepted or uncertain failures;
notification failures after acceptance are logged. Preventive bans do not notify
absent users, and unbanning does not notify the unbanned user.

` + groupArgHelp,
		Args: cobra.MinimumNArgs(2), //nolint:mnd // group and at least one recipient
		RunE: func(cmd *cobra.Command, args []string) error {
			req := app.GroupBanRequest{Group: args[0], Members: args[1:], Banned: banned}

			return changeGroupMembers(cmd, clients, printers, req.Check,
				func(ctx context.Context, use *app.App) (signal.Group, error) {
					return use.GroupsSetBanned(ctx, req)
				})
		},
	}
}
