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
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "receive",
		Short: "Receive messages until the first message arrives or the timeout expires",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			err := receive(ctx, clients, cmd.OutOrStdout())
			if errors.Is(err, context.DeadlineExceeded) {
				slog.Info("timeout reached without a message", "timeout", timeout)

				return nil
			}

			if err != nil {
				return fmt.Errorf("receive: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", defaultReceiveTimeout, "stop after this long")

	return cmd
}

// receive prints events until the first message, a logout, or ctx is done.
// Structured output replaces the %+v dump in Phase 3.5.
func receive(ctx context.Context, clients *clientOpener, out io.Writer) error {
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

			switch evt := evt.(type) {
			case *signal.Message:
				return nil
			case *signal.Connection:
				if evt.State == signal.StateLoggedOut {
					return loggedOutError(evt.Err)
				}
			}
		}
	}
}

func loggedOutError(cause error) error {
	if cause == nil {
		return signal.ErrLoggedOut
	}

	return fmt.Errorf("%w: %w", signal.ErrLoggedOut, cause)
}
