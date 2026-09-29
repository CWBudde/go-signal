package cmd_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

const addMembersCmd = "add-members"

func TestGroupsAddMembers(t *testing.T) {
	t.Parallel()

	fake := groupsFake()
	group := fake.GroupInfo[groupID]
	group.Pending = nil
	fake.GroupInfo[groupID] = group

	out, err := run(t, fake, groupsCmd, addMembersCmd, familyTitle, bobACI, bobACI)
	if err != nil {
		t.Fatal(err)
	}

	got := fake.GroupInfo[groupID]

	membership, role := got.MembershipOf(bobACI)
	if membership != signal.MembershipMember || role != signal.GroupRoleMember || got.Revision != 13 {
		t.Fatalf("added = %+v", got)
	}

	golden(t, "groups_add_members", out)
}

func TestGroupsAddMembersPendingGolden(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()
			group := fake.GroupInfo[groupID]
			group.Pending = nil
			fake.GroupInfo[groupID] = group
			fake.GroupInvitees = map[string]bool{bobACI: true}
			fake.GroupInviteTime = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

			out, err := run(t, fake, "-o", format, groupsCmd, addMembersCmd, familyTitle,
				bobACI, daveACI, aliceNumber, app.SelfRecipient)
			if err != nil {
				t.Fatal(err)
			}

			got := fake.GroupInfo[groupID]

			membership, role := got.MembershipOf(daveACI)
			if got.Revision != 13 || len(got.Members) != 4 || len(got.Pending) != 1 || len(got.Requesting) != 0 ||
				membership != signal.MembershipMember || role != signal.GroupRoleMember {
				t.Fatalf("added = %+v", got)
			}

			golden(t, "groups_add_members_pending_"+format, out)
		})
	}
}

func TestGroupsAddMembersInvalidInput(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{groupsCmd, addMembersCmd},
		{groupsCmd, addMembersCmd, familyTitle},
		{groupsCmd, addMembersCmd, " ", bobACI},
		{groupsCmd, addMembersCmd, "group:bad", bobACI},
		{groupsCmd, addMembersCmd, familyTitle, "not-a-recipient"},
		{groupsCmd, addMembersCmd, familyTitle, app.GroupPrefix + groupID},
	} {
		fake := groupsFake()

		_, err := run(t, fake, args...)
		if err == nil || len(fake.Opened()) != 0 {
			t.Errorf("%v: error %v, opened %+v", args, err, fake.Opened())
		}
	}
}

func TestGroupsAddMembersError(t *testing.T) {
	t.Parallel()

	fake := groupsFake()
	fake.AddGroupMembersErr = signal.ErrGroupChanged

	out, err := run(t, fake, groupsCmd, addMembersCmd, familyTitle, daveACI)
	if !errors.Is(err, signal.ErrGroupChanged) || strings.TrimSpace(out) != "" ||
		fake.GroupInfo[groupID].Revision != 12 {
		t.Fatalf("failed addition = %q, %v", out, err)
	}
}
