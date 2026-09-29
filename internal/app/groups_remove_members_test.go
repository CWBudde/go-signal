package app_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func TestGroupsRemoveMembers(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{familyTitle, groupID, masterKey, app.GroupPrefix + groupID} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()
			group := fake.GroupInfo[groupID]
			group.Pending = []signal.PendingMember{{Recipient: signal.Recipient{ACI: bobACI}}}
			group.Requesting = []signal.RequestingMember{{Recipient: signal.Recipient{ACI: carolACI}}}
			fake.GroupInfo[groupID] = group

			got, err := open(t, fake).GroupsRemoveMembers(t.Context(), app.RemoveGroupMembersRequest{
				Group: ref, Members: []string{aliceNumber, aliceACI, bobUsername, carolACI},
			})
			if err != nil {
				t.Fatal(err)
			}

			if got.ID != groupID || got.Revision != 5 || len(got.Members) != 1 ||
				got.Members[0].Recipient.ACI != testAccount().ACI || len(got.Pending) != 0 || len(got.Requesting) != 0 {
				t.Errorf("removed = %+v", got)
			}

			if calls := fake.Connects(); len(calls) != 1 {
				t.Errorf("connections = %+v", calls)
			}
		})
	}
}

func TestGroupsRemoveMembersInvalidInput(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		req  app.RemoveGroupMembersRequest
		want error
	}{
		{"empty group", app.RemoveGroupMembersRequest{Members: []string{aliceACI}}, signal.ErrUnknownGroup},
		{
			"bad group ID",
			app.RemoveGroupMembersRequest{Group: "group:bad", Members: []string{aliceACI}},
			app.ErrInvalidRecipient,
		},
		{"no members", app.RemoveGroupMembersRequest{Group: familyTitle}, app.ErrInvalidRecipient},
		{"bad member", app.RemoveGroupMembersRequest{Group: familyTitle, Members: []string{"bad"}}, app.ErrInvalidRecipient},
		{
			"group member",
			app.RemoveGroupMembersRequest{Group: familyTitle, Members: []string{app.GroupPrefix + groupID}},
			app.ErrInvalidRecipient,
		},
		{
			app.SelfRecipient,
			app.RemoveGroupMembersRequest{Group: familyTitle, Members: []string{app.SelfRecipient}},
			app.ErrInvalidRecipient,
		},
		{"unknown title", app.RemoveGroupMembersRequest{Group: nobody, Members: []string{aliceACI}}, signal.ErrUnknownGroup},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()

			_, err := open(t, fake).GroupsRemoveMembers(t.Context(), test.req)
			if !errors.Is(err, test.want) || !strings.HasPrefix(err.Error(), "groups remove-members:") {
				t.Errorf("error = %v, want %v", err, test.want)
			}

			if len(fake.Connects()) != 0 {
				t.Error("invalid input connected")
			}
		})
	}
}

func TestGroupsRemoveMembersFailureIsAtomic(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		member string
		setup  func(*signaltest.Fake)
		want   error
	}{
		{"unknown member", "+4915999999999", nil, signal.ErrNotOnSignal},
		{"absent member", bobACI, nil, signal.ErrInvalidGroupMember},
		{"own ACI", testAccount().ACI, nil, app.ErrInvalidRecipient},
		{"selected account number", testAccount().Number, nil, app.ErrInvalidRecipient},
		{"connect", aliceACI, func(f *signaltest.Fake) { f.ConnectErr = errBoom }, errBoom},
		{"server", aliceACI, func(f *signaltest.Fake) { f.RemoveGroupMembersErr = errBoom }, errBoom},
		{
			"conflict", aliceACI, func(f *signaltest.Fake) { f.RemoveGroupMembersErr = signal.ErrGroupChanged },
			signal.ErrGroupChanged,
		},
		{"permission", aliceACI, func(f *signaltest.Fake) {
			group := f.GroupInfo[groupID]
			group.Members[0].Role = signal.GroupRoleMember
			f.GroupInfo[groupID] = group
		}, signal.ErrGroupPermission},
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

			_, err := open(t, fake).GroupsRemoveMembers(t.Context(), app.RemoveGroupMembersRequest{
				Group: familyTitle, Members: []string{aliceNumber, test.member},
			})
			if !errors.Is(err, test.want) {
				t.Errorf("error = %v, want %v", err, test.want)
			}

			if !reflect.DeepEqual(fake.GroupInfo[groupID], before) {
				t.Error("failed removal changed the group")
			}

			if errors.Is(test.want, app.ErrInvalidRecipient) && !strings.Contains(err.Error(), "groups leave") {
				t.Error("self removal error missing groups leave hint")
			}
		})
	}
}

type partialRemoveMembersClient struct {
	signal.Client

	calls int
}

func (c *partialRemoveMembersClient) RemoveGroupMembers(
	context.Context, string, []signal.Recipient,
) (signal.Group, error) {
	c.calls++

	return signal.Group{ID: groupID, Revision: 5}, errBoom
}

func TestGroupsRemoveMembersDoesNotRetryPartialFailure(t *testing.T) {
	t.Parallel()

	client, err := groupsFake().Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	partial := &partialRemoveMembersClient{Client: client}

	group, err := app.New(partial).GroupsRemoveMembers(t.Context(), app.RemoveGroupMembersRequest{
		Group: familyTitle, Members: []string{aliceNumber},
	})
	if !errors.Is(err, errBoom) || group.ID != groupID || group.Revision != 5 || partial.calls != 1 {
		t.Errorf("partial result = %+v, %v; attempts %d", group, err, partial.calls)
	}
}
