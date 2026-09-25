package cmd

import (
	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/spf13/cobra"
)

const reactLong = `React to a message with an emoji, or take the reaction back with --remove.

The recipients are the chats the message is in, as for send: an E.164 number, an ACI,
@username, group:<id> (or --group <id>), or self for a note to yourself. --target names the
message as <author>:<timestamp>: the author is a user recipient (self for your own messages) and
the timestamp is the message's time in ms, as receive -o json shows it. --emoji is a single
emoji; to remove a reaction, give the emoji you reacted with.

The reaction also shows up on your other devices. Incoming messages are left on the server for
the next receive. The result is printed per recipient like for send; if sending to any
recipient (or group member) fails, the exit code is non-zero.`

func newReactCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	var (
		groups []string
		req    app.ReactRequest
	)

	cmd := &cobra.Command{
		Use:   "react <recipient>... --target <author>:<timestamp> --emoji <emoji> [--remove]",
		Short: "React to a message with an emoji",
		Long:  reactLong,
		Example: `  go-signal react +4915112345678 --target +4915112345678:1790000000000 --emoji 👍
  go-signal react --group 'Z3JvdXAt...=' --target @alice.42:1790000000000 --emoji ❤️
  go-signal react +4915112345678 --target self:1790000000000 --emoji 👍 --remove`,
		RunE: func(cmd *cobra.Command, args []string) error {
			req.Recipients = recipientArgs(args, groups)

			return runSend(cmd, clients, printers, appOpts,
				func(a *app.App) (app.ReactResult, error) { return a.React(cmd.Context(), req) },
				(*output.Printer).React)
		},
	}

	flags := cmd.Flags()
	flags.StringArrayVarP(&groups, "group", "g", nil, "react in the group with this base64 ID (repeatable)")
	flags.StringVar(&req.Target, "target", "", "the message to react to, `<author>:<timestamp>`")
	flags.StringVarP(&req.Emoji, "emoji", "e", "", "the reaction, a single emoji")
	flags.BoolVar(&req.Remove, "remove", false, "take back the reaction --emoji")
	cobra.CheckErr(cmd.MarkFlagRequired("target"))
	cobra.CheckErr(cmd.MarkFlagRequired("emoji"))

	return cmd
}
