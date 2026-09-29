package app_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func TestGroupsAddMembers(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{familyTitle, groupID, masterKey, app.GroupPrefix + groupID} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()
			fake.GroupInvitees = map[string]bool{bobACI: true}
			fake.GroupInviteTime = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
			use := open(t, fake)

			got, err := use.GroupsAddMembers(t.Context(), app.AddGroupMembersRequest{
				Group: ref, Members: []string{aliceNumber, aliceACI, bobUsername, carolACI, app.SelfRecipient},
			})
			if err != nil {
				t.Fatal(err)
			}

			assertGroupAddition(t, got, fake.GroupInviteTime)

			again, err := use.GroupsAddMembers(t.Context(), app.AddGroupMembersRequest{
				Group: ref, Members: []string{aliceACI, bobACI, carolACI, testAccount().ACI},
			})
			if err != nil || !reflect.DeepEqual(again, got) {
				t.Fatalf("repeat addition = %+v, %v", again, err)
			}
		})
	}
}

func assertGroupAddition(t *testing.T, got signal.Group, invitedAt time.Time) {
	t.Helper()

	if got.Revision != 5 || len(got.Members) != 3 || len(got.Pending) != 1 {
		t.Fatalf("added = %+v", got)
	}

	pending := got.Pending[0]
	if pending.Recipient.ACI != bobACI || pending.AddedBy.ACI != testAccount().ACI ||
		!pending.InvitedAt.Equal(invitedAt) {
		t.Fatalf("invitation = %+v", pending)
	}

	membership, role := got.MembershipOf(carolACI)
	if membership != signal.MembershipMember || role != signal.GroupRoleMember {
		t.Fatal("new member missing")
	}
}

func TestGroupsAddMembersValidation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		req  app.AddGroupMembersRequest
		want error
	}{
		{app.AddGroupMembersRequest{Members: []string{bobACI}}, signal.ErrUnknownGroup},
		{app.AddGroupMembersRequest{Group: "group:bad", Members: []string{bobACI}}, app.ErrInvalidRecipient},
		{app.AddGroupMembersRequest{Group: familyTitle}, app.ErrInvalidRecipient},
		{app.AddGroupMembersRequest{Group: familyTitle, Members: []string{"bad"}}, app.ErrInvalidRecipient},
		{
			app.AddGroupMembersRequest{Group: familyTitle, Members: []string{app.GroupPrefix + groupID}},
			app.ErrInvalidRecipient,
		},
		{app.AddGroupMembersRequest{Group: nobody, Members: []string{bobACI}}, signal.ErrUnknownGroup},
	} {
		fake := groupsFake()

		_, err := open(t, fake).GroupsAddMembers(t.Context(), test.req)
		if !errors.Is(err, test.want) || len(fake.Connects()) != 0 {
			t.Errorf("%+v: error %v, connections %+v", test.req, err, fake.Connects())
		}
	}
}

func TestGroupsAddMembersFailureIsAtomic(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, target string
		setup        func(*signaltest.Fake)
		want         error
	}{
		{"unknown recipient", "+4915999999999", nil, signal.ErrNotOnSignal},
		{"addition connect failure", bobACI, func(f *signaltest.Fake) { f.ConnectErr = errBoom }, errBoom},
		{"addition server failure", bobACI, func(f *signaltest.Fake) { f.AddGroupMembersErr = errBoom }, errBoom},
		{
			"addition conflict failure", bobACI,
			func(f *signaltest.Fake) { f.AddGroupMembersErr = signal.ErrGroupChanged }, signal.ErrGroupChanged,
		},
		{"addition permission failure", bobACI, func(f *signaltest.Fake) {
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

			_, err := open(t, fake).GroupsAddMembers(t.Context(), app.AddGroupMembersRequest{
				Group: familyTitle, Members: []string{carolACI, test.target},
			})
			if !errors.Is(err, test.want) || !reflect.DeepEqual(fake.GroupInfo[groupID], before) {
				t.Fatalf("failed addition = %v, group %+v", err, fake.GroupInfo[groupID])
			}
		})
	}
}

type partialAddMembersClient struct {
	signal.Client

	calls int
}

func (c *partialAddMembersClient) AddGroupMembers(context.Context, string, []signal.Recipient) (signal.Group, error) {
	c.calls++
	return signal.Group{ID: groupID, Revision: 5}, errBoom
}

func TestGroupsAddMembersDoesNotRetry(t *testing.T) {
	t.Parallel()

	client, err := groupsFake().Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	partial := &partialAddMembersClient{Client: client}

	group, err := app.New(partial).GroupsAddMembers(t.Context(), app.AddGroupMembersRequest{
		Group: familyTitle, Members: []string{bobACI},
	})
	if !errors.Is(err, errBoom) || group.ID != groupID || group.Revision != 5 || partial.calls != 1 {
		t.Fatalf("partial = %+v, %v; calls %d", group, err, partial.calls)
	}
}
