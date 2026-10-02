package cmd

import (
	"context"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

func newGroupsLinkCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use: "link", Short: "Inspect or change a group's invite link",
		Long: "Invite links contain a secret master key and password. Share them only with intended participants.",
	}
	cmd.AddCommand(newGroupsLinkShowCmd(clients, printers), newGroupsLinkUpdateCmd(clients, printers))

	return cmd
}

func newGroupsLinkShowCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	return &cobra.Command{
		Use: "show <group>", Short: "Show a group's invite-link state and active URL",
		Long: `Show fetches fresh invite-link state. You must be a full member. Disabled or
unknown access policies do not expose a URL. Generic groups show and list omit
invite links. Invite URLs cannot be used as group references.

` + groupArgHelp,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := app.GroupLinkRequest{Group: args[0]}

			return printGroupLink(cmd, clients, printers, req.Check,
				func(ctx context.Context, use *app.App) (signal.GroupLink, error) {
					return use.GroupsLinkShow(ctx, req)
				})
		},
	}
}

func newGroupsLinkUpdateCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	var (
		state string
		reset bool
	)

	cmd := &cobra.Command{
		Use: "update <group>", Short: "Change invite-link access or rotate its password",
		Long: `Update changes only supplied settings. Supply --state, --reset, or both.
States are disabled, enabled, and enabled-with-approval. --reset rotates the
password and invalidates the old URL; resetting a disabled link keeps it disabled.
You must be a full administrator, including for unchanged requests. A complete
no-op preserves the revision and password. Changes use one patch without retries.
Inspect groups link show before retrying accepted or uncertain failures. Success
prints fresh link state. Invite URLs cannot be used as group references.

` + groupArgHelp,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			update := signal.GroupLinkUpdate{Reset: reset}
			if cmd.Flags().Changed("state") {
				update.State = new(signal.GroupLinkState(state))
			}

			req := app.GroupLinkUpdateRequest{Group: args[0], Update: update}

			return printGroupLink(cmd, clients, printers, req.Check,
				func(ctx context.Context, use *app.App) (signal.GroupLink, error) {
					return use.GroupsLinkUpdate(ctx, req)
				})
		},
	}
	cmd.Flags().StringVar(&state, "state", "", "invite access: disabled, enabled or enabled-with-approval")
	cmd.Flags().BoolVar(&reset, "reset", false, "rotate the invite password, invalidating the old URL")

	return cmd
}

func printGroupLink(cmd *cobra.Command, clients *clientOpener, printers *printerFactory,
	check func() error, run func(context.Context, *app.App) (signal.GroupLink, error),
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

	link, err := run(cmd.Context(), app.New(client))
	if err != nil {
		return err
	}

	return printer.GroupLink(link) //nolint:wrapcheck // self-contained renderer error
}
