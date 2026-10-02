package signal_test

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestWithMemberRole(t *testing.T) { //nolint:funlen,cyclop // policy matrix
	t.Parallel()

	for _, test := range []struct {
		name    string
		self    string
		targets []signal.Recipient
		role    signal.GroupRole
		want    error
	}{
		{"promote", selfACI, []signal.Recipient{{ACI: aliceACI}}, signal.GroupRoleAdmin, nil},
		{
			"canonical duplicates",
			selfACI,
			[]signal.Recipient{{ACI: strings.ToUpper(aliceACI)}, {ACI: aliceACI}},
			signal.GroupRoleAdmin, nil,
		},
		{"self already admin", selfACI, []signal.Recipient{{ACI: selfACI}}, signal.GroupRoleAdmin, nil},
		{"already member", selfACI, []signal.Recipient{{ACI: aliceACI}}, signal.GroupRoleMember, nil},
		{"last admin", selfACI, []signal.Recipient{{ACI: selfACI}}, signal.GroupRoleMember, signal.ErrLastAdmin},
		{"pending target", selfACI, []signal.Recipient{{ACI: bobACI}}, signal.GroupRoleAdmin, signal.ErrInvalidGroupMember},
		{
			"requesting target",
			selfACI,
			[]signal.Recipient{{ACI: requesterACI}},
			signal.GroupRoleAdmin, signal.ErrInvalidGroupMember,
		},
		{
			"atomic absent",
			selfACI,
			[]signal.Recipient{{ACI: aliceACI}, {ACI: "ffffffff-ffff-4fff-8fff-ffffffffffff"}},
			signal.GroupRoleAdmin, signal.ErrInvalidGroupMember,
		},
		{
			"member no-op forbidden",
			aliceACI,
			[]signal.Recipient{{ACI: aliceACI}},
			signal.GroupRoleMember, signal.ErrGroupPermission,
		},
		{"pending actor", bobACI, []signal.Recipient{{ACI: aliceACI}}, signal.GroupRoleAdmin, signal.ErrNotAMember},
		{"requesting actor", requesterACI, []signal.Recipient{{ACI: aliceACI}}, signal.GroupRoleAdmin, signal.ErrNotAMember},
		{
			"absent actor",
			"unknown-role-actor",
			[]signal.Recipient{{ACI: aliceACI}},
			signal.GroupRoleAdmin, signal.ErrNotAMember,
		},
		{"no role targets", selfACI, nil, signal.GroupRoleAdmin, signal.ErrInvalidGroupMember},
		{
			"invalid aci",
			selfACI,
			[]signal.Recipient{{ACI: "invalid-role-member-aci"}},
			signal.GroupRoleAdmin, signal.ErrUnresolvable,
		},
		{"zero aci", selfACI, []signal.Recipient{{ACI: nilACITestValue}}, signal.GroupRoleAdmin, signal.ErrUnresolvable},
		{"unknown role", selfACI, []signal.Recipient{{ACI: aliceACI}}, signal.GroupRoleUnknown, signal.ErrInvalidGroupMember},
		{"invalid role", selfACI, []signal.Recipient{{ACI: aliceACI}}, signal.GroupRole(42), signal.ErrInvalidGroupMember},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			group := removalGroup()
			targets := slices.Clone(test.targets)

			got, err := group.WithMemberRole(test.self, targets, test.role)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %+v, %v, want %v", got, err, test.want)
			}

			if !reflect.DeepEqual(group, removalGroup()) || !reflect.DeepEqual(targets, test.targets) {
				t.Fatal("input changed")
			}

			if err != nil {
				return
			}

			wantRevision := uint32(9)
			if test.role == signal.GroupRoleAdmin && test.targets[0].ACI != selfACI {
				wantRevision = 10
			}

			if got.Revision != wantRevision ||
				got.Title != "Keep title" || got.Role != signal.GroupRoleAdmin || got.Membership != signal.MembershipMember {
				t.Fatalf("metadata %+v", got)
			}

			for _, target := range test.targets {
				_, role := got.MembershipOf(strings.ToLower(target.ACI))
				if role != test.role {
					t.Fatalf("role = %v", role)
				}
			}

			got.Members[0].Role = signal.GroupRoleMember
			got.Pending[0].Role = signal.GroupRoleMember
			got.Requesting[0].Recipient.ACI = selfACI

			if !reflect.DeepEqual(group, removalGroup()) {
				t.Fatal("returned slices alias input")
			}
		})
	}
}

func TestWithMemberRoleSelfAndLastAdmin(t *testing.T) {
	t.Parallel()

	group := removalGroup()
	group.Members[1].Role = signal.GroupRoleAdmin

	got, err := group.WithMemberRole(selfACI, []signal.Recipient{{ACI: selfACI}}, signal.GroupRoleMember)
	if err != nil ||
		got.Role != signal.GroupRoleMember || got.Revision != 10 || got.Members[1].Role != signal.GroupRoleAdmin {
		t.Fatalf("self demotion %+v, %v", got, err)
	}

	_, err = group.WithMemberRole(selfACI, []signal.Recipient{{ACI: selfACI}, {ACI: aliceACI}}, signal.GroupRoleMember)
	if !errors.Is(err, signal.ErrLastAdmin) {
		t.Fatalf("all admins = %v", err)
	}

	group.Members = group.Members[:1]

	_, err = group.WithMemberRole(selfACI, []signal.Recipient{{ACI: selfACI}}, signal.GroupRoleMember)
	if !errors.Is(err, signal.ErrLastAdmin) {
		t.Fatalf("singleton = %v", err)
	}
}

func TestWithMemberRoleRevisionOverflow(t *testing.T) {
	t.Parallel()

	group := removalGroup()
	group.Revision = math.MaxUint32

	_, err := group.WithMemberRole(selfACI, []signal.Recipient{{ACI: aliceACI}}, signal.GroupRoleAdmin)
	if !errors.Is(err, signal.ErrUnknownGroup) {
		t.Fatalf("overflow = %v", err)
	}

	got, err := group.WithMemberRole(selfACI, []signal.Recipient{{ACI: selfACI}}, signal.GroupRoleAdmin)
	if err != nil || got.Revision != math.MaxUint32 {
		t.Fatalf("no-op = %+v,%v", got, err)
	}
}
