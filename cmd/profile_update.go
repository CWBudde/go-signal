package cmd

import (
	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

func newProfileUpdateCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	var given, family, about, emoji string

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update your profile's name, about text or emoji",
		Long: `Update changes only the supplied text fields. Omitted flags preserve their current
values; an explicit empty value (for example --about=) clears a field. Supply at least
one flag. Spaces are preserved; quote values containing spaces. An unchanged update
does nothing. Limits are UTF-8 bytes: combined name 257 (including the family-name
delimiter), about 512, and about emoji 32.

The existing profile key, avatar, payment address, phone-number-sharing preference and
badges are preserved from the fetched server profile. Avoid concurrent profile edits on
other devices: v1 cannot detect or prevent simultaneous edits. Avatar, privacy and
payment changes, profile-key rotation and remote storage writing are unsupported.
Cleared names may appear stale elsewhere after a later storage sync fills an empty
cached display name; profile show reads the server profile directly.

An accepted write can still report verification, cache, persistence or notification
errors. If acceptance or an uncertain outcome is reported, inspect profile show before
retrying. Each invocation sends at most one profile write.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			update := changedProfileUpdate(cmd, signal.ProfileUpdate{
				GivenName: &given, FamilyName: &family, About: &about, AboutEmoji: &emoji,
			})

			err := update.Check()
			if err != nil {
				return err //nolint:wrapcheck // self-contained validation error
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

			result, err := app.New(client).ProfileUpdate(cmd.Context(), update)
			if err != nil {
				return err //nolint:wrapcheck // app retains acceptance and inspection guidance
			}

			return printer.ProfileUpdate(result)
		},
	}

	cmd.Flags().StringVar(&given, "given-name", "", "given name (empty clears; omitted preserves)")
	cmd.Flags().StringVar(&family, "family-name", "", "family name (empty clears; omitted preserves)")
	cmd.Flags().StringVar(&about, "about", "", "about text (empty clears; omitted preserves)")
	cmd.Flags().StringVar(&emoji, "about-emoji", "", "about emoji (empty clears; omitted preserves)")

	return cmd
}

// changedProfileUpdate retains pointers only for flags explicitly supplied by the user.
func changedProfileUpdate(cmd *cobra.Command, update signal.ProfileUpdate) signal.ProfileUpdate {
	for _, field := range []struct {
		flag   string
		target **string
	}{
		{"given-name", &update.GivenName},
		{"family-name", &update.FamilyName},
		{"about", &update.About},
		{"about-emoji", &update.AboutEmoji},
	} {
		if !cmd.Flags().Changed(field.flag) {
			*field.target = nil
		}
	}

	return update
}
