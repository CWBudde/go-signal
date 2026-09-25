package cmd

import (
	"fmt"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/mdp/qrterminal/v3"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newLinkCmd(cfg *viper.Viper) *cobra.Command {
	var deviceName string

	cmd := &cobra.Command{
		Use:   "link",
		Short: "Link this client as a secondary device of an existing Signal account",
		Long: `Link prints a sgnl://linkdevice URI and a QR code. Scan it in the Signal app on your
phone (Settings > Linked devices) to add go-signal as a linked device.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			account, err := signal.Link(cmd.Context(), cfg.GetString("data-dir"), deviceName, func(uri string) {
				fmt.Fprintln(out, uri)
				qrterminal.GenerateHalfBlock(uri, qrterminal.L, out)
				fmt.Fprintln(out, "Scan this code in Signal on your phone: Settings > Linked devices.")
			})
			if err != nil {
				return fmt.Errorf("link: %w", err)
			}

			fmt.Fprintf(out, "Linked %s (ACI %s, device %d)\n", account.Number, account.ACI, account.DeviceID)

			return nil
		},
	}

	cmd.Flags().StringVar(&deviceName, "name", "go-signal", "device name shown on the phone")

	return cmd
}
