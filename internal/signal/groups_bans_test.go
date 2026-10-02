package signal_test

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestWithBannedMembers(t *testing.T) { //nolint:cyclop,gocognit,funlen // policy matrix
	t.Parallel()

	for _, test := range []struct {
		name, self string
		targets    []signal.Recipient
		banned     bool
		want       error
	}{
		{"full ban", selfACI, []signal.Recipient{{ACI: aliceACI}}, true, nil},
		{"pending ban", selfACI, []signal.Recipient{{ACI: bobACI}}, true, nil},
		{"requesting ban", selfACI, []signal.Recipient{{ACI: requesterACI}}, true, nil},
		{"preventive ban", selfACI, []signal.Recipient{{ACI: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}}, true, nil},
		{"duplicate ban", selfACI, []signal.Recipient{{ACI: strings.ToUpper(aliceACI)}, {ACI: aliceACI}}, true, nil},
		{"absent unban", selfACI, []signal.Recipient{{ACI: aliceACI}}, false, nil},
		{"member no-op", aliceACI, []signal.Recipient{{ACI: bobACI}}, false, signal.ErrGroupPermission},
		{"pending actor", bobACI, []signal.Recipient{{ACI: aliceACI}}, true, signal.ErrNotAMember},
		{"requesting actor", requesterACI, []signal.Recipient{{ACI: aliceACI}}, true, signal.ErrNotAMember},
		{"absent actor", "unknown", []signal.Recipient{{ACI: aliceACI}}, false, signal.ErrNotAMember},
		{"ban self", selfACI, []signal.Recipient{{ACI: aliceACI}, {ACI: selfACI}}, true, signal.ErrInvalidGroupMember},
		{"unban self", selfACI, []signal.Recipient{{ACI: selfACI}}, false, signal.ErrInvalidGroupMember},
		{"empty bans", selfACI, nil, true, signal.ErrInvalidGroupMember},
		{
			"invalid bans", selfACI,
			[]signal.Recipient{{ACI: aliceACI}, {ACI: "invalid-ban-target"}},
			true, signal.ErrUnresolvable,
		},
		{"PNI", selfACI, []signal.Recipient{{PNI: bobACI}}, false, signal.ErrUnresolvable},
		{"zero", selfACI, []signal.Recipient{{ACI: nilACITestValue}}, true, signal.ErrUnresolvable},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			group := removalGroup()
			before := removalGroup()
			targets := slices.Clone(test.targets)
			bannedAt := time.UnixMilli(1720000000123).UTC()

			got, err := group.WithBannedMembers(test.self, targets, test.banned, bannedAt)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %+v,%v want %v", got, err, test.want)
			}

			if !reflect.DeepEqual(group, before) || !reflect.DeepEqual(targets, test.targets) {
				t.Fatal("input changed")
			}

			if err != nil {
				return
			}

			wantRevision := group.Revision
			if test.banned {
				wantRevision++

				if len(got.Banned) != 1 || !got.Banned[0].BannedAt.Equal(bannedAt) {
					t.Fatalf("bans %+v", got.Banned)
				}

				membership, _ := got.MembershipOf(strings.ToLower(test.targets[0].ACI))
				if membership != signal.MembershipNone {
					t.Fatalf("membership %v", membership)
				}
			}

			if got.Revision != wantRevision {
				t.Fatalf("revision %d", got.Revision)
			}

			got.Members[0].Role = signal.GroupRoleMember
			if len(got.Pending) > 0 {
				got.Pending[0].Recipient.ACI = "changed-ban-policy-ACI"
			}

			if len(got.Requesting) > 0 {
				got.Requesting[0].Recipient.ACI = "changed-ban-policy-ACI"
			}

			if !reflect.DeepEqual(group, before) {
				t.Fatal("slices alias")
			}
		})
	}
}

func TestWithBannedMembersPreservesAndOwnsBans(t *testing.T) { //nolint:cyclop // preservation and ownership
	t.Parallel()

	group := removalGroup()
	old := time.UnixMilli(1000).UTC()
	group.Banned = []signal.BannedMember{
		{Recipient: signal.Recipient{ACI: aliceACI}, BannedAt: old},
		{Recipient: signal.Recipient{PNI: aliceACI}, BannedAt: old},
	}

	bannedAt := time.UnixMilli(2000).UTC()

	got, err := group.WithBannedMembers(selfACI, []signal.Recipient{{ACI: aliceACI}}, true, bannedAt)
	if err != nil || got.Revision != 10 || len(got.Banned) != 2 || !got.Banned[0].BannedAt.Equal(old) {
		t.Fatalf("inconsistent ban %+v,%v", got, err)
	}

	got, err = got.WithBannedMembers(selfACI, []signal.Recipient{{ACI: aliceACI}}, true, bannedAt)
	if err != nil || got.Revision != 10 {
		t.Fatalf("repeat %+v,%v", got, err)
	}

	got.Banned[0].BannedAt = bannedAt
	if !group.Banned[0].BannedAt.Equal(old) {
		t.Fatal("ban aliases")
	}

	unbanned, err := group.WithBannedMembers(selfACI, []signal.Recipient{{ACI: aliceACI}}, false, bannedAt)
	if err != nil || unbanned.Revision != 10 || len(unbanned.Banned) != 1 ||
		unbanned.Banned[0].Recipient.PNI != aliceACI || !reflect.DeepEqual(unbanned.Members, group.Members) {
		t.Fatalf("unban %+v,%v", unbanned, err)
	}

	noop, err := group.WithBannedMembers(selfACI, []signal.Recipient{{ACI: bobACI}}, false, bannedAt)
	if err != nil || noop.Revision != group.Revision {
		t.Fatalf("noop %+v,%v", noop, err)
	}

	noop.Banned[0].BannedAt = bannedAt
	if !group.Banned[0].BannedAt.Equal(old) {
		t.Fatal("noop aliases")
	}

	group.Revision = math.MaxUint32
	_, err = group.WithBannedMembers(selfACI, []signal.Recipient{{ACI: aliceACI}}, false, bannedAt)

	if !errors.Is(err, signal.ErrUnknownGroup) {
		t.Fatalf("overflow %v", err)
	}

	_, err = group.WithBannedMembers(selfACI, []signal.Recipient{{ACI: bobACI}}, false, bannedAt)
	if err != nil {
		t.Fatal(err)
	}
}

func TestWithBannedMembersKeepsActingAdmin(t *testing.T) {
	t.Parallel()

	group := removalGroup()
	group.Members[1].Role = signal.GroupRoleAdmin

	got, err := group.WithBannedMembers(selfACI, []signal.Recipient{
		{ACI: aliceACI}, {ACI: bobACI}, {ACI: requesterACI},
	}, true, time.UnixMilli(1000))
	if err != nil || len(got.Members) != 1 || len(got.Pending) != 0 || len(got.Requesting) != 0 ||
		len(got.Banned) != 3 || got.Admins() != 1 || got.Role != signal.GroupRoleAdmin || got.Revision != 10 {
		t.Fatalf("acting admin lost: %+v,%v", got, err)
	}
}
