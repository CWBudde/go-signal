package cmd

import (
	"errors"
	"fmt"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

var errConfirmLeave = errors.New("groups leave removes you from the group and tells its members; " +
	"pass --yes to confirm")

// groupArgHelp explains the <group> argument of groups show and leave.
const groupArgHelp = `<group> is group:<id>, the base64 group ID or master key, or the group's title if exactly
one group listed before has it (ignoring case).`

func newGroupsCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "groups",
		Short: "List, show or leave groups",
		Args:  cobra.NoArgs,
	}

	cmd.AddCommand(
		newGroupsListCmd(clients, printers),
		newGroupsShowCmd(clients, printers),
		newGroupsLeaveCmd(clients, printers),
	)

	return cmd
}

func newGroupsListCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	return &cobra.Command{
		Use:   listCmd,
		Short: "List the known groups with member count and our role",
		Long: `List fetches the current state of every group go-signal knows (from "account sync" or a
message from the group) from the server. Groups we left or were removed from are listed as
"left" or "not a member". Incoming messages stay on the server for the next receive.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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

			groups, err := use.GroupsList(cmd.Context())
			if err != nil {
				return err //nolint:wrapcheck // app wraps it
			}

			showNames(cmd.Context(), printer, use)

			return printer.Groups(groups)
		},
	}
}

func newGroupsShowCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "show <group>",
		Short: "Show a group's details, members, invited and requesting members",
		Long:  "Show fetches the group's current state from the server.\n\n" + groupArgHelp,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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

			group, err := use.GroupsShow(cmd.Context(), args[0])
			if err != nil {
				return err //nolint:wrapcheck // app wraps it
			}

			showNames(cmd.Context(), printer, use)

			return printer.Group(group)
		},
	}
}

func newGroupsLeaveCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	var (
		yes bool
		req app.LeaveRequest
	)

	cmd := &cobra.Command{
		Use:   "leave <group>",
		Short: "Leave a group, decline an invitation or cancel a join request",
		Long: `Leave removes you from the group and tells its members, as leaving on the phone does. For a
group you are only invited to, it declines the invitation; for one you asked to join, it cancels
the request. The only admin of a group with other members has to make someone else admin first:
--promote <member> does that in the same change.

` + groupArgHelp,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return errConfirmLeave
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

			req.Group = args[0]

			res, err := use.GroupsLeave(cmd.Context(), req)
			if errors.Is(err, signal.ErrLastAdmin) {
				return fmt.Errorf("%w; name the new admin with --promote <member>", err)
			}

			if err != nil {
				return err //nolint:wrapcheck // app wraps it
			}

			showNames(cmd.Context(), printer, use)

			return printer.LeftGroup(res)
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "confirm leaving the group")
	cmd.Flags().StringArrayVar(&req.Promote, "promote", nil,
		"member to make admin before leaving: number, ACI or @username (repeatable)")

	return cmd
}
