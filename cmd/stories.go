package cmd

import (
	"fmt"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/spf13/cobra"
)

func newStoriesCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	cmd := &cobra.Command{Use: "stories", Short: "Send stories to a group", Args: cobra.NoArgs}
	cmd.AddCommand(newStorySendCmd(clients, printers, appOpts))

	return cmd
}

func newStorySendCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	var (
		req   app.StorySendRequest
		stdin bool
	)

	cmd := &cobra.Command{
		Use:   "send --group <id> (-m <text> | --stdin | --attach <file>)",
		Short: "Post a text or media story to a group",
		Long: `Post a story to one named group, whose full members form the audience.
Use a canonical base64 group ID known from sync or receive. A text card uses white
text on black with the default font; text is literal, including mention syntax.
Alternatively, --attach uploads one image/video (at most 100 MiB). Replies are allowed
unless --no-replies is supplied. Group stories do not use the chat's disappearing timer.

Your other devices receive a story transcript with the same timestamp. Results report
peer submission and sync failures; they do not prove that the phone displayed the story.
Incoming events stay on the server for the next receive. Private distribution lists and
My Story are not supported yet.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if stdin {
				var err error

				req.Text, err = readBody(cmd.InOrStdin())
				if err != nil {
					return err
				}
			}

			err := req.Check()
			if err != nil {
				return fmt.Errorf("story send: %w", err)
			}

			return runSend(cmd, clients, printers, appOpts,
				func(a *app.App) (app.StorySendResult, error) { return a.StorySend(cmd.Context(), req) },
				(*output.Printer).StorySend)
		},
	}
	flags := cmd.Flags()
	flags.StringVarP(&req.GroupID, "group", "g", "", "one canonical base64 group ID")
	flags.StringVarP(&req.Text, "message", "m", "", "literal story text")
	flags.BoolVar(&stdin, "stdin", false, "read story text from stdin")
	flags.StringVar(&req.Attachment, "attach", "", "one image/video file")
	flags.BoolVar(&req.NoReplies, "no-replies", false, "disable replies to this story")
	cmd.MarkFlagsMutuallyExclusive("message", "stdin", "attach")
	cmd.MarkFlagsOneRequired("message", "stdin", "attach")
	cobra.CheckErr(cmd.MarkFlagRequired("group"))

	return cmd
}
