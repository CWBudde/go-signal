package signal_test

import (
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestPrepareLeaveTypedInvitation(t *testing.T) {
	t.Parallel()

	self := signal.Recipient{ACI: selfACI, PNI: bobACI}

	tests := []struct {
		name    string
		pending signal.Recipient
		promote []signal.Recipient
		want    error
	}{
		{"own PNI", signal.Recipient{PNI: bobACI}, nil, nil},
		{"own ACI", signal.Recipient{ACI: selfACI}, nil, nil},
		{"ACI colliding with own PNI", signal.Recipient{ACI: bobACI}, nil, signal.ErrNotAMember},
		{"PNI colliding with own ACI", signal.Recipient{PNI: selfACI}, nil, signal.ErrNotAMember},
		{"ambiguous recipient", self, nil, signal.ErrNotAMember},
		{
			"invited cannot promote",
			signal.Recipient{PNI: bobACI},
			[]signal.Recipient{{ACI: aliceACI}},
			signal.ErrInvalidPromotion,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			group := signal.Group{Pending: []signal.PendingMember{{Recipient: test.pending, Role: signal.GroupRoleAdmin}}}

			got, err := group.PrepareLeave(self, test.promote)
			if !errors.Is(err, test.want) {
				t.Fatalf("PrepareLeave = %v, want %v", err, test.want)
			}

			if err == nil && (got.Membership != signal.MembershipPending || got.Role != signal.GroupRoleAdmin) {
				t.Errorf("leave membership = %v/%v", got.Membership, got.Role)
			}

			if group.Membership != signal.MembershipNone {
				t.Error("mutated input")
			}
		})
	}
}

func TestPrepareLeaveMemberPrecedence(t *testing.T) {
	t.Parallel()

	group := signal.Group{
		Members: []signal.GroupMember{member(selfACI, signal.GroupRoleAdmin), member(aliceACI, signal.GroupRoleMember)},
		Pending: []signal.PendingMember{{Recipient: signal.Recipient{PNI: bobACI}, Role: signal.GroupRoleMember}},
	}

	_, err := group.PrepareLeave(signal.Recipient{ACI: selfACI, PNI: bobACI}, nil)
	if !errors.Is(err, signal.ErrLastAdmin) {
		t.Fatalf("PrepareLeave = %v, want last-admin refusal", err)
	}
}

func TestPrepareLeaveInvalidIdentities(t *testing.T) {
	t.Parallel()

	group := signal.Group{Pending: []signal.PendingMember{{Recipient: signal.Recipient{PNI: bobACI}}}}
	for _, aci := range []string{"", "malformed-self-id", nilACITestValue} {
		_, err := group.PrepareLeave(signal.Recipient{ACI: aci, PNI: bobACI}, nil)
		if !errors.Is(err, signal.ErrUnresolvable) {
			t.Errorf("invalid ACI %q = %v", aci, err)
		}
	}

	for _, pni := range []string{"", "malformed-self-id", nilACITestValue} {
		_, err := group.PrepareLeave(signal.Recipient{ACI: selfACI, PNI: pni}, nil)
		if !errors.Is(err, signal.ErrNotAMember) {
			t.Errorf("invalid PNI %q = %v", pni, err)
		}
	}
}
