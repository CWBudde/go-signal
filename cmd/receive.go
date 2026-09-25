package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

// defaultReceiveTimeout is the default inactivity timeout of a one-shot receive.
const defaultReceiveTimeout = 5 * time.Second

var (
	// errMaxNegative rejects a negative --max.
	errMaxNegative = errors.New("--max must not be negative")
	// errIdle ends a one-shot receive after its inactivity timeout.
	errIdle = errors.New("no events within the timeout")
)

const receiveLong = `Receive the messages waiting on the server.

By default receive runs one-shot, e.g. from cron: it prints events as they arrive and exits once
nothing has arrived for --timeout (connection changes count as activity), or after --max events.
With --follow, receive streams events until it is interrupted (SIGINT/SIGTERM) or --max events
have arrived. Interrupting receive ends it normally: events already printed are acknowledged to
the server, the rest is delivered again next time. A second signal exits right away.

--max counts events with content (messages, receipts, reactions, typing, …), not connection
changes or queueEmpty. Events after the last one counted stay on the server.

receive exits with 0 when it ends normally (timeout, --max, interrupt) and with 3 when this device
was unlinked from the account.

Plain output prints one line per event: "[time] <sender> → <dest>: <text>", with placeholders
such as [attachment image/jpeg 12.3 KB photo.jpg] or [unsupported call] for content that can't
be shown as text. "me" is this account. With -o json, every event (including connection
changes) is one JSON document per line (NDJSON, see docs/json.md).

With --send-read-receipts, the sender of each incoming message (not your own messages from other
devices) gets a read receipt once the message was printed, collected for about a second into one
receipt per sender. Your other devices then mark the messages as read, too.`

func newReceiveCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	var (
		timeout time.Duration
		follow  bool
		maxEvts int
		rcpts   bool
	)

	cmd := &cobra.Command{
		Use:   "receive",
		Short: "Receive the messages waiting on the server",
		Long:  receiveLong,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if maxEvts < 0 {
				return errMaxNegative
			}

			printer, err := printers.printer(cmd.OutOrStdout())
			if err != nil {
				return err
			}

			opts := receiveOptions{max: maxEvts, readReceipts: rcpts}
			if !follow {
				opts.idle = timeout
			}

			err = receive(cmd.Context(), clients, printer, opts)

			switch {
			case errors.Is(err, errIdle):
				slog.Debug("no events within the timeout", "timeout", timeout)

				return nil
			case errors.Is(err, context.Canceled):
				slog.Debug("receive interrupted")

				return nil
			case err != nil:
				return fmt.Errorf("receive: %w", err)
			default:
				return nil
			}
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", defaultReceiveTimeout,
		"exit after this long without events (one-shot mode)")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "stream events until interrupted")
	cmd.Flags().IntVar(&maxEvts, "max", 0, "exit after this many events with content (0: no limit)")
	cmd.Flags().BoolVar(&rcpts, "send-read-receipts", false, "send read receipts for received messages")
	cmd.MarkFlagsMutuallyExclusive("timeout", "follow")

	return cmd
}

// receiveOptions says when receive ends.
type receiveOptions struct {
	// idle is the inactivity timeout; zero waits forever (--follow).
	idle time.Duration
	// max is the number of content events after which receive ends; zero means no limit.
	max int
	// readReceipts sends read receipts for received messages (--send-read-receipts).
	readReceipts bool
	// receipts collects them; receive sets it on the connected client.
	receipts *app.ReadReceipts
}

// receive prints events until opts says to stop (errIdle, or nil after opts.max events), the
// connection is lost for good, or ctx is done.
func receive(ctx context.Context, clients *clientOpener, printer *output.Printer, opts receiveOptions) error {
	client, err := clients.open(ctx)
	if err != nil {
		return err
	}
	defer closeClient(client)

	err = client.Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	if opts.readReceipts {
		opts.receipts = app.New(client).ReadReceipts()
	}

	return receiveEvents(ctx, client.Events(), printer, opts)
}

// receiveEvents prints events until opts says to stop, the connection is lost for good, or ctx
// is done.
func receiveEvents(
	ctx context.Context, events <-chan signal.Event, printer *output.Printer, opts receiveOptions,
) error {
	// Without an idle timeout, idle stays nil and never fires.
	var (
		idle  <-chan time.Time
		timer *time.Timer
	)

	if opts.idle > 0 {
		timer = time.NewTimer(opts.idle)
		defer timer.Stop()

		idle = timer.C
	}

	remaining := opts.max

	receipts := newReceiptSender(opts.receipts)
	defer receipts.close(ctx)

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for events: %w", ctx.Err())
		case <-idle:
			return errIdle
		case <-receipts.due():
			receipts.flush(ctx)
		case evt, ok := <-events:
			if !ok {
				return nil
			}

			if timer != nil {
				timer.Reset(opts.idle)
			}

			done, err := handleEvent(printer, evt, &remaining)
			receipts.handled(evt, err)

			if done {
				return err
			}
		}
	}
}

// handleEvent prints evt and reports whether receive ends with it: after the last of --max
// events (remaining counts down to zero; it starts at zero without a limit), or at a connection
// that is lost for good, whose error it returns.
func handleEvent(printer *output.Printer, evt signal.Event, remaining *int) (bool, error) {
	err := printer.Event(evt)
	if err != nil {
		return true, fmt.Errorf("print event: %w", err)
	}

	done, err := lastEvent(evt)
	if done {
		return true, err
	}

	if *remaining > 0 && hasContent(evt) {
		*remaining--

		return *remaining == 0, nil
	}

	return false, nil
}

// hasContent reports whether evt counts toward --max: anything but connection changes and
// queueEmpty.
func hasContent(evt signal.Event) bool {
	switch evt.(type) {
	case *signal.Connection, *signal.QueueEmpty:
		return false
	default:
		return true
	}
}

// lastEvent reports whether receive ends with evt, a connection that is lost for good, and
// returns its error.
func lastEvent(evt signal.Event) (bool, error) {
	conn, ok := evt.(*signal.Connection)
	if !ok {
		return false, nil
	}

	switch conn.State {
	case signal.StateLoggedOut:
		return true, loggedOutError(conn.Err)
	case signal.StateFailed:
		if conn.Err == nil {
			return true, signal.ErrConnectionFailed
		}

		return true, conn.Err
	case signal.StateConnected, signal.StateDisconnected, signal.StateError:
	}

	return false, nil
}

// loggedOutError returns the error of a StateLoggedOut event. The client already reports
// signal.UnlinkedError; anything else is wrapped so that it still maps to ExitUnlinked.
func loggedOutError(cause error) error {
	switch {
	case errors.Is(cause, signal.ErrDeviceUnlinked):
		return cause
	case cause == nil:
		return signal.ErrDeviceUnlinked
	default:
		return fmt.Errorf("%w: %w", signal.ErrDeviceUnlinked, cause)
	}
}
