package cmd

import (
	"fmt"
	"strconv"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

const sendViewedLong = `Submit a viewed receipt for media you have viewed in a received message.

The sender is the original message's author: an E.164 number, an ACI UUID or @username.
For a group message, name the individual sender. Supply the original sent timestamp in
milliseconds, as shown by receive (timestamp=<ms> in plain output or timestamp in JSON).
Repeat --timestamp for several messages from that sender; duplicates are sent once.

This explicitly submits a VIEWED receipt. Receiving, printing or downloading media does
not send one automatically. Incoming messages stay queued for the next receive.
Success means the backend accepted the submission, not that the sender's phone displayed
it. The backend may skip receipts for unaccepted message requests. Viewed state is not
synced to your other devices by the current backend.`

func newReceiptsCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	cmd := &cobra.Command{Use: "receipts", Short: "Send explicit message receipts"}
	cmd.AddCommand(newSendViewedCmd(clients, printers, appOpts))

	return cmd
}

func newSendViewedCmd(clients *clientOpener, printers *printerFactory, appOpts []app.Option) *cobra.Command {
	var timestamps []string

	cmd := &cobra.Command{
		Use:   "send-viewed <sender> --timestamp <ms> [--timestamp <ms>]...",
		Short: "Send viewed receipts for received media",
		Long:  sendViewedLong,
		Args:  cobra.ExactArgs(1),
		Example: `  go-signal receipts send-viewed +4915112345678 --timestamp 1790000000000
  go-signal receipts send-viewed @alice.42 --timestamp 1790000000000 --timestamp 1790000000001`,
		RunE: func(cmd *cobra.Command, args []string) error {
			req := app.ViewedReceiptRequest{Sender: args[0], Timestamps: make([]uint64, 0, len(timestamps))}
			for _, value := range timestamps {
				timestamp, err := strconv.ParseUint(value, 10, 64)
				if err != nil {
					return fmt.Errorf("%w: timestamp %q: %w", signal.ErrInvalidReceipt, value, err)
				}

				req.Timestamps = append(req.Timestamps, timestamp)
			}

			return runSend(cmd, clients, printers, appOpts,
				func(a *app.App) (app.ViewedReceiptResult, error) { return a.SendViewedReceipt(cmd.Context(), req) },
				(*output.Printer).ViewedReceipt)
		},
	}

	cmd.Flags().StringArrayVar(&timestamps, "timestamp", nil,
		"original message's sent timestamp in milliseconds (repeatable)")
	cobra.CheckErr(cmd.MarkFlagRequired("timestamp"))

	return cmd
}
