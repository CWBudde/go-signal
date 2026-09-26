package cmd_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	groupsCmd   = "groups"
	leaveCmd    = "leave"
	familyTitle = "Family"
	clubID      = "Y2x1Yi1pZC1jbHViLWlkLWNsdWItaWQtY2x1Yi1pZC0="
	goneID      = "Z29uZS1pZC1nb25lLWlkLWdvbmUtaWQtZ29uZS1pZC0="
	daveACI     = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
)

// groupsFake returns a fake where we are the only admin of "Family" (with alice and carol, bob
// invited and dave asking to join), are invited to "Club", and have left the group goneID.
func groupsFake() *signaltest.Fake {
	own := testAccount().ACI
	invitedAt := time.Date(2026, 9, 20, 12, 30, 0, 0, time.UTC)

	return &signaltest.Fake{
		Linked:    []signal.Account{*testAccount()},
		Directory: []signal.Recipient{{ACI: aliceACI, Number: aliceNumber}},
		GroupInfo: map[string]signal.Group{
			groupID: {
				ID: groupID, Title: familyTitle, Description: "All of us\nand the dog", Revision: 12,
				Timer: 7 * 24 * time.Hour, AnnouncementsOnly: true,
				Members: []signal.GroupMember{
					{Recipient: signal.Recipient{ACI: own}, Role: signal.GroupRoleAdmin},
					{Recipient: signal.Recipient{ACI: aliceACI}, Role: signal.GroupRoleMember, JoinedAtRevision: 2},
					{Recipient: signal.Recipient{ACI: carolACI}, Role: signal.GroupRoleMember, JoinedAtRevision: 5},
				},
				Pending: []signal.PendingMember{{
					Recipient: signal.Recipient{ACI: bobACI}, Role: signal.GroupRoleMember,
					AddedBy: signal.Recipient{ACI: aliceACI}, InvitedAt: invitedAt,
				}},
				Requesting: []signal.RequestingMember{
					{Recipient: signal.Recipient{ACI: daveACI}, RequestedAt: invitedAt.Add(time.Hour)},
				},
			},
			clubID: {
				ID: clubID, Title: "Club", Revision: 3,
				Members: []signal.GroupMember{{Recipient: signal.Recipient{ACI: aliceACI}, Role: signal.GroupRoleAdmin}},
				Pending: []signal.PendingMember{{
					Recipient: signal.Recipient{ACI: own}, Role: signal.GroupRoleMember,
					AddedBy: signal.Recipient{ACI: aliceACI}, InvitedAt: invitedAt,
				}},
			},
		},
		GroupErrs: map[string]error{goneID: signal.ErrNotAMember},
		LeaveTime: time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC),
	}
}

func TestGroupsGolden(t *testing.T) {
	t.Parallel()

	jsonFlag := []string{"-o", string(output.JSON)}
	leaveFamily := []string{groupsCmd, leaveCmd, "family", yes, "--promote", aliceNumber}

	tests := []struct {
		name string
		args []string
	}{
		{"groups_list", []string{groupsCmd, "list"}},
		{"groups_list_json", append(jsonFlag, groupsCmd, "list")},
		{"groups_show", []string{groupsCmd, showCmd, familyTitle}},
		{"groups_show_json", append(jsonFlag, groupsCmd, showCmd, "group:"+groupID)},
		{"groups_show_invited", []string{groupsCmd, showCmd, clubID}},
		{"groups_leave", leaveFamily},
		{"groups_leave_json", append(jsonFlag, leaveFamily...)},
		{"groups_leave_invited", []string{groupsCmd, leaveCmd, "Club", yes}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			out, err := run(t, groupsFake(), test.args...)
			if err != nil {
				t.Fatalf("%v: %v", test.args, err)
			}

			golden(t, test.name, out)
		})
	}
}

func TestGroupsLeaveNeedsConfirmation(t *testing.T) {
	t.Parallel()

	fake := groupsFake()

	_, err := run(t, fake, groupsCmd, leaveCmd, familyTitle)
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("got %v, want the confirmation error", err)
	}

	if len(fake.Opened()) != 0 {
		t.Error("client opened without confirmation")
	}
}

func TestGroupsLeaveLastAdmin(t *testing.T) {
	t.Parallel()

	fake := groupsFake()

	_, err := run(t, fake, groupsCmd, leaveCmd, familyTitle, yes)
	if !errors.Is(err, signal.ErrLastAdmin) || !strings.Contains(err.Error(), "--promote") {
		t.Fatalf("got %v, want ErrLastAdmin with the --promote hint", err)
	}

	if len(fake.Leaves()) != 0 {
		t.Error("left anyway")
	}
}

func TestGroupsErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		args []string
		want error
	}{
		{[]string{groupsCmd, showCmd, "Nobody Here"}, signal.ErrUnknownGroup},
		{[]string{groupsCmd, showCmd, goneID}, signal.ErrNotAMember},
		{[]string{groupsCmd, leaveCmd, goneID, yes}, signal.ErrNotAMember},
	}

	for _, test := range tests {
		_, err := run(t, groupsFake(), test.args...)
		if !errors.Is(err, test.want) {
			t.Errorf("%v: got %v, want %v", test.args, err, test.want)
		}
	}
}
