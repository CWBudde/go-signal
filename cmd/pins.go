package cmd

import (
	"fmt"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/spf13/cobra"
)

func newPinsCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	command := &cobra.Command{Use: "pins", Short: "Pin messages and inspect retained pin observations", Args: cobra.NoArgs}
	command.AddCommand(newPinAddCmd(clients, printers, appOpts), newPinRemoveCmd(clients, printers, appOpts),
		newPinListCmd(clients, printers, appOpts))

	return command
}

const pinSendHelp = `The recipients are the chats containing the target: an E.164 number, ACI,
@username, group:<base64-ID> (or --group <ID>), or self. --target identifies the
message as <author>:<timestamp>, with its author's ACI, number, @username or self
and its sent timestamp in milliseconds. No local copy of the target is required.

Group operations fetch current membership and attribute-edit permissions before
any sends: administrators may act; other full members need attribute-edit permission.
The recipients' clients decide whether the target exists and is eligible. Delivery
success does not confirm that the phone applied the operation. Results preserve
partial delivery without retries. Controls are synced to your other devices;
incoming events stay on the server for the next receive.`

func newPinAddCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	var (
		groups []string
		req    app.PinRequest
	)

	command := &cobra.Command{
		Use:   "add <recipient>... --target <author>:<timestamp> (--duration <seconds> | --forever)",
		Short: "Pin a message for an explicit duration or forever",
		Long: `Pin a message in the selected chats. Choose a positive duration in seconds
(up to 4294967295), or --forever. There is no default duration. Official clients
start finite durations from their own receipt time. Story pinning is unsupported.

` + pinSendHelp,
		RunE: func(command *cobra.Command, args []string) error {
			req.Recipients = recipientArgs(args, groups)

			err := req.Check()
			if err != nil {
				return fmt.Errorf("pin: %w", err)
			}

			return runSend(command, clients, printers, appOpts,
				func(use *app.App) (app.PinSendResult, error) { return use.Pin(command.Context(), req) },
				(*output.Printer).PinSend)
		},
	}
	flags := command.Flags()
	flags.StringArrayVarP(&groups, "group", "g", nil, "pin in this base64 group ID (repeatable)")
	flags.StringVar(&req.Target, "target", "", "message author and sent timestamp: <author>:<timestamp>")
	flags.Uint32Var(&req.DurationSeconds, "duration", 0, "positive pin duration in seconds (exclusive with --forever)")
	flags.BoolVar(&req.Forever, "forever", false, "pin without a duration limit")
	cobra.CheckErr(command.MarkFlagRequired("target"))
	command.MarkFlagsOneRequired("duration", "forever")
	command.MarkFlagsMutuallyExclusive("duration", "forever")

	return command
}

func newPinRemoveCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	var (
		groups []string
		req    app.UnpinRequest
	)

	command := &cobra.Command{
		Use: "remove <recipient>... --target <author>:<timestamp>", Short: "Unpin a message", Long: pinSendHelp,
		RunE: func(command *cobra.Command, args []string) error {
			req.Recipients = recipientArgs(args, groups)

			err := req.Check()
			if err != nil {
				return fmt.Errorf("unpin: %w", err)
			}

			return runSend(command, clients, printers, appOpts,
				func(use *app.App) (app.PinSendResult, error) { return use.Unpin(command.Context(), req) },
				(*output.Printer).PinSend)
		},
	}
	flags := command.Flags()
	flags.StringArrayVarP(&groups, "group", "g", nil, "unpin in this base64 group ID (repeatable)")
	flags.StringVar(&req.Target, "target", "", "message author and sent timestamp: <author>:<timestamp>")
	cobra.CheckErr(command.MarkFlagRequired("target"))

	return command
}

func newPinListCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	var req app.PinListRequest

	command := &cobra.Command{
		Use:   "list --chat <ACI|group:ID> [--scan-limit <number>]",
		Short: "List retained pin observations offline", Args: cobra.NoArgs,
		Long: `Inspect one bounded snapshot of inbox history collected by daemon or MCP receiving.
Supply a canonical non-nil ACI or group:<canonical-base64-ID>. No connection or user
resolution is performed. The account lock is required: stop daemon/MCP first.
Ordinary receive and outgoing sends do not populate this history.

The list preserves the latest retained pin/unpin per target, including unpins,
local expiry and observed target deletion. Events are applied in inbox receipt order;
finite expiry uses local receipt time. Completeness is always unknown. Missed events,
pruning, permissions, phone limits and disappearing timers prevent inferring the
current phone pins. --scan-limit bounds all chat entries (default 1000, maximum 10000).`,
		RunE: func(command *cobra.Command, _ []string) error {
			err := req.Check()
			if err != nil {
				return fmt.Errorf("pins list: %w", err)
			}

			printer, err := printers.printer(command.OutOrStdout())
			if err != nil {
				return err
			}

			client, err := clients.open(command.Context())
			if err != nil {
				return err
			}
			defer closeClient(client)

			use := app.New(client, appOpts...)

			state, err := use.PinList(command.Context(), req)
			if err != nil {
				return err //nolint:wrapcheck // app supplies context
			}

			showNames(command.Context(), printer, use)

			return printer.PinState(state)
		},
	}
	command.Flags().StringVar(&req.Chat, "chat", "", "canonical chat ACI or group:<base64-ID>")
	command.Flags().IntVar(&req.ScanLimit, "scan-limit", app.DefaultPinScanLimit, "maximum retained chat entries examined")
	cobra.CheckErr(command.MarkFlagRequired("chat"))

	return command
}
