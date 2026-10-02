//nolint:goconst // literal expectations are independent fixtures
package signal_test

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestGroupUpdateCheck(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		update  signal.GroupUpdate
		invalid bool
	}{
		{"empty settings update", signal.GroupUpdate{}, true},
		{"invalid UTF8", signal.GroupUpdate{Description: new("\xff")}, true},
		{"clear", signal.GroupUpdate{Description: new("")}, false},
		{"timer off", signal.GroupUpdate{TimerSeconds: new(uint32(0))}, false},
		{"explicit false", signal.GroupUpdate{AnnouncementsOnly: new(false)}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.update.Check()
			if errors.Is(err, signal.ErrInvalidGroupUpdate) != test.invalid {
				t.Fatalf("Check = %v", err)
			}
		})
	}
}

func TestGroupUpdateOriginalPermissions(t *testing.T) { //nolint:funlen // permission matrix
	t.Parallel()

	for _, test := range []struct {
		name   string
		role   signal.GroupRole
		access bool
		update signal.GroupUpdate
		want   error
	}{
		{"member description", signal.GroupRoleMember, true, signal.GroupUpdate{Description: new("new")}, nil},
		{"member timer", signal.GroupRoleMember, true, signal.GroupUpdate{TimerSeconds: new(uint32(9))}, nil},
		{
			"member denied attributes",
			signal.GroupRoleMember,
			false,
			signal.GroupUpdate{Description: new("new")},
			signal.ErrGroupPermission,
		},

		{
			"unknown access",
			signal.GroupRoleUnknown,
			false,
			signal.GroupUpdate{TimerSeconds: new(uint32(9))},
			signal.ErrGroupPermission,
		},

		{
			"member unchanged announcement",
			signal.GroupRoleMember,
			true,
			signal.GroupUpdate{
				Description:       new("new"),
				AnnouncementsOnly: new(false),
			},
			signal.ErrGroupPermission,
		},

		{
			"member unchanged edit permission",
			signal.GroupRoleMember,
			true,
			signal.GroupUpdate{
				Description:              new("new"),
				MembersCanEditAttributes: new(true),
			},
			signal.ErrGroupPermission,
		},

		{
			"member unchanged add permission",
			signal.GroupRoleMember,
			true,
			signal.GroupUpdate{
				Description:          new("new"),
				MembersCanAddMembers: new(false),
			},
			signal.ErrGroupPermission,
		},

		{
			"member cannot grant own access",
			signal.GroupRoleMember,
			false,
			signal.GroupUpdate{
				Description:              new("new"),
				MembersCanEditAttributes: new(true),
			},
			signal.ErrGroupPermission,
		},

		{
			"admin combined",
			signal.GroupRoleAdmin,
			false,
			signal.GroupUpdate{
				Description:              new("new"),
				TimerSeconds:             new(uint32(9)),
				AnnouncementsOnly:        new(true),
				MembersCanEditAttributes: new(true),
				MembersCanAddMembers:     new(true),
			},
			nil,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			group := signal.Group{
				Members:                  []signal.GroupMember{member(selfACI, test.role)},
				MembersCanEditAttributes: test.access,
			}

			_, err := group.WithUpdate(selfACI, test.update)
			if !errors.Is(err, test.want) {
				t.Fatalf("WithUpdate = %v", err)
			}
		})
	}

	for _, group := range []signal.Group{
		{},
		{Pending: []signal.PendingMember{{
			Recipient: signal.Recipient{ACI: selfACI},
			Role:      signal.GroupRoleAdmin,
		}}},
		{Requesting: []signal.RequestingMember{{Recipient: signal.Recipient{ACI: selfACI}}}},
	} {
		_, err := group.WithUpdate(selfACI, signal.GroupUpdate{Description: new("new")})
		if !errors.Is(err, signal.ErrNotAMember) {
			t.Fatalf("nonmember = %v", err)
		}
	}
}

func TestGroupWithUpdatePreservesAndIncrementsOnce(t *testing.T) { //nolint:cyclop // boundaries
	t.Parallel()

	group := signal.Group{
		ID:                       "group",
		MasterKey:                "secret",
		Title:                    "title",
		Description:              "old",
		Revision:                 7,
		Timer:                    time.Minute,
		AnnouncementsOnly:        true,
		MembersCanEditAttributes: true,
		MembersCanAddMembers:     true,
		Members:                  []signal.GroupMember{member(selfACI, signal.GroupRoleAdmin)},
	}
	got, err := group.WithUpdate(selfACI, signal.GroupUpdate{
		Description:              new(""),
		TimerSeconds:             new(uint32(0)),
		AnnouncementsOnly:        new(false),
		MembersCanEditAttributes: new(false),
		MembersCanAddMembers:     new(false),
	})
	want := group
	want.Description = ""
	want.Timer = 0
	want.AnnouncementsOnly = false
	want.MembersCanEditAttributes = false
	want.MembersCanAddMembers = false

	want.Revision = 8
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("update = %+v, %v", got, err)
	}

	if group.Description != "old" || group.Revision != 7 {
		t.Fatal("input mutated")
	}

	got, err = group.WithUpdate(selfACI, signal.GroupUpdate{Description: new("old")})
	if err != nil || !reflect.DeepEqual(got, group) {
		t.Fatalf("noop = %+v, %v", got, err)
	}

	got, err = group.WithUpdate(selfACI, signal.GroupUpdate{TimerSeconds: new(uint32(math.MaxUint32))})
	if err != nil || got.Timer != time.Duration(math.MaxUint32)*time.Second || got.Description != "old" {
		t.Fatalf("max timer = %+v, %v", got, err)
	}

	group.Revision = math.MaxUint32

	_, err = group.WithUpdate(selfACI, signal.GroupUpdate{Description: new("new")})
	if !errors.Is(err, signal.ErrUnknownGroup) {
		t.Fatalf("overflow = %v", err)
	}

	got, err = group.WithUpdate(selfACI, signal.GroupUpdate{Description: new("old")})
	if err != nil || got.Revision != math.MaxUint32 {
		t.Fatalf("overflow noop = %+v, %v", got, err)
	}
}
