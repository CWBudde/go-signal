//nolint:cyclop,gocognit,lll // Behavioral matrices keep independent fixtures and assertions together.
package signaltest_test

import (
	"context"
	"errors"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func joinFake(t *testing.T, state signal.GroupLinkState) (*signaltest.Fake, string) {
	t.Helper()

	group := signal.Group{ID: roleFakeGroupID, MasterKey: fakeLinkKey, Revision: 7, Title: "Join group", Members: []signal.GroupMember{{Recipient: signal.Recipient{ACI: profileAliceACI}, Role: signal.GroupRoleAdmin}}}

	link, err := group.Link(profileAliceACI, signal.GroupLinkEnabled, fakeLinkPassword)
	if err != nil {
		t.Fatal(err)
	}

	return &signaltest.Fake{
		Linked:             []signal.Account{{ACI: profileAliceACI, Number: "+12025550101", DeviceID: 2}, {ACI: profileBobACI, Number: "+12025550102", DeviceID: 2}},
		GroupJoinServer:    map[string]signal.Group{fakeLinkKey: group},
		GroupLinkStates:    map[string]signal.GroupLinkState{roleFakeGroupID: state},
		GroupLinkPasswords: map[string]string{roleFakeGroupID: fakeLinkPassword},
	}, link.URL
}

func TestFakeGroupJoin(t *testing.T) {
	t.Parallel()

	for _, state := range []signal.GroupLinkState{signal.GroupLinkEnabled, signal.GroupLinkApproval} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			fake, link := joinFake(t, state)

			cli := profileClient(t, fake, profileBobACI, true)

			left := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

			fake.GroupTitleCache = map[string]signal.CachedGroup{roleFakeGroupID: {Title: "previous join title", LeftAt: left}}

			got, err := cli.JoinGroup(t.Context(), link)

			want := signal.GroupJoinMember

			if state == signal.GroupLinkApproval {
				want = signal.GroupJoinRequesting
			}

			if err != nil || got.Status != want || !got.Accepted || !got.Changed || !got.Verified || got.Revision != 8 {
				t.Fatalf("result %+v,%v", got, err)
			}

			if fake.GroupJoinKnownKeys[profileBobACI][fakeLinkKey] != roleFakeGroupID {
				t.Fatal("key not retained")
			}

			if state == signal.GroupLinkApproval && fake.GroupTitleCache[roleFakeGroupID].LeftAt != left {
				t.Fatal("pending erased left marker")
			}

			if state == signal.GroupLinkEnabled && !fake.GroupTitleCache[roleFakeGroupID].LeftAt.IsZero() {
				t.Fatal("member retained left marker")
			}

			fake.JoinGroupErr = io.ErrClosedPipe

			again, err := cli.JoinGroup(t.Context(), link)
			if err != nil || again.Status != want || !again.Verified || again.Accepted || again.Changed || again.Revision != 8 {
				t.Fatalf("noop %+v,%v", again, err)
			}

			shown, err := cli.Group(t.Context(), roleFakeGroupID)

			if state == signal.GroupLinkEnabled {
				if err != nil || shown.Membership != signal.MembershipMember {
					t.Fatalf("show %+v,%v", shown, err)
				}
			} else if !errors.Is(err, signal.ErrNotAMember) {
				t.Fatal(err)
			}
		})
	}
}

func TestGroupJoinAccountIsolation(t *testing.T) {
	t.Parallel()

	fake, link := joinFake(t, signal.GroupLinkEnabled)

	bob := profileClient(t, fake, profileBobACI, true)
	// A retained key never establishes membership.

	fake.GroupJoinKnownKeys = map[string]map[string]string{profileBobACI: {fakeLinkKey: roleFakeGroupID}}

	got, err := bob.JoinGroup(t.Context(), link)
	if err != nil || !got.Changed {
		t.Fatalf("retained key implied membership %+v,%v", got, err)
	}

	if len(fake.GroupJoinServer[fakeLinkKey].Members) != 2 {
		t.Fatal("wrong self action")
	}

	err = bob.Close()
	if err != nil {
		t.Fatal(err)
	}

	alice := profileClient(t, fake, profileAliceACI, true)

	titles, titleErr := alice.GroupTitles(t.Context())
	if titleErr != nil || len(titles) != 0 {
		t.Fatal("other account learned join title", titles, titleErr)
	}

	_, err = alice.Group(t.Context(), roleFakeGroupID)
	if !errors.Is(err, signal.ErrUnknownGroup) {
		t.Fatal("other account learned key", err)
	}

	// Establish Alice's existing membership independently; disabled links do not prevent no-ops.
	fake.GroupJoinKnownKeys[profileAliceACI] = map[string]string{fakeLinkKey: roleFakeGroupID}
	fake.GroupLinkStates[roleFakeGroupID] = signal.GroupLinkDisabled

	got, err = alice.JoinGroup(t.Context(), link)
	if err != nil || !got.Verified || got.Changed || got.Status != signal.GroupJoinMember {
		t.Fatalf("member %+v,%v", got, err)
	}
}

//nolint:funlen // Definite, uncertain and accepted fake failure scenarios.
func TestFakeGroupJoinFailures(t *testing.T) {
	t.Parallel()

	secretErr := &url.Error{Op: "PATCH", URL: "https://secret/password", Err: io.ErrClosedPipe}

	cases := []struct {
		name               string
		setup              func(*signaltest.Fake)
		identity           error
		accepted, retained bool
	}{
		{"disabled", func(fixture *signaltest.Fake) { fixture.GroupLinkStates[roleFakeGroupID] = signal.GroupLinkDisabled }, signal.ErrGroupLinkInactive, false, false},
		{"reset", func(fixture *signaltest.Fake) { fixture.GroupLinkPasswords[roleFakeGroupID] = "reset invite" }, signal.ErrGroupLinkInactive, false, false},
		{"banned", func(fixture *signaltest.Fake) {
			g := fixture.GroupJoinServer[fakeLinkKey]

			g.Banned = []signal.BannedMember{{Recipient: signal.Recipient{ACI: profileBobACI}}}
			fixture.GroupJoinServer[fakeLinkKey] = g
		}, signal.ErrGroupLinkInactive, false, false},
		{"invited", func(fixture *signaltest.Fake) {
			g := fixture.GroupJoinServer[fakeLinkKey]

			g.Pending = []signal.PendingMember{{Recipient: signal.Recipient{ACI: profileBobACI}}}
			fixture.GroupJoinServer[fakeLinkKey] = g

			fixture.GroupJoinKnownKeys = map[string]map[string]string{profileBobACI: {fakeLinkKey: roleFakeGroupID}}
		}, signal.ErrGroupInvitationRequiresAcceptance, false, false},
		{"conflict", func(fixture *signaltest.Fake) { fixture.JoinGroupErr = signal.ErrGroupChanged }, signal.ErrGroupChanged, false, true},
		{"uncertain", func(fixture *signaltest.Fake) {
			fixture.JoinGroupErr = errors.Join(signal.ErrGroupUpdateUncertain, secretErr)
		}, signal.ErrGroupUpdateUncertain, false, true},
		{"followup", func(fixture *signaltest.Fake) { fixture.GroupJoinFollowUpErr = secretErr }, io.ErrClosedPipe, true, true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			fake, link := joinFake(t, signal.GroupLinkEnabled)
			testCase.setup(fake)

			cli := profileClient(t, fake, profileBobACI, true)

			before := fake.GroupJoinServer[fakeLinkKey].Revision

			got, err := cli.JoinGroup(t.Context(), link)
			if !errors.Is(err, testCase.identity) || got.Accepted != testCase.accepted || got.Changed != testCase.accepted || got.Status != "" || got.Title != "" || got.Verified {
				t.Fatalf("result %+v,%v", got, err)
			}

			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "password") {
				t.Fatal("leaked error")
			}

			if testCase.retained && fake.GroupJoinKnownKeys[profileBobACI][fakeLinkKey] != roleFakeGroupID {
				t.Fatal("key lost")
			}

			if !testCase.accepted && fake.GroupJoinServer[fakeLinkKey].Revision != before {
				t.Fatal("rejection changed server")
			}

			if testCase.name == "conflict" && got.Revision != 8 {
				t.Fatal("definite submission lost attempted revision", got)
			}

			if testCase.name == "uncertain" && (got.Revision != 8 || !strings.Contains(err.Error(), "attempted revision 8")) {
				t.Fatal(got, err)
			}
		})
	}
}

func TestFakeGroupJoinLifecycle(t *testing.T) {
	t.Parallel()

	fake, link := joinFake(t, signal.GroupLinkEnabled)

	cli := profileClient(t, fake, profileBobACI, false)

	_, err := cli.JoinGroup(t.Context(), link)
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Fatal(err)
	}

	err = cli.Connect(t.Context(), signal.SendOnly())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = cli.JoinGroup(ctx, link)
	if !errors.Is(err, context.Canceled) || fake.GroupJoinServer[fakeLinkKey].Revision != 7 {
		t.Fatal(err)
	}

	err = cli.Close()
	if err != nil {
		t.Fatal(err)
	}

	_, err = cli.JoinGroup(t.Context(), link)
	if !errors.Is(err, signal.ErrClosed) {
		t.Fatal(err)
	}

	for _, evt := range []*signal.Connection{{State: signal.StateFailed, Err: signal.ErrConnectionFailed}, {State: signal.StateLoggedOut}} {
		fake, link = joinFake(t, signal.GroupLinkEnabled)

		fake.Incoming = []signal.Event{evt}

		cli = profileClient(t, fake, profileBobACI, true)

		_, err = cli.JoinGroup(t.Context(), link)

		want := signal.ErrConnectionFailed

		if evt.State == signal.StateLoggedOut {
			want = signal.ErrDeviceUnlinked
		}

		if !errors.Is(err, want) {
			t.Fatal(err)
		}
	}
}

func TestFakeGroupJoinFreshPendingPassword(t *testing.T) {
	t.Parallel()

	fake, link := joinFake(t, signal.GroupLinkApproval)

	group := fake.GroupJoinServer[fakeLinkKey]

	group.Requesting = []signal.RequestingMember{{Recipient: signal.Recipient{ACI: profileBobACI}}}
	fake.GroupJoinServer[fakeLinkKey] = group
	fake.GroupLinkPasswords[roleFakeGroupID] = "reset invite"

	cli := profileClient(t, fake, profileBobACI, true)

	_, err := cli.JoinGroup(t.Context(), link)
	if !errors.Is(err, signal.ErrGroupLinkInactive) {
		t.Fatal(err)
	}

	if len(fake.GroupJoinKnownKeys[profileBobACI]) != 0 {
		t.Fatal("failed preview retained key")
	}
}

func TestFakeGroupJoinRetainedInspection(t *testing.T) {
	t.Parallel()

	fake, link := joinFake(t, signal.GroupLinkApproval)

	bob := profileClient(t, fake, profileBobACI, true)

	_, err := bob.JoinGroup(t.Context(), link)
	if err != nil {
		t.Fatal(err)
	}

	fake.GroupErrs = map[string]error{roleFakeGroupID: signal.ErrNotAMember}

	groups, err := bob.Groups(t.Context())
	if err != nil || len(groups) != 1 || !errors.Is(groups[0].Err, signal.ErrNotAMember) {
		t.Fatalf("pending list %+v,%v", groups, err)
	}

	err = bob.Close()
	if err != nil {
		t.Fatal(err)
	}

	alice := profileClient(t, fake, profileAliceACI, true)

	groups, err = alice.Groups(t.Context())
	if err != nil || len(groups) != 0 {
		t.Fatalf("other account list %+v,%v", groups, err)
	}

	err = alice.Close()
	if err != nil {
		t.Fatal(err)
	}

	fake, link = joinFake(t, signal.GroupLinkEnabled)

	fake.JoinGroupErr = signal.ErrGroupUpdateUncertain

	bob = profileClient(t, fake, profileBobACI, true)

	_, err = bob.JoinGroup(t.Context(), link)
	if !errors.Is(err, signal.ErrGroupUpdateUncertain) {
		t.Fatal(err)
	}

	_, err = bob.Group(t.Context(), roleFakeGroupID)
	if !errors.Is(err, signal.ErrNotAMember) {
		t.Fatal("retained key made full state accessible", err)
	}
}
