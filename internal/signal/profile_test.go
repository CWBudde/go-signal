//nolint:goconst // literal expectations are independent fixtures
package signal_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

const initialProfileGivenName = "Alice"

func TestProfileUpdateCheck(t *testing.T) { //nolint:cyclop,funlen // table-driven validation cases
	t.Parallel()

	tests := []struct {
		name    string
		update  signal.ProfileUpdate
		invalid bool
	}{
		{name: "empty request", invalid: true},
		{name: "explicit clear", update: signal.ProfileUpdate{About: new("")}},
		{name: "about 512", update: signal.ProfileUpdate{About: new(strings.Repeat("a", 512))}},
		{name: "about 513", update: signal.ProfileUpdate{About: new(strings.Repeat("a", 513))}, invalid: true},
		{name: "emoji 32", update: signal.ProfileUpdate{AboutEmoji: new(strings.Repeat("😀", 8))}},
		{name: "emoji 33", update: signal.ProfileUpdate{AboutEmoji: new(strings.Repeat("😀", 8) + "a")}, invalid: true},
		{name: "given 257", update: signal.ProfileUpdate{GivenName: new(strings.Repeat("a", 257))}},
		{name: "given 258", update: signal.ProfileUpdate{GivenName: new(strings.Repeat("a", 258))}, invalid: true},
		{name: "family 256", update: signal.ProfileUpdate{FamilyName: new(strings.Repeat("a", 256))}},
		{name: "family 257", update: signal.ProfileUpdate{FamilyName: new(strings.Repeat("a", 257))}, invalid: true},
		{name: "combined 257", update: signal.ProfileUpdate{GivenName: new("a"), FamilyName: new(strings.Repeat("b", 255))}},
		{
			name: "combined 258",
			update: signal.ProfileUpdate{
				GivenName:  new("ab"),
				FamilyName: new(strings.Repeat("b", 255)),
			},
			invalid: true,
		},
		{
			name: "empty family adds no delimiter",
			update: signal.ProfileUpdate{
				GivenName:  new(strings.Repeat("a", 257)),
				FamilyName: new(""),
			},
		},
		{name: "spaces retained", update: signal.ProfileUpdate{GivenName: new("  Mary Jane  ")}},
	}

	for _, field := range []string{"given", "family", "about", "emoji"} {
		for _, invalid := range []string{"\xff", "a\x00b"} {
			update := signal.ProfileUpdate{}

			switch field {
			case "given":
				update.GivenName = new(invalid)
			case "family":
				update.FamilyName = new(invalid)
			case "about":
				update.About = new(invalid)
			case "emoji":
				update.AboutEmoji = new(invalid)
			}

			t.Run(field+" invalid "+invalid, func(t *testing.T) {
				t.Parallel()

				err := update.Check()

				if !errors.Is(err, signal.ErrInvalidProfileUpdate) {
					t.Fatalf("Check() error = %v; want ErrInvalidProfileUpdate", err)
				}
			})
		}
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.update.Check()

			if test.invalid {
				if !errors.Is(err, signal.ErrInvalidProfileUpdate) {
					t.Fatalf("Check() error = %v; want ErrInvalidProfileUpdate", err)
				}
			} else if err != nil {
				t.Fatalf("Check() error = %v", err)
			}
		})
	}
}

func TestProfileUpdateApply(t *testing.T) { //nolint:funlen // table-driven merge cases
	t.Parallel()

	original := signal.Profile{
		ACI:        "account-aci",
		GivenName:  initialProfileGivenName,
		FamilyName: "Smith",
		About:      "original about",
		AboutEmoji: "😀",
		AvatarPath: "/avatar",
	}

	tests := []struct {
		name     string
		original signal.Profile
		update   signal.ProfileUpdate
		want     signal.Profile
		changed  bool
		invalid  bool
	}{
		{
			name:     "multiword given preserves family",
			original: original,
			update:   signal.ProfileUpdate{GivenName: new(" Mary Jane ")},
			want: signal.Profile{
				ACI:        "account-aci",
				GivenName:  " Mary Jane ",
				FamilyName: "Smith",
				About:      "original about",
				AboutEmoji: "😀",
				AvatarPath: "/avatar",
			},
			changed: true,
		},
		{
			name:     "clear about",
			original: original,
			update:   signal.ProfileUpdate{About: new("")},
			want: signal.Profile{
				ACI:        "account-aci",
				GivenName:  initialProfileGivenName,
				FamilyName: "Smith",
				AboutEmoji: "😀",
				AvatarPath: "/avatar",
			},
			changed: true,
		},
		{
			name:     "unchanged",
			original: original,
			update:   signal.ProfileUpdate{GivenName: new(initialProfileGivenName)},
			want:     original,
		},
		{
			name:     "omitted family overflows merged name",
			original: original,
			update:   signal.ProfileUpdate{GivenName: new(strings.Repeat("a", 257))},
			invalid:  true,
		},
		{
			name:     "omitted given overflows merged name",
			original: original,
			update:   signal.ProfileUpdate{FamilyName: new(strings.Repeat("a", 256))},
			invalid:  true,
		},
		{
			name:     "family only 256 includes delimiter",
			original: signal.Profile{ACI: "account-aci"},
			update:   signal.ProfileUpdate{FamilyName: new(strings.Repeat("a", 256))},
			want: signal.Profile{
				ACI:        "account-aci",
				FamilyName: strings.Repeat("a", 256),
			},
			changed: true,
		},
		{
			name:     "family only 257 includes delimiter",
			original: signal.Profile{ACI: "account-aci"},
			update:   signal.ProfileUpdate{FamilyName: new(strings.Repeat("a", 257))},
			invalid:  true,
		},
		{
			name:     "clear both names",
			original: original,
			update: signal.ProfileUpdate{
				GivenName:  new(""),
				FamilyName: new(""),
			},
			want: signal.Profile{
				ACI:        "account-aci",
				About:      "original about",
				AboutEmoji: "😀",
				AvatarPath: "/avatar",
			},
			changed: true,
		},
		{name: "empty request", original: original, invalid: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, changed, err := test.update.Apply(test.original)

			if test.invalid {
				if !errors.Is(err, signal.ErrInvalidProfileUpdate) {
					t.Fatalf("Apply() error = %v; want ErrInvalidProfileUpdate", err)
				}

				if changed || got != (signal.Profile{}) {
					t.Fatalf("invalid Apply() = %+v, %t", got, changed)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if got != test.want || changed != test.changed {
				t.Fatalf("Apply() = %+v, %t; want %+v, %t", got, changed, test.want, test.changed)
			}
		})
	}
}
