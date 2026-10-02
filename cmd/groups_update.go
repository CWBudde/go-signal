package cmd

import (
	"fmt"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

type groupUpdateFlags struct {
	description         string
	timer               uint32
	announcementsOnly   bool
	editPermission      string
	addMemberPermission string
}

func newGroupsUpdateCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	var flags groupUpdateFlags

	cmd := &cobra.Command{
		Use:   "update <group>",
		Short: "Update a group's description, timer or permissions",
		Long: `Update changes only the supplied settings. Supply at least one flag. Omitted flags
preserve current values; --description= clears the description, --timer 0 disables
disappearing messages, and --announcements-only=false lets all members send messages.
The timer uses integer seconds. Permission values are members or admins.

Description and timer changes require full membership and permission to edit group
information. Changing announcement mode or permissions requires an administrator.
Every supplied setting is checked against fresh permissions, including unchanged values.
All changed settings are submitted in one patch. An unchanged update sends nothing.
Conflicts fail without automatic retries; accepted or uncertain errors require inspecting
groups show before retrying. Success prints fresh server state. Member notification
failures are logged separately. Use groups rename to change the title.

` + groupArgHelp,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			update, err := flags.update(cmd)
			if err != nil {
				return err
			}

			req := app.UpdateGroupRequest{Group: args[0], Update: update}

			err = req.Check()
			if err != nil {
				return err //nolint:wrapcheck // preflight error is self-contained
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

			group, err := use.GroupsUpdate(cmd.Context(), req)
			if err != nil {
				return err //nolint:wrapcheck // app preserves inspection guidance
			}

			showNames(cmd.Context(), printer, use)

			return printer.Group(group)
		},
	}

	flags.register(cmd)

	return cmd
}

func (f *groupUpdateFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.description, "description", "", "description (empty clears; omitted preserves)")
	cmd.Flags().Uint32Var(&f.timer, "timer", 0,
		"disappearing-message timer in seconds (0 disables; omitted preserves)")
	cmd.Flags().BoolVar(&f.announcementsOnly, "announcements-only", false,
		"only admins may send (explicit false disables; omitted preserves)")
	cmd.Flags().StringVar(&f.editPermission, "edit-permission", "",
		"who may edit group information: members or admins")
	cmd.Flags().StringVar(&f.addMemberPermission, "add-member-permission", "",
		"who may add or invite users: members or admins")
}

func (f groupUpdateFlags) update(cmd *cobra.Command) (signal.GroupUpdate, error) {
	var update signal.GroupUpdate

	if cmd.Flags().Changed("description") {
		update.Description = new(f.description)
	}

	if cmd.Flags().Changed("timer") {
		update.TimerSeconds = new(f.timer)
	}

	if cmd.Flags().Changed("announcements-only") {
		update.AnnouncementsOnly = new(f.announcementsOnly)
	}

	for _, permission := range []struct {
		flag   string
		value  string
		target **bool
	}{
		{"edit-permission", f.editPermission, &update.MembersCanEditAttributes},
		{"add-member-permission", f.addMemberPermission, &update.MembersCanAddMembers},
	} {
		if !cmd.Flags().Changed(permission.flag) {
			continue
		}

		switch permission.value {
		case "members":
			*permission.target = new(true)
		case "admins":
			*permission.target = new(false)
		default:
			return signal.GroupUpdate{}, fmt.Errorf("%w: --%s must be members or admins",
				signal.ErrInvalidGroupUpdate, permission.flag)
		}
	}

	return update, nil
}
