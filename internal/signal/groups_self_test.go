package signal_test

import (
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestSelfMembership(t *testing.T) {
	t.Parallel()

	self := signal.Recipient{ACI: selfACI, PNI: bobACI}
	aci := signal.Recipient{ACI: selfACI}
	pni := signal.Recipient{PNI: bobACI}
	admin, plain := signal.GroupRoleAdmin, signal.GroupRoleMember
	invited := func(recipient signal.Recipient, role signal.GroupRole) signal.Group {
		return signal.Group{Pending: []signal.PendingMember{{Recipient: recipient, Role: role}}}
	}
	both := signal.Group{Pending: []signal.PendingMember{
		{Recipient: pni, Role: admin}, {Recipient: aci, Role: plain},
	}}
	full := invited(pni, admin)
	full.Members = []signal.GroupMember{member(selfACI, plain)}

	tests := []struct {
		name       string
		self       signal.Recipient
		group      signal.Group
		membership signal.Membership
		role       signal.GroupRole
	}{
		{"own PNI", self, invited(pni, admin), signal.MembershipPending, admin},
		{
			"foreign PNI", self, invited(signal.Recipient{PNI: aliceACI}, plain),
			signal.MembershipNone, signal.GroupRoleUnknown,
		},
		{
			"ACI UUID as PNI", self, invited(signal.Recipient{PNI: selfACI}, plain),
			signal.MembershipNone, signal.GroupRoleUnknown,
		},
		{
			"PNI UUID as ACI", self, invited(signal.Recipient{ACI: bobACI}, plain),
			signal.MembershipNone, signal.GroupRoleUnknown,
		},
		{"ambiguous recipient", self, invited(self, admin), signal.MembershipNone, signal.GroupRoleUnknown},
		{"ACI invitation preferred", self, both, signal.MembershipPending, plain},
		{"full member wins", self, full, signal.MembershipMember, plain},
		{
			"requesting ACI", self,
			signal.Group{Requesting: []signal.RequestingMember{{Recipient: aci}}},
			signal.MembershipRequesting, signal.GroupRoleUnknown,
		},
		{
			"requesting PNI", self,
			signal.Group{Requesting: []signal.RequestingMember{{Recipient: pni}}},
			signal.MembershipNone, signal.GroupRoleUnknown,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			membership, role := test.group.SelfMembership(test.self)
			if membership != test.membership || role != test.role {
				t.Errorf("membership = %v/%v; want %v/%v", membership, role, test.membership, test.role)
			}
		})
	}
}

func TestSelfMembershipInvalidIdentities(t *testing.T) {
	t.Parallel()

	const malformed = "malformed-membership-id"

	tests := []struct {
		name    string
		self    signal.Recipient
		invited signal.Recipient
	}{
		{"missing ACI", signal.Recipient{PNI: bobACI}, signal.Recipient{PNI: bobACI}},
		{"malformed ACI", signal.Recipient{ACI: malformed, PNI: bobACI}, signal.Recipient{PNI: bobACI}},
		{"malformed PNI", signal.Recipient{ACI: selfACI, PNI: malformed}, signal.Recipient{PNI: malformed}},
		{"zero PNI", signal.Recipient{ACI: selfACI, PNI: nilACITestValue}, signal.Recipient{PNI: nilACITestValue}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			group := signal.Group{Pending: []signal.PendingMember{{Recipient: test.invited, Role: signal.GroupRoleMember}}}

			membership, role := group.SelfMembership(test.self)
			if membership != signal.MembershipNone || role != signal.GroupRoleUnknown {
				t.Errorf("invalid identities matched: %v/%v", membership, role)
			}
		})
	}
}
