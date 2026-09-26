package cmd

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

// syncLater tells what to do after an incomplete or failed sync.
const syncLater = `The rest arrives with received messages; run "go-signal account sync" to try again.`

var errConfirmUnlink = errors.New("account unlink removes this device from the account and deletes " +
	"its local data (keys, messages); pass --yes to confirm")

func newAccountCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "Show, sync or unlink the linked account",
		Args:  cobra.NoArgs,
	}

	cmd.AddCommand(
		newAccountShowCmd(clients, printers),
		newAccountSyncCmd(clients, printers),
		newAccountUnlinkCmd(clients, printers),
	)

	return cmd
}

func newAccountShowCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show number, ACI, PNI and device of the account",
		Long:  "Show reads the account from the data dir; it doesn't contact the server.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			printer, err := printers.printer(cmd.OutOrStdout())
			if err != nil {
				return err
			}

			client, err := clients.open(cmd.Context())
			if err != nil {
				return err
			}
			defer closeClient(client)

			acc, err := app.New(client).AccountShow(cmd.Context())
			if err != nil {
				return err //nolint:wrapcheck // app wraps it
			}

			return printer.Account(acc)
		},
	}
}

func newAccountSyncCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Fetch contacts and groups from the phone and the storage service",
		Long: `Sync asks the phone for its contact list and the storage service key, and fetches the
storage service (contacts with names, numbers and blocked state; groups), as link does right
after linking. Progress goes to stderr. When --timeout runs out first (0 waits until the sync is
complete), the result shows what is missing and a warning goes to stderr, but the exit code is 0:
what did arrive is stored. Incoming messages stay on the server for the next receive.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			printer, err := printers.printer(cmd.OutOrStdout())
			if err != nil {
				return err
			}

			client, err := clients.open(cmd.Context())
			if err != nil {
				return err
			}
			defer closeClient(client)

			stderr := cmd.ErrOrStderr()

			res, err := app.New(client).Sync(cmd.Context(), app.SyncRequest{
				Timeout:  timeout,
				Progress: syncProgress(stderr),
			})
			if err != nil {
				return err //nolint:wrapcheck // app wraps it
			}

			err = printer.Sync(res)
			if err != nil {
				return err //nolint:wrapcheck // output wraps it
			}

			warnIncomplete(stderr, res)

			return nil
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", app.DefaultSyncTimeout,
		"how long to wait for the phone and the storage service (0 waits until complete)")

	return cmd
}

// syncProgress returns a progress callback that writes a line per sync stage to w.
func syncProgress(w io.Writer) func(signal.SyncStage) {
	return func(stage signal.SyncStage) {
		if stage != signal.SyncDone {
			fmt.Fprintf(w, "Sync: %s...\n", stage)
		}
	}
}

// syncSummary sums up what the store holds after a sync ("Synced 12 contacts and 3 groups.").
func syncSummary(res app.SyncResult) string {
	return fmt.Sprintf("Synced %s and %s.", count(res.Contacts, "contact"), count(res.Groups, "group"))
}

// warnIncomplete writes a warning to w if the sync didn't finish.
func warnIncomplete(w io.Writer, res app.SyncResult) {
	if res.Incomplete != nil {
		fmt.Fprintf(w, "Warning: %v\n%s\n", res.Incomplete, syncLater)
	}
}

// count formats n with noun, in the plural unless n is 1.
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}

	return fmt.Sprintf("%d %ss", n, noun)
}

func newAccountUnlinkCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	var (
		yes bool
		req app.UnlinkRequest
	)

	cmd := &cobra.Command{
		Use:   "unlink",
		Short: "Remove this device from the account and delete its local data",
		Long: `Unlink removes this device from the Signal account (as "Unlink" on the phone would) and
then deletes the account's local data. With --local-only it skips the server, e.g. when the
device was already removed on the phone or the server can't be reached. An account that
go-signal has seen being unlinked (see "account show") skips the server automatically.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !yes {
				return errConfirmUnlink
			}

			printer, err := printers.printer(cmd.OutOrStdout())
			if err != nil {
				return err
			}

			client, err := clients.open(cmd.Context())
			if err != nil {
				return err
			}
			defer closeClient(client)

			res, err := app.New(client).AccountUnlink(cmd.Context(), req)
			if err != nil {
				return err //nolint:wrapcheck // app wraps it
			}

			return printer.Unlinked(res.Account, res.LocalOnly)
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deleting the account's local data")
	cmd.Flags().BoolVar(&req.LocalOnly, "local-only", false, "only delete the local data; don't contact the server")

	return cmd
}
