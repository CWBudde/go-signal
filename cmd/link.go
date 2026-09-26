package cmd

import (
	"fmt"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/mdp/qrterminal/v3"
	"github.com/spf13/cobra"
)

func newLinkCmd(clients *clientOpener) *cobra.Command {
	var (
		deviceName  string
		syncTimeout time.Duration
	)

	cmd := &cobra.Command{
		Use:   "link",
		Short: "Link this client as a secondary device of an existing Signal account",
		Long: `Link prints a sgnl://linkdevice URI and a QR code. Scan it in the Signal app on your
phone (Settings > Linked devices) to add go-signal as a linked device.

Once linked, it fetches the contacts and groups from the phone and the storage service (see
"account sync"), with progress on stderr, for at most --sync-timeout (0 skips it). A sync that
doesn't finish in time is only a warning: the device is linked, and the rest arrives with
"account sync" and received messages.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			client, err := clients.open(cmd.Context())
			if err != nil {
				return err
			}
			defer closeClient(client)

			account, err := client.Link(cmd.Context(), deviceName, func(uri string) {
				fmt.Fprintln(out, uri)
				qrterminal.GenerateHalfBlock(uri, qrterminal.L, out)
				fmt.Fprintln(out, "Scan this code in Signal on your phone: Settings > Linked devices.")
			})
			if err != nil {
				return fmt.Errorf("link: %w", err)
			}

			fmt.Fprintf(out, "Linked %s (ACI %s, device %d)\n", account.Number, account.ACI, account.DeviceID)

			if syncTimeout > 0 {
				linkSync(cmd, client, syncTimeout)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&deviceName, "name", "go-signal", "device name shown on the phone")
	cmd.Flags().DurationVar(&syncTimeout, "sync-timeout", app.DefaultSyncTimeout,
		"how long to wait for contacts and groups after linking (0 skips the sync)")

	return cmd
}

// linkSync runs the initial sync after link on client. The device is linked whatever happens
// now, so a failed or incomplete sync is only a warning on stderr.
func linkSync(cmd *cobra.Command, client signal.Client, timeout time.Duration) {
	stderr := cmd.ErrOrStderr()

	res, err := app.New(client).Sync(cmd.Context(), app.SyncRequest{Timeout: timeout, Progress: syncProgress(stderr)})
	if err != nil {
		fmt.Fprintf(stderr, "Warning: %v\n%s\n", err, syncLater)

		return
	}

	fmt.Fprintln(cmd.OutOrStdout(), syncSummary(res))
	warnIncomplete(stderr, res)
}
