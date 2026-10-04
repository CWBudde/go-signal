package signal_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestWithRemovedMembersPNI(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		targets []signal.Recipient
		want    error
		pending int
	}{
		{"explicit PNI", []signal.Recipient{{PNI: bobACI}}, nil, 1},
		{"resolved number", []signal.Recipient{{ACI: requesterACI, PNI: bobACI}}, nil, 1},
		{"typed collision", []signal.Recipient{{ACI: bobACI}}, nil, 1},
		{"both identities", []signal.Recipient{{ACI: bobACI, PNI: bobACI}}, nil, 0},
		{"duplicates", []signal.Recipient{{PNI: bobACI}, {PNI: bobACI}}, nil, 1},
		{"canonical PNI", []signal.Recipient{{PNI: strings.ToUpper(bobACI)}}, nil, 1},
		{"malformed PNI", []signal.Recipient{{PNI: "invalid-removal-pni"}}, signal.ErrUnresolvable, 2},
		{"zero PNI", []signal.Recipient{{PNI: nilACITestValue}}, signal.ErrUnresolvable, 2},
		{
			"invalid additional PNI",
			[]signal.Recipient{{ACI: aliceACI, PNI: "invalid-removal-pni"}},
			signal.ErrUnresolvable, 2,
		},
		{"absent PNI atomic", []signal.Recipient{{ACI: aliceACI}, {PNI: requesterACI}}, signal.ErrInvalidGroupMember, 2},
		{"PNI cannot remove full ACI", []signal.Recipient{{PNI: aliceACI}}, signal.ErrInvalidGroupMember, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			group := removalGroup()
			group.Requesting = nil
			group.Pending = append(group.Pending, signal.PendingMember{Recipient: signal.Recipient{PNI: bobACI}})
			before := group

			got, err := group.WithRemovedMembers(selfACI, test.targets)
			if !errors.Is(err, test.want) {
				t.Fatalf("removal = %+v, %v; want %v", got, err, test.want)
			}

			if !reflect.DeepEqual(group, before) || len(group.Pending) != 2 || len(group.Members) != 2 {
				t.Fatal("input group changed")
			}

			if err == nil && (len(got.Pending) != test.pending || len(got.Members) != 2) {
				t.Fatalf("wrong identities removed: %+v", got)
			}
		})
	}
}

func TestWithRemovedMembersPNIPermissions(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		self signal.Recipient
		want error
	}{
		{"selected account PNI", signal.Recipient{ACI: selfACI, PNI: bobACI}, signal.ErrInvalidGroupMember},
		{"other account PNI", signal.Recipient{ACI: selfACI, PNI: requesterACI}, nil},
		{"ordinary member", signal.Recipient{ACI: aliceACI}, signal.ErrGroupPermission},
		{"offered admin", signal.Recipient{ACI: requesterACI, PNI: bobACI}, signal.ErrNotAMember},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			group := removalGroup()
			group.Pending = []signal.PendingMember{{Recipient: signal.Recipient{PNI: bobACI}, Role: signal.GroupRoleAdmin}}

			_, err := group.WithRemovedMembersAs(test.self, []signal.Recipient{{PNI: bobACI}})
			if !errors.Is(err, test.want) {
				t.Fatalf("permission = %v, want %v", err, test.want)
			}
		})
	}
}

func TestWithRemovedMembersPNITypedRefusals(t *testing.T) {
	t.Parallel()

	for _, ambiguous := range []bool{true, false} {
		t.Run(map[bool]string{true: "ambiguous invitation", false: "ACI requester"}[ambiguous], func(t *testing.T) {
			t.Parallel()

			group := removalGroup()

			group.Pending = nil
			if ambiguous {
				group.Pending = []signal.PendingMember{{Recipient: signal.Recipient{ACI: requesterACI, PNI: requesterACI}}}
			}

			_, err := group.WithRemovedMembers(selfACI, []signal.Recipient{{PNI: requesterACI}})
			if !errors.Is(err, signal.ErrInvalidGroupMember) {
				t.Fatalf("PNI matched non-PNI invitation: %v", err)
			}
		})
	}
}
