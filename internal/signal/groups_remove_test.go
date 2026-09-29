package signal_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

const requesterACI = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"

func removalGroup() signal.Group {
	return signal.Group{
		Title: "Keep title", Revision: 9, Membership: signal.MembershipMember, Role: signal.GroupRoleAdmin,
		Members:    []signal.GroupMember{member(selfACI, signal.GroupRoleAdmin), member(aliceACI, signal.GroupRoleMember)},
		Pending:    []signal.PendingMember{{Recipient: signal.Recipient{ACI: bobACI}, Role: signal.GroupRoleAdmin}},
		Requesting: []signal.RequestingMember{{Recipient: signal.Recipient{ACI: requesterACI}}},
	}
}

func TestWithRemovedMembers(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		self    string
		members []signal.Recipient
		want    error
	}{
		{"full", selfACI, []signal.Recipient{{ACI: aliceACI}}, nil},
		{"pending", selfACI, []signal.Recipient{{ACI: bobACI}}, nil},
		{"requesting", selfACI, []signal.Recipient{{ACI: requesterACI}}, nil},
		{
			"all and duplicates", selfACI,
			[]signal.Recipient{{ACI: aliceACI}, {ACI: bobACI}, {ACI: requesterACI}, {ACI: aliceACI}},
			nil,
		},
		{"self", selfACI, []signal.Recipient{{ACI: selfACI}}, signal.ErrInvalidGroupMember},
		{
			"absent among valid", selfACI,
			[]signal.Recipient{{ACI: aliceACI}, {ACI: "ffffffff-ffff-4fff-8fff-ffffffffffff"}},
			signal.ErrInvalidGroupMember,
		},
		{"nonadmin", aliceACI, []signal.Recipient{{ACI: bobACI}}, signal.ErrGroupPermission},
		{"pending admin", bobACI, []signal.Recipient{{ACI: aliceACI}}, signal.ErrNotAMember},
		{"requester", requesterACI, []signal.Recipient{{ACI: aliceACI}}, signal.ErrNotAMember},
		{"outsider", "outsider", []signal.Recipient{{ACI: aliceACI}}, signal.ErrNotAMember},
		{"empty", selfACI, nil, signal.ErrInvalidGroupMember},
		{"invalid ACI", selfACI, []signal.Recipient{{ACI: "invalid"}}, signal.ErrUnresolvable},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			group := removalGroup()

			got, err := group.WithRemovedMembers(test.self, test.members)
			if !errors.Is(err, test.want) {
				t.Fatalf("WithRemovedMembers = %+v, %v, want %v", got, err, test.want)
			}

			if !reflect.DeepEqual(group, removalGroup()) {
				t.Fatal("input group changed")
			}

			if err != nil {
				return
			}

			assertRemovalResult(t, group, got, test.members)
		})
	}
}

func assertRemovalResult(t *testing.T, group, got signal.Group, targets []signal.Recipient) {
	t.Helper()

	for _, target := range targets {
		membership, _ := got.MembershipOf(target.ACI)
		if membership != signal.MembershipNone {
			t.Errorf("target %s remains", target.ACI)
		}
	}

	if got.Title != group.Title || got.Revision != group.Revision ||
		got.Membership != group.Membership || got.Role != group.Role {
		t.Errorf("unrelated metadata changed: %+v", got)
	}

	membership, role := got.MembershipOf(selfACI)
	if membership != signal.MembershipMember || role != signal.GroupRoleAdmin {
		t.Fatal("creator membership changed")
	}
}

func TestNormalizeGroupRemovalMembers(t *testing.T) {
	t.Parallel()

	input := []signal.Recipient{{ACI: strings.ToUpper(aliceACI), Username: "alice.42"}, {ACI: aliceACI}, {ACI: bobACI}}
	got, err := signal.NormalizeGroupRemovalMembers(input)

	want := []signal.Recipient{{ACI: aliceACI, Username: "alice.42"}, {ACI: bobACI}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("normalize = %+v, %v", got, err)
	}

	if input[0].ACI != strings.ToUpper(aliceACI) {
		t.Fatal("input changed")
	}

	for _, aci := range []string{"", "bad", "00000000-0000-0000-0000-000000000000"} {
		_, err = signal.NormalizeGroupRemovalMembers([]signal.Recipient{{ACI: aci}})
		if !errors.Is(err, signal.ErrUnresolvable) {
			t.Errorf("ACI %q: %v", aci, err)
		}
	}
}
