package app_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func TestGroupsSetBanned(t *testing.T) { //nolint:cyclop // sequential ban, no-op and unban contracts
	t.Parallel()

	for _, ref := range []string{familyTitle, groupID, masterKey, app.GroupPrefix + groupID} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()
			bannedAt := time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC)
			fake.GroupBanTime = bannedAt
			before := fake.GroupInfo[groupID]
			before.Pending = []signal.PendingMember{{Recipient: signal.Recipient{ACI: bobACI}}}
			before.Requesting = []signal.RequestingMember{{Recipient: signal.Recipient{ACI: carolACI}}}
			fake.GroupInfo[groupID] = before
			use := open(t, fake)
			req := app.GroupBanRequest{
				Group: ref, Members: []string{aliceNumber, aliceACI, bobUsername, carolACI}, Banned: true,
			}

			got, err := use.GroupsSetBanned(t.Context(), req)
			if err != nil || got.ID != groupID || got.Revision != 5 || len(got.Members) != 1 ||
				len(got.Pending) != 0 || len(got.Requesting) != 0 || len(got.Banned) != 3 {
				t.Fatalf("ban = %+v, %v", got, err)
			}

			for i, aci := range []string{aliceACI, bobACI, carolACI} {
				if got.Banned[i].Recipient.ACI != aci || !got.Banned[i].BannedAt.Equal(bannedAt) {
					t.Fatalf("ban %d = %+v", i, got.Banned[i])
				}
			}

			fake.GroupBanTime = bannedAt.Add(time.Hour)

			got, err = use.GroupsSetBanned(t.Context(), req)
			if err != nil || got.Revision != 5 || !got.Banned[0].BannedAt.Equal(bannedAt) {
				t.Fatalf("repeat = %+v, %v", got, err)
			}

			req.Banned = false

			got, err = use.GroupsSetBanned(t.Context(), req)
			if err != nil || got.Revision != 6 || len(got.Banned) != 0 || len(got.Members) != 1 ||
				len(got.Pending) != 0 || len(got.Requesting) != 0 {
				t.Fatalf("unban = %+v, %v", got, err)
			}

			got, err = use.GroupsSetBanned(t.Context(), req)
			if err != nil || got.Revision != 6 {
				t.Fatalf("unban repeat = %+v, %v", got, err)
			}

			if calls := fake.Connects(); len(calls) != 1 || calls[0] != testAccount().ACI {
				t.Fatalf("connections = %+v", calls)
			}
		})
	}
}

func TestGroupBanRequestPreflight(t *testing.T) {
	t.Parallel()

	for _, req := range []app.GroupBanRequest{
		{Members: []string{aliceACI}},
		{Group: "group:invalid-ban-group", Members: []string{aliceACI}},
		{Group: familyTitle},
		{Group: familyTitle, Members: []string{"invalid-ban-user"}},
		{Group: familyTitle, Members: []string{app.GroupPrefix + groupID}},
		{Group: familyTitle, Members: []string{app.SelfRecipient}},
	} {
		for _, banned := range []bool{false, true} {
			req.Banned = banned
			fake := groupsFake()

			_, err := open(t, fake).GroupsSetBanned(t.Context(), req)
			if err == nil || len(fake.Connects()) != 0 {
				t.Fatalf("%+v: %v, connects %+v", req, err, fake.Connects())
			}
		}
	}
}

func TestGroupBanFailureIsAtomic(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, member string
		setup        func(*signaltest.Fake)
		want         error
	}{
		{"unknown number", "+4915999999999", nil, signal.ErrNotOnSignal},
		{"own ACI", testAccount().ACI, nil, app.ErrInvalidRecipient},
		{"own number", testAccount().Number, nil, app.ErrInvalidRecipient},
		{"ban connect error", aliceACI, func(f *signaltest.Fake) { f.ConnectErr = errBoom }, errBoom},
		{"not admin", aliceACI, func(f *signaltest.Fake) {
			g := f.GroupInfo[groupID]
			g.Members[0].Role = signal.GroupRoleMember
			f.GroupInfo[groupID] = g
		}, signal.ErrGroupPermission},
		{
			"ban conflict", aliceACI, func(f *signaltest.Fake) { f.SetGroupBannedErr = signal.ErrGroupChanged },
			signal.ErrGroupChanged,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()
			if test.setup != nil {
				test.setup(fake)
			}

			before := fake.GroupInfo[groupID]
			before.Members = slices.Clone(before.Members)
			before.Pending = slices.Clone(before.Pending)
			before.Requesting = slices.Clone(before.Requesting)
			before.Banned = slices.Clone(before.Banned)

			_, err := open(t, fake).GroupsSetBanned(t.Context(), app.GroupBanRequest{
				Group: familyTitle, Members: []string{aliceNumber, test.member}, Banned: true,
			})
			if !errors.Is(err, test.want) || !reflect.DeepEqual(fake.GroupInfo[groupID], before) {
				t.Fatalf("error %v, group %+v", err, fake.GroupInfo[groupID])
			}
		})
	}
}

type partialBanClient struct {
	signal.Client

	calls   int
	result  signal.Group
	failure error
}

func (c *partialBanClient) SetGroupBanned(context.Context, string, []signal.Recipient, bool) (signal.Group, error) {
	c.calls++
	return c.result, c.failure
}

func TestGroupBanDoesNotRetryFailures(t *testing.T) { //nolint:cyclop // accepted and uncertain outcomes for both modes
	t.Parallel()

	for _, banned := range []bool{false, true} {
		for _, accepted := range []bool{false, true} {
			client, err := groupsFake().Factory(t.Context(), signal.Options{})
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = client.Close() })

			partial := &partialBanClient{
				Client: client, failure: fmt.Errorf("inspect groups show before retrying: %w", signal.ErrGroupUpdateUncertain),
			}
			want := signal.ErrGroupUpdateUncertain

			if accepted {
				partial.result = signal.Group{ID: groupID, Revision: 5}
				partial.failure = errBoom
				want = errBoom
			}

			got, err := app.New(partial).GroupsSetBanned(t.Context(), app.GroupBanRequest{
				Group: familyTitle, Members: []string{aliceNumber}, Banned: banned,
			})
			if !errors.Is(err, want) || partial.calls != 1 || got.ID != partial.result.ID ||
				got.Revision != partial.result.Revision || strings.Contains(err.Error(), "accepted") != accepted ||
				!strings.Contains(err.Error(), "inspect groups show") {
				t.Fatalf("outcome = %+v,%v attempts %d", got, err, partial.calls)
			}
		}
	}
}
