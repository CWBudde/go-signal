//nolint:lll,cyclop // Behavioral fixture matrices cover ownership and partial outcomes.
package signaltest_test

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func cancelRequestFake(t *testing.T) *signaltest.Fake {
	t.Helper()
	fake := acceptFake(t, true)
	group := fake.GroupJoinServer[fakeLinkKey]
	group.Requesting = []signal.RequestingMember{
		{Recipient: signal.Recipient{ACI: profileBobACI}},
		{Recipient: signal.Recipient{PNI: profileBobACI}},
		{Recipient: signal.Recipient{ACI: acceptBobPNI}},
	}
	fake.GroupJoinServer[fakeLinkKey] = group

	return fake
}

func TestFakeGroupCancelRequest(t *testing.T) {
	t.Parallel()
	fake := cancelRequestFake(t)
	before := fake.GroupJoinServer[fakeLinkKey]
	cache := fake.GroupJoinTitleCache[profileBobACI][roleFakeGroupID]
	known := fake.GroupJoinKnownKeys[profileBobACI][fakeLinkKey]
	cli := profileClient(t, fake, profileBobACI, true)

	got, err := cli.CancelGroupJoinRequest(t.Context(), roleFakeGroupID)
	if err != nil || !got.Accepted || !got.Changed || !got.Verified || got.Revision != 8 || got.Title != before.Title {
		t.Fatal(got, err)
	}

	after := fake.GroupJoinServer[fakeLinkKey]
	if len(after.Requesting) != 2 || after.Requesting[0].Recipient.PNI != profileBobACI || after.Requesting[1].Recipient.ACI != acceptBobPNI || after.Revision != 8 {
		t.Fatal("removed wrong identity", after)
	}

	if !reflect.DeepEqual(before.Members, after.Members) || !reflect.DeepEqual(before.Pending, after.Pending) || before.Title != after.Title || before.LeftAt != after.LeftAt || fake.GroupJoinTitleCache[profileBobACI][roleFakeGroupID] != cache || fake.GroupJoinKnownKeys[profileBobACI][fakeLinkKey] != known {
		t.Fatal("mutated unrelated state")
	}

	if len(before.Requesting) != 3 || before.Requesting[0].Recipient.ACI != profileBobACI {
		t.Fatal("mutated input snapshot")
	}

	fake.CancelGroupJoinRequestErr = io.ErrClosedPipe

	got, err = cli.CancelGroupJoinRequest(t.Context(), roleFakeGroupID)
	if err != nil || got.Accepted || got.Changed || !got.Verified || got.Revision != 8 {
		t.Fatal("fresh no-op failed", got, err)
	}
}

func TestFakeGroupCancelRequestAccountIsolation(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{roleFakeGroupID, fakeLinkKey, "cancellation-alias", "group:" + fakeLinkKey, " " + strings.TrimSuffix(fakeLinkKey, "=") + " "} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()
			fake := cancelRequestFake(t)
			fake.GroupInfo = map[string]signal.Group{roleFakeGroupID: fake.GroupJoinServer[fakeLinkKey]}
			fake.GroupKeys = map[string]string{fakeLinkKey: roleFakeGroupID, "cancellation-alias": roleFakeGroupID}
			fake.GroupTitleCache = map[string]signal.CachedGroup{roleFakeGroupID: {Title: "cancellation global decoy"}}
			alice := profileClient(t, fake, profileAliceACI, true)

			got, err := alice.CancelGroupJoinRequest(t.Context(), ref)
			if !errors.Is(err, signal.ErrUnknownGroup) || got != (signal.GroupCancelRequestResult{}) || fake.GroupJoinServer[fakeLinkKey].Revision != 7 {
				t.Fatal("unknown account leaked state", got, err)
			}

			err = alice.Close()
			if err != nil {
				t.Fatal(err)
			}

			bob := profileClient(t, fake, profileBobACI, true)

			got, err = bob.CancelGroupJoinRequest(t.Context(), ref)
			if err != nil || !got.Changed || got.Title == "cancellation global decoy" {
				t.Fatal(got, err)
			}
		})
	}
}

func TestFakeGroupCancelRequestRepeatAfterAcceptedFailure(t *testing.T) {
	t.Parallel()
	fake := cancelRequestFake(t)
	fake.GroupCancelRequestVerificationErr = io.ErrClosedPipe
	cli := profileClient(t, fake, profileBobACI, true)

	got, err := cli.CancelGroupJoinRequest(t.Context(), roleFakeGroupID)
	if !errors.Is(err, io.ErrClosedPipe) || !got.Accepted || !got.Changed || got.Verified || got.Revision != 8 {
		t.Fatal(got, err)
	}

	err = cli.Close()
	if err != nil {
		t.Fatal(err)
	}

	fake.CancelGroupJoinRequestErr = signal.ErrGroupChanged
	cli = profileClient(t, fake, profileBobACI, true)

	got, err = cli.CancelGroupJoinRequest(t.Context(), roleFakeGroupID)
	if err != nil || got.Accepted || got.Changed || !got.Verified || got.Revision != 8 {
		t.Fatal("accepted server state was lost", got, err)
	}

	fake.GroupErrs = map[string]error{roleFakeGroupID: signal.ErrNotAMember}

	got, err = cli.CancelGroupJoinRequest(t.Context(), roleFakeGroupID)
	if !errors.Is(err, signal.ErrNotAMember) || got.Verified || got.Accepted {
		t.Fatal("refusal became no-op", got, err)
	}
}

func TestFakeGroupCancelRequestPartialResults(t *testing.T) {
	t.Parallel()

	secret := &url.Error{Op: http.MethodPatch, URL: "https://private-secret/key", Err: io.ErrClosedPipe}

	tests := []struct {
		name     string
		setup    func(*signaltest.Fake)
		cause    error
		accepted bool
		revision uint32
	}{
		{"preview auth", func(fake *signaltest.Fake) { fake.GroupErrs = map[string]error{roleFakeGroupID: signal.ErrNotAMember} }, signal.ErrNotAMember, false, 0},
		{"unknown cancellation group", func(fake *signaltest.Fake) { fake.GroupJoinKnownKeys = nil }, signal.ErrUnknownGroup, false, 0},
		{"key ID mismatch", func(fake *signaltest.Fake) { fake.GroupJoinKnownKeys[profileBobACI][fakeLinkKey] = "another-groupID" }, signal.ErrUnknownGroup, false, 0},
		{"cancellation conflict", func(fake *signaltest.Fake) {
			fake.CancelGroupJoinRequestErr = errors.Join(signal.ErrGroupChanged, secret)
		}, signal.ErrGroupChanged, false, 8},
		{"uncertain cancellation", func(fake *signaltest.Fake) {
			fake.CancelGroupJoinRequestErr = errors.Join(signal.ErrGroupUpdateUncertain, secret)
		}, signal.ErrGroupUpdateUncertain, false, 8},
		{"accepted invalid", func(fake *signaltest.Fake) { fake.GroupCancelRequestVerificationErr = secret }, io.ErrClosedPipe, true, 8},
		{"cancellation revision overflow", func(fake *signaltest.Fake) {
			group := fake.GroupJoinServer[fakeLinkKey]
			group.Revision = math.MaxUint32
			fake.GroupJoinServer[fakeLinkKey] = group
		}, nil, false, math.MaxUint32},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			fake := cancelRequestFake(t)
			testCase.setup(fake)
			before := fake.GroupJoinServer[fakeLinkKey]
			cli := profileClient(t, fake, profileBobACI, true)

			got, err := cli.CancelGroupJoinRequest(t.Context(), roleFakeGroupID)
			if err == nil || (testCase.cause != nil && !errors.Is(err, testCase.cause)) || got.Accepted != testCase.accepted || got.Changed != testCase.accepted || got.Verified || got.Revision != testCase.revision || strings.Contains(err.Error(), "private-secret") {
				t.Fatal(got, err)
			}

			after := fake.GroupJoinServer[fakeLinkKey]
			if !testCase.accepted && !reflect.DeepEqual(before, after) {
				t.Fatal("failure mutated server")
			}

			if testCase.accepted && (after.Revision != 8 || len(after.Requesting) != 2) {
				t.Fatal("accepted server state lost")
			}
		})
	}
}

func TestFakeGroupCancelRequestNoop(t *testing.T) {
	t.Parallel()
	fake := cancelRequestFake(t)
	group := fake.GroupJoinServer[fakeLinkKey]
	group.Requesting = group.Requesting[1:]
	group.Revision = math.MaxUint32
	fake.GroupJoinServer[fakeLinkKey] = group
	cli := profileClient(t, fake, profileBobACI, true)

	got, err := cli.CancelGroupJoinRequest(t.Context(), roleFakeGroupID)
	if err != nil || got.Accepted || got.Changed || !got.Verified || got.Revision != math.MaxUint32 || !reflect.DeepEqual(fake.GroupJoinServer[fakeLinkKey], group) {
		t.Fatal(got, err)
	}
}

func TestFakeGroupCancelRequestLifecycle(t *testing.T) {
	t.Parallel()
	fake := cancelRequestFake(t)
	cli := profileClient(t, fake, profileBobACI, false)

	_, err := cli.CancelGroupJoinRequest(t.Context(), roleFakeGroupID)
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Fatal(err)
	}

	err = cli.Connect(t.Context(), signal.SendOnly())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = cli.CancelGroupJoinRequest(ctx, roleFakeGroupID)
	if !errors.Is(err, context.Canceled) || fake.GroupJoinServer[fakeLinkKey].Revision != 7 {
		t.Fatal(err)
	}

	err = cli.Close()
	if err != nil {
		t.Fatal(err)
	}

	_, err = cli.CancelGroupJoinRequest(t.Context(), roleFakeGroupID)
	if !errors.Is(err, signal.ErrClosed) {
		t.Fatal(err)
	}

	for _, evt := range []*signal.Connection{{State: signal.StateLoggedOut}, {State: signal.StateFailed, Err: signal.ErrConnectionFailed}} {
		fake = cancelRequestFake(t)
		fake.Incoming = []signal.Event{evt}
		cli = profileClient(t, fake, profileBobACI, true)
		_, err = cli.CancelGroupJoinRequest(t.Context(), roleFakeGroupID)

		want := signal.ErrDeviceUnlinked
		if evt.State == signal.StateFailed {
			want = signal.ErrConnectionFailed
		}

		if !errors.Is(err, want) {
			t.Fatal(err)
		}
	}
}
