package output

import (
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/cwbudde/go-signal/internal/signal"
)

// ProfileJSON is the own-profile object shared by profile show and profile update.
// Empty text fields remain present so scripts can distinguish cleared values.
type ProfileJSON struct {
	ACI        string `json:"aci"`
	GivenName  string `json:"givenName"`
	FamilyName string `json:"familyName"`
	About      string `json:"about"`
	AboutEmoji string `json:"aboutEmoji"`
	AvatarPath string `json:"avatarPath,omitempty"`
}

type profileDoc struct {
	Version int         `json:"version"`
	Profile ProfileJSON `json:"profile"`
}

type profileUpdateDoc struct {
	Version  int         `json:"version"`
	Profile  ProfileJSON `json:"profile"`
	Changed  bool        `json:"changed"`
	Accepted bool        `json:"accepted"`
	Verified bool        `json:"verified"`
}

func newProfileJSON(profile signal.Profile) ProfileJSON {
	return ProfileJSON{
		ACI: profile.ACI, GivenName: profile.GivenName, FamilyName: profile.FamilyName,
		About: profile.About, AboutEmoji: profile.AboutEmoji, AvatarPath: profile.AvatarPath,
	}
}

// Profile prints the selected account's own server profile (profile show).
func (p *Printer) Profile(profile signal.Profile) error {
	if p.format == JSON {
		return p.writeJSON(profileDoc{Version: SchemaVersion, Profile: newProfileJSON(profile)})
	}

	return p.profilePlain(profile, "")
}

// ProfileUpdate prints a successful own-profile update or an unchanged profile.
// Callers must return operation errors before invoking this printer.
func (p *Printer) ProfileUpdate(result signal.ProfileUpdateResult) error {
	if p.format == JSON {
		return p.writeJSON(profileUpdateDoc{
			Version: SchemaVersion, Profile: newProfileJSON(result.Profile),
			Changed: result.Changed, Accepted: result.Accepted, Verified: result.Verified,
		})
	}

	label := "Profile unchanged"
	if result.Changed {
		label = "Updated profile"
	}

	return p.profilePlain(result.Profile, label)
}

func (p *Printer) profilePlain(profile signal.Profile, label string) error {
	table := tabwriter.NewWriter(p.w, 0, 0, 1, ' ', 0)
	if label != "" {
		fmt.Fprintln(table, label)
	}

	fmt.Fprintf(table, "ACI:\t%s\n", strconv.Quote(profile.ACI))
	fmt.Fprintf(table, "Given name:\t%s\n", strconv.Quote(profile.GivenName))
	fmt.Fprintf(table, "Family name:\t%s\n", strconv.Quote(profile.FamilyName))
	fmt.Fprintf(table, "About:\t%s\n", strconv.Quote(profile.About))
	fmt.Fprintf(table, "About emoji:\t%s\n", strconv.Quote(profile.AboutEmoji))
	fmt.Fprintf(table, "Avatar path:\t%s\n", strconv.Quote(profile.AvatarPath))

	return flush(table)
}
