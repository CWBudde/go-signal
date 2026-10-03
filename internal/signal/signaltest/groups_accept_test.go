//nolint:lll,cyclop // Account, alias and partial-outcome behavioral matrices.
package signaltest_test

import (
	"context"
	"errors"
	"io"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const acceptBobPNI = "33333333-3333-4333-8333-333333333333"

func acceptFake(t *testing.T, pni bool) *signaltest.Fake {
	t.Helper()

	fake, _ := joinFake(t, signal.GroupLinkDisabled)

	fake.Linked[1].PNI = acceptBobPNI

	group := fake.GroupJoinServer[fakeLinkKey]

	invited := signal.Recipient{ACI: profileBobACI}

	if pni {
		invited = signal.Recipient{PNI: acceptBobPNI}
	}

	group.Pending = []signal.PendingMember{{Recipient: invited, Role: signal.GroupRoleAdmin, AddedBy: signal.Recipient{ACI: profileAliceACI}}, {Recipient: signal.Recipient{ACI: "44444444-4444-4444-8444-444444444444"}, Role: signal.GroupRoleMember}}

	group.Title = "Acceptance group"

	group.Description = "preserved"

	group.MembersCanEditAttributes = true

	fake.GroupJoinServer[fakeLinkKey] = group

	fake.GroupJoinKnownKeys = map[string]map[string]string{profileBobACI: {fakeLinkKey: roleFakeGroupID}}

	fake.GroupJoinTitleCache = map[string]map[string]signal.CachedGroup{profileBobACI: {roleFakeGroupID: {Title: "old title", LeftAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}}

	return fake
}

func TestFakeGroupAccept(t *testing.T) {
	t.Parallel()

	for _, pni := range []bool{false, true} {
		name := "ACI"

		if pni {
			name = "PNI"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fake := acceptFake(t, pni)

			original := fake.GroupJoinServer[fakeLinkKey]

			cli := profileClient(t, fake, profileBobACI, true)

			got, err := cli.AcceptGroupInvitation(t.Context(), roleFakeGroupID)

			if err != nil || !got.Accepted || !got.Changed || !got.Verified || got.Title != "Acceptance group" || got.Revision != 8 {
				t.Fatal(got, err)
			}

			group := fake.GroupJoinServer[fakeLinkKey]

			_, role := group.MembershipOf(profileBobACI)

			want := signal.GroupRoleAdmin

			if pni {
				want = signal.GroupRoleMember
			}

			if role != want || len(group.Pending) != 1 || group.Description != "preserved" || !group.MembersCanEditAttributes || len(group.Members) != 2 {
				t.Fatal(group)
			}

			if original.Revision != 7 || original.Members[0].Role != signal.GroupRoleAdmin || len(original.Pending) != 2 || original.Pending[0].Recipient != fakeRecipient(pni) {
				t.Fatal("mutated input snapshot", original)
			}

			if !fake.GroupJoinTitleCache[profileBobACI][roleFakeGroupID].LeftAt.IsZero() {
				t.Fatal("left marker remained")
			}

			fake.AcceptGroupInvitationErr = io.ErrClosedPipe

			again, err := cli.AcceptGroupInvitation(t.Context(), roleFakeGroupID)

			if err != nil || !again.Verified || again.Accepted || again.Changed || again.Revision != 8 {
				t.Fatal(again, err)
			}
		})
	}
}

func fakeRecipient(pni bool) signal.Recipient {
	if pni {
		return signal.Recipient{PNI: acceptBobPNI}
	}

	return signal.Recipient{ACI: profileBobACI}
}

func TestFakeGroupAcceptTypedAccountIsolation(t *testing.T) {
	t.Parallel()

	fake := acceptFake(t, true)

	fake.Linked = append(fake.Linked, signal.Account{ACI: acceptBobPNI, PNI: "55555555-5555-4555-8555-555555555555", Number: "+12025550103", DeviceID: 2})

	fake.GroupJoinKnownKeys[acceptBobPNI] = map[string]string{fakeLinkKey: roleFakeGroupID}

	other := profileClient(t, fake, acceptBobPNI, true)

	_, err := other.AcceptGroupInvitation(t.Context(), roleFakeGroupID)

	if !errors.Is(err, signal.ErrGroupInvitationNotFound) || fake.GroupJoinServer[fakeLinkKey].Revision != 7 {
		t.Fatal(err)
	}

	err = other.Close()
	if err != nil {
		t.Fatal(err)
	}

	bob := profileClient(t, fake, profileBobACI, true)

	got, err := bob.AcceptGroupInvitation(t.Context(), roleFakeGroupID)

	if err != nil || !got.Changed {
		t.Fatal(got, err)
	}

	err = bob.Close()
	if err != nil {
		t.Fatal(err)
	}

	other = profileClient(t, fake, acceptBobPNI, true)

	titles, err := other.GroupTitles(t.Context())

	if err != nil || len(titles) != 0 {
		t.Fatal("other gained title", titles, err)
	}

	_, err = other.AcceptGroupInvitation(t.Context(), roleFakeGroupID)

	if !errors.Is(err, signal.ErrGroupInvitationNotFound) {
		t.Fatal(err)
	}
}

func TestFakeGroupAcceptKnownKeyAliases(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{
		roleFakeGroupID, fakeLinkKey, "legacy-alias", "  " + fakeLinkKey + "  ",
		"group:" + fakeLinkKey, strings.TrimSuffix(fakeLinkKey, "="),
	} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()

			fake := acceptFake(t, false)

			fake.GroupKeys = map[string]string{fakeLinkKey: roleFakeGroupID, "legacy-alias": roleFakeGroupID}

			fake.GroupInfo = map[string]signal.Group{roleFakeGroupID: fake.GroupJoinServer[fakeLinkKey]}

			fake.GroupTitleCache = map[string]signal.CachedGroup{roleFakeGroupID: {Title: "acceptance global decoy"}}

			alice := profileClient(t, fake, profileAliceACI, true)

			got, err := alice.AcceptGroupInvitation(t.Context(), ref)

			if !errors.Is(err, signal.ErrUnknownGroup) || got.ID != "" || fake.GroupJoinServer[fakeLinkKey].Revision != 7 {
				t.Fatal("unretained alias exposed group", got, err)
			}

			err = alice.Close()
			if err != nil {
				t.Fatal(err)
			}

			bob := profileClient(t, fake, profileBobACI, true)

			got, err = bob.AcceptGroupInvitation(t.Context(), ref)

			if err != nil || !got.Changed || got.Title == "acceptance global decoy" {
				t.Fatal(got, err)
			}
		})
	}
}

//nolint:funlen // Independent definite, uncertain and accepted-failure fixtures.
func TestFakeGroupAcceptFailures(t *testing.T) {
	t.Parallel()

	secret := &url.Error{Op: "PATCH", URL: "https://secret/master-key", Err: io.ErrClosedPipe}

	tests := []struct {
		name     string
		setup    func(*signaltest.Fake)
		cause    error
		accepted bool
	}{
		{"acceptance conflict", func(f *signaltest.Fake) { f.AcceptGroupInvitationErr = errors.Join(signal.ErrGroupChanged, secret) }, signal.ErrGroupChanged, false},
		{"acceptance uncertainty", func(f *signaltest.Fake) {
			f.AcceptGroupInvitationErr = errors.Join(signal.ErrGroupUpdateUncertain, secret)
		}, signal.ErrGroupUpdateUncertain, false},
		{"accepted failure", func(f *signaltest.Fake) { f.GroupAcceptFollowUpErr = secret }, io.ErrClosedPipe, true},
		{"no invitation", func(f *signaltest.Fake) {
			group := f.GroupJoinServer[fakeLinkKey]

			group.Pending = nil

			f.GroupJoinServer[fakeLinkKey] = group
		}, signal.ErrGroupInvitationNotFound, false},
		{"unknown", func(f *signaltest.Fake) { f.GroupJoinKnownKeys = nil }, signal.ErrUnknownGroup, false},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			fake := acceptFake(t, false)

			testCase.setup(fake)

			before := fake.GroupJoinServer[fakeLinkKey]

			cache := fake.GroupJoinTitleCache[profileBobACI][roleFakeGroupID]

			cli := profileClient(t, fake, profileBobACI, true)

			got, err := cli.AcceptGroupInvitation(t.Context(), roleFakeGroupID)

			if !errors.Is(err, testCase.cause) || got.Accepted != testCase.accepted || got.Changed != testCase.accepted || got.Verified || strings.Contains(err.Error(), "secret") {
				t.Fatal(got, err)
			}

			after := fake.GroupJoinServer[fakeLinkKey]

			if !testCase.accepted && !reflect.DeepEqual(before, after) {
				t.Fatal("failed operation mutated fixtures")
			}

			if testCase.accepted && (after.Revision != 8 || len(after.Pending) != 1 || len(after.Members) != 2) {
				t.Fatal("accepted state lost")
			}

			if fake.GroupJoinTitleCache[profileBobACI][roleFakeGroupID] != cache {
				t.Fatal("unverified write changed cache")
			}

			if testCase.name == "acceptance conflict" || testCase.name == "acceptance uncertainty" {
				if got.Revision != 8 {
					t.Fatal("lost attempt", got)
				}
			}
		})
	}
}

func TestGroupAcceptRepeatAfterAcceptedFailure(t *testing.T) {
	t.Parallel()

	fake := acceptFake(t, true)

	fake.GroupAcceptFollowUpErr = io.ErrClosedPipe

	bob := profileClient(t, fake, profileBobACI, true)

	got, err := bob.AcceptGroupInvitation(t.Context(), roleFakeGroupID)

	if !errors.Is(err, io.ErrClosedPipe) || !got.Accepted || got.Verified {
		t.Fatal(got, err)
	}

	err = bob.Close()
	if err != nil {
		t.Fatal(err)
	}

	fake.AcceptGroupInvitationErr = signal.ErrGroupChanged

	bob = profileClient(t, fake, profileBobACI, true)

	again, err := bob.AcceptGroupInvitation(t.Context(), roleFakeGroupID)

	if err != nil || !again.Verified || again.Accepted || again.Changed || again.Revision != 8 || !fake.GroupJoinTitleCache[profileBobACI][roleFakeGroupID].LeftAt.IsZero() {
		t.Fatal(again, err)
	}
}

func TestFakeGroupAcceptLegacy(t *testing.T) {
	t.Parallel()

	fake := acceptFake(t, false)

	original := fake.GroupJoinServer[fakeLinkKey]

	fake.GroupJoinServer = nil

	fake.GroupJoinKnownKeys = nil

	fake.GroupInfo = map[string]signal.Group{roleFakeGroupID: original}

	fake.GroupKeys = map[string]string{fakeLinkKey: roleFakeGroupID}

	bob := profileClient(t, fake, profileBobACI, true)

	got, err := bob.AcceptGroupInvitation(t.Context(), fakeLinkKey)

	if err != nil || !got.Verified || got.Revision != 8 {
		t.Fatal(got, err)
	}

	if fake.GroupInfo[roleFakeGroupID].Revision != 7 || original.Pending[0].Recipient.ACI != profileBobACI {
		t.Fatal("legacy input mutated")
	}

	err = bob.Close()
	if err != nil {
		t.Fatal(err)
	}

	alice := profileClient(t, fake, profileAliceACI, true)

	_, err = alice.AcceptGroupInvitation(t.Context(), roleFakeGroupID)

	if !errors.Is(err, signal.ErrUnknownGroup) {
		t.Fatal("legacy alias exposed introduced server snapshot", err)
	}
}

func TestFakeGroupAcceptPriority(t *testing.T) {
	t.Parallel()

	fake := acceptFake(t, true)

	group := fake.GroupJoinServer[fakeLinkKey]

	group.Pending = append(group.Pending, signal.PendingMember{Recipient: signal.Recipient{ACI: profileBobACI}, Role: signal.GroupRoleAdmin})

	fake.GroupJoinServer[fakeLinkKey] = group

	bob := profileClient(t, fake, profileBobACI, true)

	_, err := bob.AcceptGroupInvitation(t.Context(), roleFakeGroupID)
	if err != nil {
		t.Fatal(err)
	}

	group = fake.GroupJoinServer[fakeLinkKey]

	_, role := group.MembershipOf(profileBobACI)

	if role != signal.GroupRoleAdmin || len(group.Pending) != 2 || group.Pending[0].Recipient.PNI != acceptBobPNI {
		t.Fatal("ACI priority lost", group)
	}
}

func TestFakeGroupAcceptLifecycle(t *testing.T) {
	t.Parallel()

	fake := acceptFake(t, false)

	cli := profileClient(t, fake, profileBobACI, false)

	_, err := cli.AcceptGroupInvitation(t.Context(), roleFakeGroupID)

	if !errors.Is(err, signal.ErrNotConnected) {
		t.Fatal(err)
	}

	err = cli.Connect(t.Context(), signal.SendOnly())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())

	cancel()

	_, err = cli.AcceptGroupInvitation(ctx, roleFakeGroupID)

	if !errors.Is(err, context.Canceled) || fake.GroupJoinServer[fakeLinkKey].Revision != 7 {
		t.Fatal(err)
	}

	err = cli.Close()
	if err != nil {
		t.Fatal(err)
	}

	_, err = cli.AcceptGroupInvitation(t.Context(), roleFakeGroupID)

	if !errors.Is(err, signal.ErrClosed) {
		t.Fatal(err)
	}

	for _, evt := range []*signal.Connection{{State: signal.StateLoggedOut}, {State: signal.StateFailed, Err: signal.ErrConnectionFailed}} {
		fake = acceptFake(t, false)

		fake.Incoming = []signal.Event{evt}

		cli = profileClient(t, fake, profileBobACI, true)

		_, err = cli.AcceptGroupInvitation(t.Context(), roleFakeGroupID)

		want := signal.ErrDeviceUnlinked

		if evt.State == signal.StateFailed {
			want = signal.ErrConnectionFailed
		}

		if !errors.Is(err, want) {
			t.Fatal(err)
		}
	}
}
