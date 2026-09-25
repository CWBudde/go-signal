package cmd

import (
	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/spf13/cobra"
)

const deleteLong = `Delete one of your own messages for everyone (remote delete).

The recipients are the chats the message went to, as for send: an E.164 number, an ACI,
@username, group:<id> (or --group <id>), or self for a note to yourself. --target is the
message's timestamp in ms, as send prints it (or receive -o json for messages sent from your
other devices). Only your own messages can be deleted, and the Signal apps ignore deletes that
come too long after the message.

The message is also deleted on your other devices. Incoming messages are left on the server for
the next receive. The result is printed per recipient like for send; if sending to any
recipient (or group member) fails, the exit code is non-zero.`

func newDeleteCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	var (
		groups []string
		req    app.DeleteRequest
	)

	cmd := &cobra.Command{
		Use:   "delete <recipient>... --target <timestamp>",
		Short: "Delete your own message for everyone",
		Long:  deleteLong,
		Example: `  go-signal delete +4915112345678 --target 1790000000000
  go-signal delete --group 'Z3JvdXAt...=' --target 1790000000000`,
		RunE: func(cmd *cobra.Command, args []string) error {
			req.Recipients = recipientArgs(args, groups)

			return runSend(cmd, clients, printers, appOpts,
				func(a *app.App) (app.DeleteResult, error) { return a.Delete(cmd.Context(), req) },
				(*output.Printer).Delete)
		},
	}

	flags := cmd.Flags()
	flags.StringArrayVarP(&groups, "group", "g", nil, "delete in the group with this base64 ID (repeatable)")
	flags.Uint64Var(&req.Target, "target", 0, "the sent timestamp (ms) of the message to delete")
	cobra.CheckErr(cmd.MarkFlagRequired("target"))

	return cmd
}
