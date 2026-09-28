package signal_test

import (
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

const renameTitle = "New title"

func TestCheckRename(t *testing.T) {
	t.Parallel()

	group := signal.Group{
		Members:    []signal.GroupMember{member(selfACI, signal.GroupRoleAdmin), member(aliceACI, signal.GroupRoleMember)},
		Pending:    []signal.PendingMember{{Recipient: signal.Recipient{ACI: bobACI}, Role: signal.GroupRoleAdmin}},
		Requesting: []signal.RequestingMember{{Recipient: signal.Recipient{ACI: "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"}}},
	}

	for _, test := range []struct {
		name           string
		aci            string
		title          string
		membersCanEdit bool
		want           error
	}{
		{"admin", selfACI, renameTitle, false, nil},
		{"unicode", selfACI, "Familie 👨‍👩‍👧‍👦", false, nil},
		{"member allowed", aliceACI, renameTitle, true, nil},
		{"member denied", aliceACI, renameTitle, false, signal.ErrGroupPermission},
		{"invited admin", bobACI, renameTitle, true, signal.ErrNotAMember},
		{"requester", "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", renameTitle, true, signal.ErrNotAMember},
		{"absent", "absent", renameTitle, true, signal.ErrNotAMember},
		{"blank", selfACI, " \n\t", false, signal.ErrInvalidGroupTitle},
		{"empty", selfACI, "", false, signal.ErrInvalidGroupTitle},
		{"invalid UTF-8", selfACI, "\xff", false, signal.ErrInvalidGroupTitle},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			copyGroup := group

			copyGroup.MembersCanEditAttributes = test.membersCanEdit

			err := copyGroup.CheckRename(test.aci, test.title)
			if !errors.Is(err, test.want) {
				t.Errorf("CheckRename = %v, want %v", err, test.want)
			}
		})
	}
}
