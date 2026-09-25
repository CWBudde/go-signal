package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const defaultReceiveTimeout = time.Minute

func newReceiveCmd(cfg *viper.Viper) *cobra.Command {
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "receive",
		Short: "Receive messages until the first data message arrives or the timeout expires",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			err := signal.Receive(ctx, cfg.GetString("data-dir"), func(evt any) {
				fmt.Fprintf(out, "%T %+v\n", evt, evt)
			})
			if errors.Is(err, context.DeadlineExceeded) {
				slog.Info("timeout reached without a data message", "timeout", timeout)

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
