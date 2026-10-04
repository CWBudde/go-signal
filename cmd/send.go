package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

const sendLong = `Send a message to one or more recipients.

A recipient is an E.164 number (+4915112345678), an ACI (UUID), @username
(nickname.discriminator), group:<id> (base64 group ID; or use --group <id>), or self
for a note to yourself. All recipients get the same message timestamp.

In the text, @{<recipient>} mentions a user (a number, ACI, @username or self). Files
attached with --attach (up to 100 MiB each) are uploaded once for all recipients.
--quote <author>:<timestamp> replies to a message: the author is a user recipient (self
for your own messages) and the timestamp is the message's time in ms, as receive -o json
shows it. --quote-text is the quoted text shown when the recipient no longer has the message.
It accepts the same @{<recipient>} mention syntax as message text.

--style <start>:<length>:<STYLE> formats message text (repeatable). Styles are bold,
italic, spoiler, strikethrough and monospace (case-insensitive). Offsets count UTF-16
code units in the text after mention substitution: each mention occupies one unit,
and an emoji outside the BMP occupies two. Ranges may overlap but cannot split a character.
Styles also apply to replacement text with --edit.

--sticker-pack <link> --sticker-id <number> sends one sticker from a Signal pack link
(https://signal.art/addstickers/#pack_id=...&pack_key=...). Sticker ID 0 is valid.
The selected image is fetched and uploaded once for all recipients. A sticker is sent alone:
message text, stdin, attachments, quotes and edits cannot be combined with these flags.

--edit <timestamp> replaces one of your own sent messages in the same chats. Use the original
message's timestamp in ms and provide the replacement text with -m or --stdin. Mentions and
other supplied content form the replacement; previous content is not loaded automatically.
Recipients enforce Signal's edit limits and may ignore an ineligible edit. The result reports
delivery of the edit, with its new timestamp, not whether the recipient applied it.

The message also shows up on your other devices (a sync transcript); a note to self only goes
there. Incoming messages are left on the server for the next receive.

The result is printed per recipient. If sending to any recipient (or group member) fails, the
exit code is non-zero.`

func newSendCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	var (
		message     string
		stdin       bool
		groups      []string
		stickerPack string
		stickerID   uint32
		req         app.SendRequest
	)

	cmd := &cobra.Command{
		Use:   "send <recipient>... (-m <text> | --stdin | --attach <file> | --sticker-pack <link> --sticker-id <number>)",
		Short: "Send a message to users, groups or yourself",
		Long:  sendLong,
		Example: `  go-signal send +4915112345678 -m "Hello"
  go-signal send @alice.42 self -m "Meeting at 10"
  go-signal send --group 'Z3JvdXAt...=' -m "Hi all"
  echo "Build done" | go-signal send self --stdin
  go-signal send +4915112345678 --attach photo.jpg -m "Look, @{@alice.42}"
  go-signal send +4915112345678 --quote +4915112345678:1790000000000 -m "Yes"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			err := sendStickerFlags(cmd, &req, stickerPack, stickerID)
			if err != nil {
				return err
			}

			if cmd.Flags().Changed("edit") && req.EditTarget == 0 {
				return fmt.Errorf("%w: --edit must be a nonzero message timestamp in ms", app.ErrInvalidEdit)
			}

			req.Recipients, req.Body = recipientArgs(args, groups), message

			if stdin {
				var err error

				req.Body, err = readBody(cmd.InOrStdin())
				if err != nil {
					return err
				}
			}

			return runSend(cmd, clients, printers, appOpts,
				func(a *app.App) (app.SendResult, error) { return a.Send(cmd.Context(), req) },
				(*output.Printer).Send)
		},
	}

	flags := cmd.Flags()
	flags.StringVarP(&message, "message", "m", "", "message text")
	flags.BoolVar(&stdin, "stdin", false, "read the message text from stdin")
	flags.StringArrayVarP(&groups, "group", "g", nil, "send to the group with this base64 ID (repeatable)")
	flags.StringArrayVar(&req.Attachments, "attach", nil, "attach this file (repeatable)")
	flags.StringVar(&req.Quote, "quote", "", "reply to the message `<author>:<timestamp>`")
	flags.StringVar(&req.QuoteText, "quote-text", "", "the quoted text, shown if the recipient lacks the message")
	flags.StringArrayVar(&req.Styles, "style", nil, "format `start:length:STYLE` (UTF-16 after mentions; repeatable)")
	flags.Uint64Var(&req.EditTarget, "edit", 0, "edit your message with this sent timestamp in ms")
	addStickerFlags(cmd, &stickerPack, &stickerID)
	cmd.MarkFlagsMutuallyExclusive("message", "stdin")
	cmd.MarkFlagsOneRequired("message", "stdin", "attach", "sticker-pack")

	return cmd
}

func addStickerFlags(cmd *cobra.Command, pack *string, stickerID *uint32) {
	cmd.Flags().StringVar(pack, "sticker-pack", "", "Signal sticker pack link (requires --sticker-id)")
	cmd.Flags().Uint32Var(stickerID, "sticker-id", 0, "sticker ID within the pack, including 0 (requires --sticker-pack)")
	cmd.MarkFlagsRequiredTogether("sticker-pack", "sticker-id")
}

// sendStickerFlags validates explicit flags before a client is opened or stdin is read.
func sendStickerFlags(cmd *cobra.Command, req *app.SendRequest, pack string, stickerID uint32) error {
	if !cmd.Flags().Changed("sticker-pack") {
		return nil
	}

	for _, flag := range []string{"message", "stdin", "attach", "quote", "quote-text", "edit", "style"} {
		if cmd.Flags().Changed(flag) {
			return fmt.Errorf("%w: stickers cannot be combined with --%s", signal.ErrInvalidSticker, flag)
		}
	}

	ref, err := app.ParseStickerPackURL(pack)
	if err != nil {
		return err //nolint:wrapcheck // parsing reports the invalid-sticker context
	}

	ref.StickerID = stickerID
	req.Sticker = &ref

	return nil
}

// runSend runs a sending use case (send, react, delete) on a new client and prints its result
// with show, also when some recipients failed (app.ErrSendFailed).
func runSend[R any](cmd *cobra.Command, clients *clientOpener, printers *printerFactory, appOpts []app.Option,
	run func(*app.App) (R, error), show func(*output.Printer, R) error,
) error {
	printer, err := printers.printer(cmd.OutOrStdout())
	if err != nil {
		return err
	}

	client, err := clients.open(cmd.Context())
	if err != nil {
		return err
	}
	defer closeClient(client)

	use := app.New(client, appOpts...)

	res, err := run(use)
	if err != nil && !errors.Is(err, app.ErrSendFailed) {
		return err
	}

	showNames(cmd.Context(), printer, use)

	printErr := show(printer, res)
	if printErr != nil {
		return printErr
	}

	return err
}

// readBody reads the message text from r, without the final line break(s).
func readBody(r io.Reader) (string, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read message from stdin: %w", err)
	}

	return strings.TrimRight(string(raw), "\r\n"), nil
}

// recipientArgs returns the recipient arguments followed by the --group IDs as group:<id>.
func recipientArgs(args, groups []string) []string {
	out := make([]string, 0, len(args)+len(groups))
	out = append(out, args...)

	for _, id := range groups {
		out = append(out, app.GroupPrefix+id)
	}

	return out
}

// showNames makes printer show the names of the contacts in the store. Without them, users show
// as they are identified.
func showNames(ctx context.Context, printer *output.Printer, use *app.App) {
	names, err := use.Names(ctx)
	if err != nil {
		slog.Debug("names not loaded", "error", err)

		return
	}

	printer.SetNames(names)
}
