package signal_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	selfACI  = "11111111-1111-4111-8111-111111111111"
	aliceACI = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	bobACI   = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

func member(aci string, role signal.GroupRole) signal.GroupMember {
	return signal.GroupMember{Recipient: signal.Recipient{ACI: aci}, Role: role}
}

func TestMembershipOf(t *testing.T) {
	t.Parallel()

	group := signal.Group{
		Members:    []signal.GroupMember{member(selfACI, signal.GroupRoleAdmin)},
		Pending:    []signal.PendingMember{{Recipient: signal.Recipient{ACI: aliceACI}, Role: signal.GroupRoleAdmin}},
		Requesting: []signal.RequestingMember{{Recipient: signal.Recipient{ACI: bobACI}}},
	}

	tests := []struct {
		aci        string
		membership signal.Membership
		role       signal.GroupRole
	}{
		{selfACI, signal.MembershipMember, signal.GroupRoleAdmin},
		{aliceACI, signal.MembershipPending, signal.GroupRoleAdmin},
		{bobACI, signal.MembershipRequesting, signal.GroupRoleUnknown},
		{"cccccccc-cccc-4ccc-8ccc-cccccccccccc", signal.MembershipNone, signal.GroupRoleUnknown},
		{"", signal.MembershipNone, signal.GroupRoleUnknown},
	}

	for _, test := range tests {
		membership, role := group.MembershipOf(test.aci)
		if membership != test.membership || role != test.role {
			t.Errorf("MembershipOf(%q) = %v, %v; want %v, %v", test.aci, membership, role, test.membership, test.role)
		}
	}

	names := []string{
		signal.MembershipNone.String(), signal.MembershipMember.String(),
		signal.MembershipPending.String(), signal.MembershipRequesting.String(),
		signal.GroupRoleUnknown.String(), signal.GroupRoleMember.String(), signal.GroupRoleAdmin.String(),
	}
	if want := strings.Fields("none member pending requesting unknown member admin"); !slices.Equal(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
}

type checkLeaveTest struct {
	name    string
	group   signal.Group
	promote []signal.Recipient
	want    error
}

func checkLeaveTests() []checkLeaveTest {
	alice, bob := signal.Recipient{ACI: aliceACI}, signal.Recipient{ACI: bobACI}
	admin, plain := signal.GroupRoleAdmin, signal.GroupRoleMember
	// members has us with the role self and alice with the role other.
	members := func(self, other signal.GroupRole) signal.Group {
		return signal.Group{Members: []signal.GroupMember{member(selfACI, self), member(aliceACI, other)}}
	}
	ourselves := signal.Recipient{ACI: selfACI}

	return []checkLeaveTest{
		{"member", members(plain, admin), nil, nil},
		{"one of two admins", members(admin, admin), nil, nil},
		{"last admin", members(admin, plain), nil, signal.ErrLastAdmin},
		{"last admin promotes", members(admin, plain), []signal.Recipient{alice}, nil},
		{"promote twice", members(admin, plain), []signal.Recipient{alice, alice}, nil},
		{"alone", signal.Group{Members: []signal.GroupMember{member(selfACI, admin)}}, nil, nil},
		{"promote an admin", members(admin, admin), []signal.Recipient{alice}, nil},
		{"promote a non-member", members(admin, plain), []signal.Recipient{bob}, signal.ErrInvalidPromotion},
		{"promote ourselves", members(admin, plain), []signal.Recipient{ourselves}, signal.ErrInvalidPromotion},
		{"promote without ACI", members(admin, plain), []signal.Recipient{{Number: "+15550101"}}, signal.ErrInvalidPromotion},
		{"member can't promote", members(plain, plain), []signal.Recipient{alice}, signal.ErrInvalidPromotion},
		{"invited", signal.Group{Pending: []signal.PendingMember{{Recipient: ourselves}}}, nil, nil},
		{
			"invited can't promote",
			signal.Group{Pending: []signal.PendingMember{{Recipient: ourselves}}},
			[]signal.Recipient{alice},
			signal.ErrInvalidPromotion,
		},
		{"requesting", signal.Group{Requesting: []signal.RequestingMember{{Recipient: ourselves}}}, nil, nil},
		{"not a member", signal.Group{Members: []signal.GroupMember{member(aliceACI, admin)}}, nil, signal.ErrNotAMember},
	}
}

func TestCheckLeave(t *testing.T) {
	t.Parallel()

	for _, test := range checkLeaveTests() {
		err := test.group.CheckLeave(selfACI, test.promote)
		if !errors.Is(err, test.want) || (test.want == nil && err != nil) {
			t.Errorf("%s: CheckLeave = %v, want %v", test.name, err, test.want)
		}
	}
}

func TestSortGroups(t *testing.T) {
	t.Parallel()

	groups := []signal.Group{
		{ID: "4"}, {ID: "3", Title: "beta"}, {ID: "2", Title: "Alpha"}, {ID: "1", Title: "beta"}, {ID: "0"},
	}
	signal.SortGroups(groups)

	ids := make([]string, 0, len(groups))
	for _, group := range groups {
		ids = append(ids, group.ID)
	}

	if want := []string{"2", "1", "3", "0", "4"}; !slices.Equal(ids, want) {
		t.Errorf("order = %v, want %v", ids, want)
	}
}
