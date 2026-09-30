package app_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	profileFamilyName = "Example"
	profileAvatarPath = "avatar/path"
)

func profileFake() *signaltest.Fake {
	account := testAccount()

	return &signaltest.Fake{
		Linked: []signal.Account{account},
		Profiles: map[string]signal.Profile{account.ACI: {
			ACI: account.ACI, GivenName: "Alice", FamilyName: profileFamilyName,
			About: "Hello", AboutEmoji: "👋", AvatarPath: profileAvatarPath,
		}},
	}
}

func TestProfileShow(t *testing.T) {
	t.Parallel()

	fake := profileFake()

	profile, err := open(t, fake).ProfileShow(t.Context())
	if err != nil || profile != fake.Profiles[testAccount().ACI] {
		t.Fatalf("ProfileShow = %+v, %v", profile, err)
	}

	if len(fake.Connects()) != 1 || len(fake.ProfileUpdates()) != 0 {
		t.Fatalf("connects/updates = %v/%+v", fake.Connects(), fake.ProfileUpdates())
	}
}

func TestProfileUpdate(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		update  signal.ProfileUpdate
		want    signal.Profile
		changed bool
	}{
		{
			name:   "omitted fields preserved and empty clears",
			update: signal.ProfileUpdate{GivenName: new("Bob"), About: new("")},
			want: signal.Profile{
				ACI: testAccount().ACI, GivenName: "Bob", FamilyName: profileFamilyName,
				AboutEmoji: "👋", AvatarPath: profileAvatarPath,
			},
			changed: true,
		},
		{
			name:   "unchanged",
			update: signal.ProfileUpdate{About: new("Hello")},
			want: signal.Profile{
				ACI: testAccount().ACI, GivenName: "Alice", FamilyName: profileFamilyName,
				About: "Hello", AboutEmoji: "👋", AvatarPath: profileAvatarPath,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := profileFake()
			result, err := open(t, fake).ProfileUpdate(t.Context(), test.update)

			want := signal.ProfileUpdateResult{
				Profile: test.want, Changed: test.changed, Accepted: test.changed, Verified: true,
			}
			if err != nil || result != want {
				t.Fatalf("ProfileUpdate = %+v, %v; want %+v", result, err, want)
			}

			if len(fake.ProfileUpdates()) != 1 || fake.Profiles[testAccount().ACI] != test.want {
				t.Fatalf("updates/state = %+v/%+v", fake.ProfileUpdates(), fake.Profiles)
			}
		})
	}
}

func TestProfileUpdateInvalidBeforeConnect(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		update signal.ProfileUpdate
	}{
		{name: "no fields"},
		{name: "invalid utf8", update: signal.ProfileUpdate{About: new("\xff")}},
		{name: "nul", update: signal.ProfileUpdate{GivenName: new("A\x00B")}},
		{name: "oversize", update: signal.ProfileUpdate{About: new(strings.Repeat("a", 513))}},
		{name: "combined name", update: signal.ProfileUpdate{
			GivenName: new(strings.Repeat("a", 257)), FamilyName: new("B"),
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := profileFake()
			fake.ConnectErr = errBoom

			result, err := open(t, fake).ProfileUpdate(t.Context(), test.update)
			if !errors.Is(err, signal.ErrInvalidProfileUpdate) || result != (signal.ProfileUpdateResult{}) {
				t.Fatalf("invalid update = %+v, %v", result, err)
			}

			if len(fake.Connects()) != 0 || len(fake.ProfileUpdates()) != 0 {
				t.Fatalf("invalid update connected or delegated: %v/%+v", fake.Connects(), fake.ProfileUpdates())
			}
		})
	}
}

func TestProfileUpdateAcceptedError(t *testing.T) {
	t.Parallel()

	for _, unverified := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified", true: "unverified"}[unverified], func(t *testing.T) {
			t.Parallel()

			fake := profileFake()
			fake.ProfileFollowUpErr = errBoom
			fake.ProfileVerificationFails = unverified

			result, err := open(t, fake).ProfileUpdate(t.Context(), signal.ProfileUpdate{About: new("Changed")})
			if !errors.Is(err, errBoom) || !result.Accepted || !result.Changed || result.Verified == unverified {
				t.Fatalf("accepted result/error = %+v/%v", result, err)
			}

			want := fake.Profiles[testAccount().ACI]
			if unverified {
				want = signal.Profile{ACI: testAccount().ACI}
			}

			if result.Profile != want || len(fake.ProfileUpdates()) != 1 {
				t.Fatalf("accepted profile/attempts = %+v/%+v", result.Profile, fake.ProfileUpdates())
			}

			assertAcceptedProfileError(t, err)
		})
	}
}

func TestProfileUseExistingConnection(t *testing.T) {
	t.Parallel()

	fake := profileFake()
	client := openProfileClient(t, fake, signal.Options{})

	err := client.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	use := app.New(client)

	_, err = use.ProfileShow(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	_, err = use.ProfileUpdate(t.Context(), signal.ProfileUpdate{AboutEmoji: new("")})
	if err != nil {
		t.Fatal(err)
	}

	if len(fake.Connects()) != 1 || len(fake.ProfileUpdates()) != 1 {
		t.Fatalf("existing connection/updates = %v/%+v", fake.Connects(), fake.ProfileUpdates())
	}
}

func TestProfileAccountSelection(t *testing.T) {
	t.Parallel()

	fake := profileFake()
	other := signal.Account{ACI: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Number: "+4915112345679", DeviceID: 2}
	fake.Linked = append(fake.Linked, other)
	fake.Profiles[other.ACI] = signal.Profile{ACI: other.ACI, GivenName: "Other", About: "Second"}
	first := fake.Profiles[testAccount().ACI]
	client := openProfileClient(t, fake, signal.Options{Account: other.Number})

	use := app.New(client)

	profile, err := use.ProfileShow(t.Context())
	if err != nil || profile.ACI != other.ACI || profile.GivenName != "Other" {
		t.Fatalf("selected profile = %+v, %v", profile, err)
	}

	result, err := use.ProfileUpdate(t.Context(), signal.ProfileUpdate{About: new("")})
	if err != nil || result.Profile.ACI != other.ACI || result.Profile.About != "" {
		t.Fatalf("selected update = %+v, %v", result, err)
	}

	calls := fake.ProfileUpdates()
	if len(calls) != 1 || calls[0].ACI != other.ACI || fake.Profiles[testAccount().ACI] != first {
		t.Fatalf("account selection changed wrong account: %+v/%+v", calls, fake.Profiles)
	}
}

func TestProfileErrors(t *testing.T) {
	t.Parallel()

	unknownWrite := fmt.Errorf("write outcome unknown; inspect profile show before retrying: %w", errBoom)
	for _, test := range []struct {
		name      string
		configure func(*signaltest.Fake)
		want      error
		attempts  int
	}{
		{name: "connection failure", configure: func(f *signaltest.Fake) { f.ConnectErr = errBoom }, want: errBoom},
		{name: "read", configure: func(f *signaltest.Fake) { f.OwnProfileErr = errBoom }, want: errBoom, attempts: 1},
		{
			name: "write", configure: func(f *signaltest.Fake) { f.UpdateProfileErr = unknownWrite },
			want: unknownWrite, attempts: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := profileFake()
			test.configure(fake)

			use := open(t, fake)
			if test.name != "write" {
				_, err := use.ProfileShow(t.Context())
				if !errors.Is(err, test.want) || !strings.HasPrefix(err.Error(), "profile show:") {
					t.Fatalf("show error = %v", err)
				}
			}

			result, err := use.ProfileUpdate(t.Context(), signal.ProfileUpdate{About: new("")})
			if !errors.Is(err, test.want) || result != (signal.ProfileUpdateResult{}) ||
				!strings.HasPrefix(err.Error(), "profile update:") || !strings.Contains(err.Error(), test.want.Error()) {
				t.Fatalf("update error = %+v, %v", result, err)
			}

			if len(fake.ProfileUpdates()) != test.attempts {
				t.Fatalf("update attempts = %+v; want %d", fake.ProfileUpdates(), test.attempts)
			}
		})
	}
}

func assertAcceptedProfileError(t *testing.T, err error) {
	t.Helper()

	for _, hint := range []string{"profile update:", "accepted", "profile show", "before retrying"} {
		if !strings.Contains(err.Error(), hint) {
			t.Fatalf("accepted error lacks command context or retry guidance %q: %v", hint, err)
		}
	}
}

func openProfileClient(t *testing.T, fake *signaltest.Fake, opts signal.Options) signal.Client {
	t.Helper()

	client, err := fake.Factory(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		closeErr := client.Close()
		if closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	return client
}
