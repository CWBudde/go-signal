package cmd

import (
	"github.com/cwbudde/go-signal/internal/app"
	"github.com/spf13/cobra"
)

const contactsBlockLong = `Block users: Signal then drops their direct messages, typing indicators and calls
(messages in groups you share still arrive).

A user is an E.164 number, an ACI or @username. go-signal reads the current blocked list
from the storage service, adds the users and sends the complete list to your other
devices; the phone applies it and updates the storage service. This needs the storage
service key (run "go-signal account sync" if it is unknown). Until the storage service
shows the change, go-signal keeps it against storage syncs that say otherwise, for up to
7 days. Incoming messages are left on the server for the next receive.`

const contactsUnblockLong = `Unblock users, like "contacts block" blocks them: the complete new blocked list goes to
your other devices, and the phone updates the storage service.`

func newContactsCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "contacts",
		Short: "List, show and block contacts",
		Args:  cobra.NoArgs,
	}

	cmd.AddCommand(
		newContactsListCmd(clients, printers),
		newContactsShowCmd(clients, printers),
		newContactsBlockCmd(clients, printers, appOpts, true),
		newContactsBlockCmd(clients, printers, appOpts, false),
	)

	return cmd
}

func newContactsListCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	var req app.ContactsListRequest

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the known users, sorted by name",
		Long: `List the users go-signal knows from the phone's contacts, the storage service and
received messages: users with a name or number, and blocked users. Names come from the
nickname you gave them, else your phone's contacts, else their profile. The list is read
from the local store; "go-signal account sync" updates it.`,
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

			contacts, err := app.New(client).ContactsList(cmd.Context(), req)
			if err != nil {
				return err //nolint:wrapcheck // app wraps it
			}

			return printer.Contacts(contacts)
		},
	}

	cmd.Flags().BoolVar(&req.Blocked, "blocked", false, "list only blocked users")
	cmd.Flags().StringVarP(&req.Query, "query", "q", "", "list only users whose name, number or ACI contains this")

	return cmd
}

func newContactsShowCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "show <recipient>",
		Short: "Show what go-signal knows about a user",
		Long: `Show a user from the local store: names, number, ACI, PNI, whether they are blocked and
whether you accepted their message request. The user is an E.164 number, an ACI,
@username (looked up on the server) or self.`,
		Args: cobra.ExactArgs(1),
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

			contact, err := app.New(client).ContactsShow(cmd.Context(), args[0])
			if err != nil {
				return err //nolint:wrapcheck // app wraps it
			}

			return printer.Contact(contact)
		},
	}
}

func newContactsBlockCmd(
	clients *clientOpener, printers *printerFactory, appOpts []app.Option, block bool,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "block <recipient>...",
		Short: "Block users on all your devices",
		Long:  contactsBlockLong,
		Args:  cobra.MinimumNArgs(1),
	}

	run := (*app.App).ContactsBlock

	if !block {
		cmd.Use, cmd.Short, cmd.Long = "unblock <recipient>...", "Unblock users on all your devices", contactsUnblockLong
		run = (*app.App).ContactsUnblock
	}

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		printer, err := printers.printer(cmd.OutOrStdout())
		if err != nil {
			return err
		}

		client, err := clients.open(cmd.Context())
		if err != nil {
			return err
		}
		defer closeClient(client)

		res, err := run(app.New(client, appOpts...), cmd.Context(), args)
		if err != nil {
			return err
		}

		return printer.Block(res)
	}

	return cmd
}
