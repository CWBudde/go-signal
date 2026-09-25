package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

const defaultReceiveTimeout = time.Minute

func newReceiveCmd(clients *clientOpener) *cobra.Command {
	var (
		timeout time.Duration
		follow  bool
	)

	cmd := &cobra.Command{
		Use:   "receive",
		Short: "Receive messages until the first message arrives or the timeout expires",
		Long: `Receive messages until the first message arrives or the timeout expires.

With --follow, receive streams events until it is interrupted (SIGINT/SIGTERM). Interrupting
receive ends it normally: events already printed are acknowledged to the server, the rest is
delivered again next time. A second signal exits right away.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			if !follow {
				var cancel context.CancelFunc

				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}

			err := receive(ctx, clients, cmd.OutOrStdout(), follow)

			switch {
			case errors.Is(err, context.DeadlineExceeded):
				slog.Info("timeout reached without a message", "timeout", timeout)

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

	cmd.Flags().DurationVar(&timeout, "timeout", defaultReceiveTimeout, "stop after this long")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "stream events until interrupted")
	cmd.MarkFlagsMutuallyExclusive("timeout", "follow")

	return cmd
}

// receive prints events until the first message (unless follow), the connection is lost for
// good, or ctx is done. Structured output replaces the %+v dump in Phase 3.5.
func receive(ctx context.Context, clients *clientOpener, out io.Writer, follow bool) error {
	client, err := clients.open(ctx)
	if err != nil {
		return err
	}
	defer closeClient(client)

	err = client.Connect(ctx)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for events: %w", ctx.Err())
		case evt, ok := <-client.Events():
			if !ok {
				return nil
			}

			fmt.Fprintf(out, "%T %+v\n", evt, evt)

			done, err := lastEvent(evt, follow)
			if done {
				return err
			}
		}
	}
}

// lastEvent reports whether receive ends with evt: the first message (unless follow), or a
// connection that is lost for good, whose error it returns.
func lastEvent(evt signal.Event, follow bool) (bool, error) {
	switch evt := evt.(type) {
	case *signal.Message:
		return !follow, nil
	case *signal.Connection:
		switch evt.State {
		case signal.StateLoggedOut:
			return true, loggedOutError(evt.Err)
		case signal.StateFailed:
			if evt.Err == nil {
				return true, signal.ErrConnectionFailed
			}

			return true, evt.Err
		case signal.StateConnected, signal.StateDisconnected, signal.StateError:
		}
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
