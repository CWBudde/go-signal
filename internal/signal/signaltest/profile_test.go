//nolint:goconst // literal expectations are independent fixtures
package signaltest_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	profileAliceACI = "11111111-1111-4111-8111-111111111111"
	profileBobACI   = "22222222-2222-4222-8222-222222222222"
)

func TestProfileFakeAccountIsolation(t *testing.T) { //nolint:cyclop // assertions cover the complete operation result
	t.Parallel()

	fake := profileFake()

	cli := profileClient(t, fake, "+12025550102", true)

	profile, err := cli.OwnProfile(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if profile != (signal.Profile{ACI: profileBobACI, GivenName: "Bob", About: "bob about"}) {
		t.Fatalf("OwnProfile() = %+v", profile)
	}

	result, err := cli.UpdateOwnProfile(t.Context(), signal.ProfileUpdate{GivenName: new("Robert")})
	if err != nil {
		t.Fatal(err)
	}

	want := signal.Profile{ACI: profileBobACI, GivenName: "Robert", About: "bob about"}

	if result != (signal.ProfileUpdateResult{Profile: want, Changed: true, Accepted: true, Verified: true}) {
		t.Fatalf("result = %+v", result)
	}

	if fake.Profiles[profileBobACI] != want || fake.Profiles[profileAliceACI] != aliceProfile() {
		t.Fatalf("account profiles = %+v", fake.Profiles)
	}

	calls := fake.ProfileUpdates()

	if len(calls) != 1 ||
		calls[0].ACI != profileBobACI ||
		calls[0].Result != result ||
		calls[0].Update.GivenName == nil ||
		*calls[0].Update.GivenName != "Robert" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestProfileFakeNoOpAndFailures(t *testing.T) { //nolint:cyclop,funlen // table-driven operation cases
	t.Parallel()

	followUp := signal.ErrConnectionFailed

	tests := []struct {
		name      string
		configure func(*signaltest.Fake)
		update    signal.ProfileUpdate
		wantErr   error
		want      signal.ProfileUpdateResult
		changed   bool
	}{
		{
			name:   "no op",
			update: signal.ProfileUpdate{GivenName: new("Alice")},
			want: signal.ProfileUpdateResult{
				Profile:  aliceProfile(),
				Verified: true,
			},
		},
		{
			name: "no op skips configured mutation and follow up failures",
			configure: func(f *signaltest.Fake) {
				f.UpdateProfileErr = signal.ErrProfileRejected
				f.ProfileFollowUpErr = followUp
				f.ProfileVerificationFails = true
			},
			update: signal.ProfileUpdate{GivenName: new("Alice")},
			want: signal.ProfileUpdateResult{
				Profile:  aliceProfile(),
				Verified: true,
			},
		},
		{name: "invalid request", wantErr: signal.ErrInvalidProfileUpdate},
		{
			name:    "merged name overflow",
			update:  signal.ProfileUpdate{GivenName: new(strings.Repeat("a", 257))},
			wantErr: signal.ErrInvalidProfileUpdate,
		},
		{
			name:      "read failure",
			configure: func(f *signaltest.Fake) { f.OwnProfileErr = signal.ErrProfileKeyChanged },
			update:    signal.ProfileUpdate{About: new("")},
			wantErr:   signal.ErrProfileKeyChanged,
		},
		{
			name: "missing seed",
			configure: func(f *signaltest.Fake) {
				delete(f.Profiles,
					profileAliceACI)
			},
			update:  signal.ProfileUpdate{About: new("")},
			wantErr: signal.ErrInvalidProfile,
		},
		{
			name:      "rejected",
			configure: func(f *signaltest.Fake) { f.UpdateProfileErr = signal.ErrProfileRejected },
			update:    signal.ProfileUpdate{About: new("")},
			wantErr:   signal.ErrProfileRejected,
		},
		{
			name:      "verified follow up failure",
			configure: func(f *signaltest.Fake) { f.ProfileFollowUpErr = followUp },
			update:    signal.ProfileUpdate{About: new("")},
			wantErr:   followUp,
			want: signal.ProfileUpdateResult{
				Profile: signal.Profile{
					ACI:        profileAliceACI,
					GivenName:  "Alice",
					FamilyName: "Smith",
					AboutEmoji: "😀",
					AvatarPath: "/avatar",
				},
				Changed:  true,
				Accepted: true,
				Verified: true,
			},
			changed: true,
		},
		{
			name:      "verification failure",
			configure: func(f *signaltest.Fake) { f.ProfileVerificationFails = true },
			update:    signal.ProfileUpdate{About: new("")},
			wantErr:   signal.ErrProfileVerification,
			want: signal.ProfileUpdateResult{
				Profile:  signal.Profile{ACI: profileAliceACI},
				Changed:  true,
				Accepted: true,
			},
			changed: true,
		},
		{
			name:      "verification explicit cause",
			configure: func(f *signaltest.Fake) { f.ProfileVerificationFails = true; f.ProfileFollowUpErr = followUp },
			update:    signal.ProfileUpdate{About: new("")},
			wantErr:   followUp,
			want: signal.ProfileUpdateResult{
				Profile:  signal.Profile{ACI: profileAliceACI},
				Changed:  true,
				Accepted: true,
			},
			changed: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := profileFake()

			if test.configure != nil {
				test.configure(fake)
			}

			before, existed := fake.Profiles[profileAliceACI]

			cli := profileClient(t, fake, profileAliceACI, true)

			got, err := cli.UpdateOwnProfile(t.Context(), test.update)

			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v; want %v", err, test.wantErr)
			}

			if got != test.want {
				t.Fatalf("result = %+v; want %+v", got, test.want)
			}

			profile, exists := fake.Profiles[profileAliceACI]

			if test.changed {
				if profile != (signal.Profile{
					ACI:        profileAliceACI,
					GivenName:  "Alice",
					FamilyName: "Smith",
					AboutEmoji: "😀",
					AvatarPath: "/avatar",
				}) {
					t.Fatalf("accepted state = %+v", profile)
				}
			} else if profile != before || exists != existed {
				t.Fatalf("profile changed on failure/no op: %+v", profile)
			}

			calls := fake.ProfileUpdates()

			if len(calls) != 1 || calls[0].ACI != profileAliceACI || calls[0].Result != got {
				t.Fatalf("recorded calls = %+v", calls)
			}
		})
	}

	for _, injected := range []error{
		signal.ErrProfileKeyUnavailable,
		signal.ErrProfileKeyChanged,
		signal.ErrProfileV2Unsupported,
		signal.ErrInvalidProfile,
	} {
		fake := profileFake()
		fake.OwnProfileErr = injected

		cli := profileClient(t, fake, profileAliceACI, true)

		got, err := cli.OwnProfile(t.Context())

		if !errors.Is(err, injected) || got != (signal.Profile{}) {
			t.Fatalf("OwnProfile() = %+v, %v; want %v", got, err, injected)
		}
	}
}

func TestProfileFakeLifecycle(t *testing.T) { //nolint:cyclop,funlen // table-driven operation cases
	t.Parallel()

	tests := []struct {
		name      string
		connect   bool
		configure func(*signaltest.Fake)
		close     bool
		cancel    bool
		wantErr   error
	}{
		{name: "not connected", wantErr: signal.ErrNotConnected},
		{name: "closed", connect: true, close: true, wantErr: signal.ErrClosed},
		{name: "cancelled", connect: true, cancel: true, wantErr: context.Canceled},
		{name: "unlinked connection", connect: true, configure: func(f *signaltest.Fake) {
			f.Incoming = []signal.Event{&signal.Connection{State: signal.StateLoggedOut}}
		}, wantErr: signal.ErrDeviceUnlinked},
		{name: "connection lost", connect: true, configure: func(f *signaltest.Fake) {
			f.Incoming = []signal.Event{&signal.Connection{State: signal.StateFailed, Err: signal.ErrConnectionFailed}}
		}, wantErr: signal.ErrConnectionFailed},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := profileFake()

			if test.configure != nil {
				test.configure(fake)
			}

			cli := profileClient(t, fake, profileAliceACI, test.connect)

			if test.close {
				err := cli.Close()
				if err != nil {
					t.Fatal(err)
				}
			}

			ctx := t.Context()

			if test.cancel {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()

				ctx = cancelled
			}

			profile, err := cli.OwnProfile(ctx)

			if !errors.Is(err, test.wantErr) || profile != (signal.Profile{}) {
				t.Fatalf("OwnProfile() = %+v, %v; want %v", profile, err, test.wantErr)
			}

			result, err := cli.UpdateOwnProfile(ctx, signal.ProfileUpdate{About: new("")})

			if !errors.Is(err, test.wantErr) || result != (signal.ProfileUpdateResult{}) {
				t.Fatalf("UpdateOwnProfile() = %+v, %v; want %v", result, err, test.wantErr)
			}

			if fake.Profiles[profileAliceACI] != aliceProfile() || len(fake.ProfileUpdates()) != 1 {
				t.Fatalf("unexpected state/attempt count: %+v, %+v", fake.Profiles, fake.ProfileUpdates())
			}
		})
	}

	t.Run("account lock", func(t *testing.T) {
		t.Parallel()

		fake := profileFake()

		profileClient(t, fake, profileAliceACI, true)

		blocked := profileClient(t, fake, profileAliceACI, false)

		err := blocked.Connect(t.Context(), signal.SendOnly())

		if !errors.Is(err, signal.ErrAccountInUse) {
			t.Fatalf("Connect() = %v", err)
		}

		profileClient(t, fake, profileBobACI, true)
	})

	t.Run("already unlinked", func(t *testing.T) {
		t.Parallel()

		fake := profileFake()
		fake.Linked[0].UnlinkedAt = time.Now()

		cli := profileClient(t, fake, profileAliceACI, false)

		err := cli.Connect(t.Context(), signal.SendOnly())

		if !errors.Is(err, signal.ErrDeviceUnlinked) {
			t.Fatalf("Connect() = %v", err)
		}
	})
}

func TestProfileFakeCallCopies(t *testing.T) { //nolint:cyclop // assertions cover the complete operation result
	t.Parallel()

	fake := profileFake()

	cli := profileClient(t, fake, profileAliceACI, true)

	given, family, about, emoji := "Mary Jane", "Jones", "updated", "🙂"

	_, err := cli.UpdateOwnProfile(t.Context(),
		signal.ProfileUpdate{
			GivenName:  &given,
			FamilyName: &family,
			About:      &about,
			AboutEmoji: &emoji,
		})
	if err != nil {
		t.Fatal(err)
	}

	given, family, about, emoji = "mutated", "mutated", "mutated", "mutated"

	calls := fake.ProfileUpdates()

	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}

	call := calls[0]

	if *call.Update.GivenName != "Mary Jane" ||
		*call.Update.FamilyName != "Jones" ||
		*call.Update.About != "updated" ||
		*call.Update.AboutEmoji != "🙂" {
		t.Fatalf("caller mutation changed recorded request: %+v", call.Update)
	}

	*call.Update.GivenName = "snapshot"
	*call.Update.FamilyName = "snapshot"
	*call.Update.About = "snapshot"
	*call.Update.AboutEmoji = "snapshot"
	calls[0].ACI, calls[0].Result.Profile.GivenName = "snapshot", "snapshot"

	next := fake.ProfileUpdates()[0]

	if next.ACI != profileAliceACI ||
		next.Result.Profile.GivenName != "Mary Jane" ||
		*next.Update.GivenName != "Mary Jane" ||
		*next.Update.FamilyName != "Jones" ||
		*next.Update.About != "updated" ||
		*next.Update.AboutEmoji != "🙂" {
		t.Fatalf("snapshot mutation changed recorded request: %+v", next)
	}

	if fake.Profiles[profileAliceACI].GivenName != "Mary Jane" {
		t.Fatal("snapshot mutation changed stored profile")
	}
}

func profileFake() *signaltest.Fake {
	return &signaltest.Fake{
		Linked: []signal.Account{
			{
				ACI:    profileAliceACI,
				Number: "+12025550101",
			},
			{
				ACI:    profileBobACI,
				Number: "+12025550102",
			},
		},
		Profiles: map[string]signal.Profile{
			profileAliceACI: aliceProfile(),
			profileBobACI: {
				ACI:       profileBobACI,
				GivenName: "Bob",
				About:     "bob about",
			},
		},
	}
}

func aliceProfile() signal.Profile {
	return signal.Profile{
		ACI:        profileAliceACI,
		GivenName:  "Alice",
		FamilyName: "Smith",
		About:      "original about",
		AboutEmoji: "😀",
		AvatarPath: "/avatar",
	}
}

func profileClient(t *testing.T, fake *signaltest.Fake, account string, connect bool) signal.Client {
	t.Helper()

	cli, err := fake.Factory(t.Context(), signal.Options{Account: account})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		err := cli.Close()
		if err != nil {
			t.Error(err)
		}
	})

	if connect {
		err = cli.Connect(t.Context(), signal.SendOnly())
		if err != nil {
			t.Fatal(err)
		}
	}

	return cli
}
