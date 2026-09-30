package cmd_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
	"github.com/cwbudde/go-signal/internal/store"
)

const (
	profileCmd          = "profile"
	updateCmd           = "update"
	aboutFlag           = "--about"
	profileGivenFlag    = "--given-name"
	profileFamilyFlag   = "--family-name"
	profileGiven        = "Alice Marie"
	profileFamily       = "Smith"
	profileAbout        = "Hello\n世界" //nolint:gosmopolitan // non-ASCII profile fixture
	profileAcceptedHint = "write accepted; inspect profile show before retrying"
)

var errProfileTransport = errors.New("profile transport failed")

func profileFake() *signaltest.Fake {
	acc := *testAccount()

	return &signaltest.Fake{
		Linked: []signal.Account{acc},
		Profiles: map[string]signal.Profile{
			acc.ACI: {
				ACI: acc.ACI, GivenName: profileGiven, FamilyName: profileFamily,
				About: profileAbout, AboutEmoji: "🐶", AvatarPath: "profiles/avatar",
			},
		},
	}
}

func TestProfileCommandsGolden(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		args     []string
		attempts int
	}{
		{"profile_show_plain", []string{profileCmd, showCmd}, 0},
		{"profile_show_json", []string{"-o", formatJSON, profileCmd, showCmd}, 0},
		{"profile_update_plain", []string{profileCmd, updateCmd, profileGivenFlag, "Zoë Marie", aboutFlag, "New about"}, 1},
		{
			"profile_update_json",
			[]string{"-o", formatJSON, profileCmd, updateCmd, profileGivenFlag, "Zoë Marie", aboutFlag, "New about"},
			1,
		},
		{"profile_unchanged_plain", []string{profileCmd, updateCmd, profileFamilyFlag, profileFamily}, 1},
		{"profile_unchanged_json", []string{"-o", formatJSON, profileCmd, updateCmd, profileFamilyFlag, profileFamily}, 1},
		{
			"profile_clear_json",
			[]string{
				"-o", formatJSON, profileCmd, updateCmd,
				profileGivenFlag + "=", "--family-name=", "--about=", "--about-emoji=",
			},
			1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := profileFake()

			out, err := run(t, fake, test.args...)
			if err != nil {
				t.Fatal(err)
			}

			golden(t, test.name, out)

			if got := len(fake.ProfileUpdates()); got != test.attempts {
				t.Errorf("update attempts = %d, want %d", got, test.attempts)
			}

			if got := fake.Connects(); !slices.Equal(got, []string{testAccount().ACI}) {
				t.Errorf("connected accounts = %v", got)
			}
		})
	}
}

func TestProfileClearAndOmittedFlags(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		args   []string
		given  string
		family string
		about  string
		emoji  string
	}{
		{"clear about", []string{"--about="}, profileGiven, profileFamily, "", "🐶"},
		{"clear given", []string{profileGivenFlag + "="}, "", profileFamily, profileAbout, "🐶"},
		{"clear family", []string{"--family-name="}, profileGiven, "", profileAbout, "🐶"},
		{"clear emoji", []string{"--about-emoji="}, profileGiven, profileFamily, profileAbout, ""},
		{
			"family only",
			[]string{profileGivenFlag + "=", profileFamilyFlag, "van der Berg"},
			"", "van der Berg", profileAbout, "🐶",
		},
		{
			"multiword",
			[]string{profileGivenFlag, " Ana María ", profileFamilyFlag, "de la Cruz"},
			" Ana María ", "de la Cruz", profileAbout, "🐶",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := profileFake()

			out, err := run(t, fake, append([]string{profileCmd, updateCmd}, test.args...)...)
			if err != nil || out == "" {
				t.Fatalf("output/error = %q/%v", out, err)
			}

			want := signal.Profile{
				ACI: testAccount().ACI, GivenName: test.given, FamilyName: test.family,
				About: test.about, AboutEmoji: test.emoji, AvatarPath: "profiles/avatar",
			}
			if got := fake.Profiles[testAccount().ACI]; got != want {
				t.Errorf("profile = %+v, want %+v", got, want)
			}

			calls := fake.ProfileUpdates()
			if len(calls) != 1 {
				t.Fatalf("update attempts = %d, want 1", len(calls))
			}

			assertProfileFlagPointers(t, test.args, calls[0].Update)
		})
	}
}

func assertProfileFlagPointers(t *testing.T, args []string, update signal.ProfileUpdate) {
	t.Helper()

	for _, field := range []struct {
		flag  string
		value *string
	}{
		{profileGivenFlag, update.GivenName},
		{profileFamilyFlag, update.FamilyName},
		{aboutFlag, update.About},
		{"--about-emoji", update.AboutEmoji},
	} {
		supplied := slices.Contains(args, field.flag) || slices.Contains(args, field.flag+"=")
		if (field.value != nil) != supplied {
			t.Errorf("%s pointer = %v, supplied = %v", field.flag, field.value, supplied)
		}
	}
}

func TestProfileCommandValidationBeforeOpen(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{profileCmd, showCmd, "unexpected-argument"},
		{profileCmd, updateCmd, aboutFlag, "text", "unexpected-argument"},
		{profileCmd, updateCmd},
		{profileCmd, updateCmd, profileGivenFlag, strings.Repeat("a", 257), profileFamilyFlag, "b"},
		{profileCmd, updateCmd, profileFamilyFlag, strings.Repeat("a", 257)},
		{profileCmd, updateCmd, profileGivenFlag, strings.Repeat("a", 258)},
		{profileCmd, updateCmd, aboutFlag, strings.Repeat("a", 513)},
		{profileCmd, updateCmd, "--about-emoji", strings.Repeat("🐶", 9)},
		{profileCmd, updateCmd, aboutFlag, "bad\x00text"},
		{profileCmd, updateCmd, profileGivenFlag, "bad\xff"},
		{profileCmd, updateCmd, "--unknown", "text"},
		{profileCmd, showCmd, "--unknown"},
	} {
		fake := profileFake()

		out, err := run(t, fake, args...)
		if err == nil || out != "" {
			t.Errorf("%q: output/error = %q/%v", args, out, err)
		}

		if len(fake.Opened()) != 0 || len(fake.ProfileUpdates()) != 0 {
			t.Errorf("%q opened a client or attempted an update", args)
		}
	}
}

func TestProfileCommandFailures(t *testing.T) {
	t.Parallel()

	uncertain := fmt.Errorf("outcome uncertain; inspect profile show before retrying: %w", errProfileTransport)
	for _, test := range []struct {
		name     string
		setup    func(*signaltest.Fake)
		want     error
		attempts int
		hint     string
	}{
		{"open", func(f *signaltest.Fake) { f.OpenErr = store.ErrAccountInUse }, store.ErrAccountInUse, 0, ""},
		{"connect", func(f *signaltest.Fake) { f.ConnectErr = errProfileTransport }, errProfileTransport, 0, ""},
		{
			"read", func(f *signaltest.Fake) { f.OwnProfileErr = signal.ErrProfileKeyChanged },
			signal.ErrProfileKeyChanged, 1, "",
		},
		{
			"rejected", func(f *signaltest.Fake) { f.UpdateProfileErr = signal.ErrProfileRejected },
			signal.ErrProfileRejected, 1, "",
		},
		{
			"uncertain", func(f *signaltest.Fake) { f.UpdateProfileErr = uncertain },
			errProfileTransport, 1, "inspect profile show before retrying",
		},
		{
			"accepted verified", func(f *signaltest.Fake) { f.ProfileFollowUpErr = errProfileTransport },
			errProfileTransport, 1, profileAcceptedHint,
		},
		{
			"accepted unverified", func(f *signaltest.Fake) { f.ProfileVerificationFails = true },
			signal.ErrProfileVerification, 1, profileAcceptedHint,
		},
		{"merged overflow", func(f *signaltest.Fake) {
			profile := f.Profiles[testAccount().ACI]
			profile.GivenName = strings.Repeat("a", 252)
			f.Profiles[testAccount().ACI] = profile
		}, signal.ErrInvalidProfileUpdate, 1, ""},
	} {
		for _, format := range []string{formatPlain, formatJSON} {
			t.Run(test.name+"/"+format, func(t *testing.T) {
				t.Parallel()

				fake := profileFake()
				test.setup(fake)

				out, err := run(t, fake, "-o", format, profileCmd, updateCmd, profileFamilyFlag, "Johnson")
				if !errors.Is(err, test.want) || out != "" {
					t.Fatalf("output/error = %q/%v, want empty/%v", out, err, test.want)
				}

				if test.hint != "" && !strings.Contains(err.Error(), test.hint) {
					t.Errorf("missing guidance %q: %v", test.hint, err)
				}

				if got := len(fake.ProfileUpdates()); got != test.attempts {
					t.Errorf("update attempts = %d, want %d", got, test.attempts)
				}
			})
		}
	}
}

func TestProfileShowFailures(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		fake := profileFake()
		fake.OwnProfileErr = signal.ErrInvalidProfile

		out, err := run(t, fake, "-o", format, profileCmd, showCmd)
		if !errors.Is(err, signal.ErrInvalidProfile) || out != "" {
			t.Errorf("show output/error = %q/%v", out, err)
		}
	}
}

func TestProfileCommandCancellation(t *testing.T) {
	t.Parallel()

	fake := profileFake()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	out, err := runContext(t, ctx, fake, profileCmd, updateCmd, aboutFlag, "new")
	if !errors.Is(err, context.Canceled) || out != "" || len(fake.ProfileUpdates()) != 1 {
		t.Errorf("canceled update = %q/%v, attempts %d", out, err, len(fake.ProfileUpdates()))
	}

	if fake.Profiles[testAccount().ACI].About != profileAbout {
		t.Error("canceled update changed the profile")
	}
}

func TestProfileCommandAccountSelection(t *testing.T) {
	t.Parallel()

	for _, selector := range []string{secondAccount().Number, secondAccount().ACI} {
		for _, verb := range []string{showCmd, updateCmd} {
			fake := profileFake()
			fake.Linked = append(fake.Linked, secondAccount())
			fake.Profiles[secondAccount().ACI] = signal.Profile{ACI: secondAccount().ACI, GivenName: "Second"}

			args := []string{"-o", formatJSON, "-a", selector, profileCmd, verb}
			if verb == updateCmd {
				args = append(args, aboutFlag, "selected")
			}

			out, err := run(t, fake, args...)
			if err != nil {
				t.Fatal(err)
			}

			var doc struct{ Profile signal.Profile }

			err = json.Unmarshal([]byte(out), &doc)
			if err != nil {
				t.Fatal(err)
			}

			if doc.Profile.ACI != secondAccount().ACI || fake.Profiles[testAccount().ACI].About != profileAbout {
				t.Errorf("selected profile = %+v; first account changed", doc.Profile)
			}

			if got := fake.Connects(); !slices.Equal(got, []string{secondAccount().ACI}) {
				t.Errorf("connected accounts = %v", got)
			}
		}
	}
}

func TestProfileCommandInvalidAccount(t *testing.T) {
	t.Parallel()

	for _, selector := range []string{"", "alice"} {
		fake := profileFake()
		fake.Linked = append(fake.Linked, secondAccount())
		args := []string{profileCmd, showCmd}
		want := signal.ErrAccountRequired

		if selector != "" {
			args = append(args, "-a", selector)
			want = signal.ErrInvalidAccount
		}

		out, err := run(t, fake, args...)
		if !errors.Is(err, want) || out != "" {
			t.Errorf("account %q = %q/%v, want %v", selector, out, err, want)
		}
	}
}

func TestProfileCommandTree(t *testing.T) {
	t.Parallel()

	root := cmd.NewRootCmd()

	profile, _, err := root.Find([]string{profileCmd})
	if err != nil || profile == root {
		t.Fatalf("profile command = %v/%v", profile, err)
	}

	names := make([]string, 0, len(profile.Commands()))
	for _, child := range profile.Commands() {
		names = append(names, child.Name())
	}

	if !slices.Equal(names, []string{showCmd, updateCmd}) {
		t.Errorf("profile subcommands = %v", names)
	}

	update, _, err := root.Find([]string{profileCmd, updateCmd})
	if err != nil {
		t.Fatal(err)
	}

	for _, flag := range []string{"given-name", "family-name", "about", "about-emoji"} {
		if update.Flags().Lookup(flag) == nil {
			t.Errorf("missing --%s", flag)
		}
	}
}
