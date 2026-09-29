package signal_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

const newMemberACI = "ffffffff-ffff-4fff-8fff-ffffffffffff"

func TestCheckAddMembers(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, self string
		allow      bool
		members    []signal.Recipient
		want       []signal.Recipient
		wantErr    error
	}{
		{
			"new and duplicate", selfACI, false,
			[]signal.Recipient{{ACI: strings.ToUpper(newMemberACI)}, {ACI: newMemberACI}},
			[]signal.Recipient{{ACI: newMemberACI}},
			nil,
		},
		{
			"existing and self", selfACI, false,
			[]signal.Recipient{{ACI: aliceACI}, {ACI: bobACI}, {ACI: selfACI}},
			[]signal.Recipient{},
			nil,
		},
		{
			"approve request", selfACI, false,
			[]signal.Recipient{{ACI: requesterACI}},
			[]signal.Recipient{{ACI: requesterACI}},
			nil,
		},
		{
			"member allowed", aliceACI, true,
			[]signal.Recipient{{ACI: newMemberACI}},
			[]signal.Recipient{{ACI: newMemberACI}},
			nil,
		},
		{"member restricted", aliceACI, false, []signal.Recipient{{ACI: newMemberACI}}, nil, signal.ErrGroupPermission},
		{
			"member cannot approve", aliceACI, true,
			[]signal.Recipient{{ACI: newMemberACI}, {ACI: requesterACI}},
			nil, signal.ErrGroupPermission,
		},
		{"invited admin", bobACI, true, []signal.Recipient{{ACI: newMemberACI}}, nil, signal.ErrNotAMember},
		{"nonmember", newMemberACI, true, []signal.Recipient{{ACI: newMemberACI}}, nil, signal.ErrNotAMember},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			group := removalGroup()
			group.MembersCanAddMembers = test.allow

			got, err := group.CheckAddMembers(test.self, test.members)
			if !errors.Is(err, test.wantErr) || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("targets = %+v, %v; want %+v, %v", got, err, test.want, test.wantErr)
			}

			group.MembersCanAddMembers = false
			if !reflect.DeepEqual(group, removalGroup()) {
				t.Fatal("validation changed group")
			}
		})
	}
}

func TestCheckAddMembersInvalidTargets(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		members []signal.Recipient
		want    error
	}{
		{"no targets", nil, signal.ErrInvalidGroupMember},
		{"malformed ACI", []signal.Recipient{{ACI: "not-an-aci"}}, signal.ErrUnresolvable},
		{"zero", []signal.Recipient{{ACI: "00000000-0000-0000-0000-000000000000"}}, signal.ErrUnresolvable},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			group := removalGroup()

			got, err := group.CheckAddMembers(selfACI, test.members)
			if !errors.Is(err, test.want) || got != nil || !reflect.DeepEqual(group, removalGroup()) {
				t.Fatalf("invalid targets = %+v, %v", got, err)
			}
		})
	}
}
