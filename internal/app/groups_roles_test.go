package app_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func TestGroupsSetMemberRole(t *testing.T) { //nolint:cyclop // sequential promotion, no-op and self-demotion contracts
	t.Parallel()

	for _, ref := range []string{familyTitle, groupID, masterKey, app.GroupPrefix + groupID} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()
			use := open(t, fake)

			group, err := use.GroupsSetMemberRole(t.Context(), app.GroupMemberRoleRequest{
				Group: ref, Members: []string{aliceNumber, aliceACI}, Role: signal.GroupRoleAdmin,
			})
			if err != nil || group.ID != groupID || group.Revision != 5 || group.Members[1].Role != signal.GroupRoleAdmin {
				t.Fatalf("promotion = %+v, %v", group, err)
			}

			group, err = use.GroupsSetMemberRole(t.Context(), app.GroupMemberRoleRequest{
				Group: ref, Members: []string{aliceNumber}, Role: signal.GroupRoleAdmin,
			})
			if err != nil || group.Revision != 5 {
				t.Fatalf("no-op = %+v, %v", group, err)
			}

			group, err = use.GroupsSetMemberRole(t.Context(), app.GroupMemberRoleRequest{
				Group: ref, Members: []string{app.SelfRecipient}, Role: signal.GroupRoleMember,
			})
			if err != nil || group.Revision != 6 || group.Role != signal.GroupRoleMember ||
				group.Members[0].Role != signal.GroupRoleMember || group.Members[1].Role != signal.GroupRoleAdmin {
				t.Fatalf("self-demotion = %+v, %v", group, err)
			}
		})
	}
}

func TestGroupsSetMemberRoleInvalidInput(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		req  app.GroupMemberRoleRequest
		want error
	}{
		{
			"blank role group",
			app.GroupMemberRoleRequest{Members: []string{aliceACI}, Role: signal.GroupRoleAdmin},
			signal.ErrUnknownGroup,
		},
		{
			"bad group",
			app.GroupMemberRoleRequest{
				Group: "group:invalid-role-group", Members: []string{aliceACI}, Role: signal.GroupRoleAdmin,
			},
			app.ErrInvalidRecipient,
		},
		{"no members", app.GroupMemberRoleRequest{Group: familyTitle, Role: signal.GroupRoleAdmin}, app.ErrInvalidRecipient},
		{
			"bad member",
			app.GroupMemberRoleRequest{
				Group: familyTitle, Members: []string{"invalid-role-member"}, Role: signal.GroupRoleAdmin,
			},
			app.ErrInvalidRecipient,
		},
		{
			"group as role member",
			app.GroupMemberRoleRequest{
				Group: familyTitle, Members: []string{app.GroupPrefix + groupID}, Role: signal.GroupRoleAdmin,
			},
			app.ErrInvalidRecipient,
		},
		{
			"bad role",
			app.GroupMemberRoleRequest{Group: familyTitle, Members: []string{aliceACI}},
			signal.ErrInvalidGroupMember,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()

			_, err := open(t, fake).GroupsSetMemberRole(t.Context(), test.req)
			if !errors.Is(err, test.want) || len(fake.Connects()) != 0 {
				t.Fatalf("invalid input = %v; connects %v", err, fake.Connects())
			}
		})
	}
}

func TestGroupsSetMemberRoleFailureIsAtomic(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		member string
		setup  func(*signaltest.Fake)
		want   error
	}{
		{"unresolvable", "+4915999999999", nil, signal.ErrNotOnSignal},
		{"absent", bobACI, nil, signal.ErrInvalidGroupMember},
		{"pending", bobACI, func(f *signaltest.Fake) {
			group := f.GroupInfo[groupID]
			group.Pending = []signal.PendingMember{{Recipient: signal.Recipient{ACI: bobACI}}}
			f.GroupInfo[groupID] = group
		}, signal.ErrInvalidGroupMember},
		{"not admin", aliceACI, func(f *signaltest.Fake) {
			group := f.GroupInfo[groupID]
			group.Members[0].Role = signal.GroupRoleMember
			f.GroupInfo[groupID] = group
		}, signal.ErrGroupPermission},
		{"role connect error", aliceACI, func(f *signaltest.Fake) { f.ConnectErr = errBoom }, errBoom},
		{
			"role conflict", aliceACI, func(f *signaltest.Fake) { f.SetGroupMemberRoleErr = signal.ErrGroupChanged },
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

			_, err := open(t, fake).GroupsSetMemberRole(t.Context(), app.GroupMemberRoleRequest{
				Group: familyTitle, Members: []string{aliceNumber, test.member}, Role: signal.GroupRoleAdmin,
			})
			if !errors.Is(err, test.want) || !reflect.DeepEqual(fake.GroupInfo[groupID], before) {
				t.Fatalf("failure = %v; group changed: %v", err, !reflect.DeepEqual(fake.GroupInfo[groupID], before))
			}
		})
	}
}

type partialMemberRoleClient struct {
	signal.Client

	calls   int
	result  signal.Group
	failure error
}

func (c *partialMemberRoleClient) SetGroupMemberRole(
	context.Context, string, []signal.Recipient, signal.GroupRole,
) (signal.Group, error) {
	c.calls++

	return c.result, c.failure
}

func TestGroupsSetMemberRoleDoesNotRetryAcceptedFailure(t *testing.T) {
	t.Parallel()

	client, err := groupsFake().Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	partial := &partialMemberRoleClient{
		Client: client, result: signal.Group{ID: groupID, Revision: 5}, failure: errBoom,
	}

	group, err := app.New(partial).GroupsSetMemberRole(t.Context(), app.GroupMemberRoleRequest{
		Group: familyTitle, Members: []string{aliceNumber}, Role: signal.GroupRoleAdmin,
	})
	if !errors.Is(err, errBoom) || group.ID != groupID || group.Revision != 5 || partial.calls != 1 ||
		!strings.Contains(err.Error(), "accepted") || !strings.Contains(err.Error(), "inspect groups show") {
		t.Fatalf("accepted failure = %+v, %v; attempts %d", group, err, partial.calls)
	}
}

func TestGroupsSetMemberRoleUncertainFailureDoesNotClaimAcceptance(t *testing.T) {
	t.Parallel()

	client, err := groupsFake().Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	uncertain := &partialMemberRoleClient{
		Client:  client,
		failure: fmt.Errorf("inspect groups show before retrying: %w", signal.ErrGroupUpdateUncertain),
	}

	group, err := app.New(uncertain).GroupsSetMemberRole(t.Context(), app.GroupMemberRoleRequest{
		Group: familyTitle, Members: []string{aliceNumber}, Role: signal.GroupRoleAdmin,
	})
	if !errors.Is(err, signal.ErrGroupUpdateUncertain) || group.ID != "" || group.Revision != 0 ||
		uncertain.calls != 1 || strings.Contains(err.Error(), "accepted") ||
		!strings.Contains(err.Error(), "inspect groups show") {
		t.Fatalf("uncertain failure = %+v, %v; attempts %d", group, err, uncertain.calls)
	}
}
