package cmd_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

const removeMembersCmd = "remove-members"

func TestGroupsRemoveMembersGolden(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		args []string
	}{
		{"groups_remove_members", []string{groupsCmd, removeMembersCmd, familyTitle, aliceNumber, aliceACI, bobACI, daveACI}},
		{
			"groups_remove_members_json",
			[]string{"-o", formatJSON, groupsCmd, removeMembersCmd, familyTitle, aliceNumber, bobACI, daveACI},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			out, err := run(t, groupsFake(), test.args...)
			if err != nil {
				t.Fatal(err)
			}

			golden(t, test.name, out)
		})
	}
}

func TestGroupsRemoveMembersInvalidInput(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{groupsCmd, removeMembersCmd},
		{groupsCmd, removeMembersCmd, familyTitle},
		{groupsCmd, removeMembersCmd, " ", aliceACI},
		{groupsCmd, removeMembersCmd, "group:bad", aliceACI},
		{groupsCmd, removeMembersCmd, familyTitle, "bad"},
		{groupsCmd, removeMembersCmd, familyTitle, app.GroupPrefix + groupID},
		{groupsCmd, removeMembersCmd, familyTitle, app.SelfRecipient},
	} {
		fake := groupsFake()

		_, err := run(t, fake, args...)
		if err == nil {
			t.Errorf("%v: expected error", args)
		}

		if len(fake.Opened()) != 0 {
			t.Error("invalid input opened client")
		}
	}
}

func TestGroupsRemoveMembersError(t *testing.T) {
	t.Parallel()

	fake := groupsFake()
	fake.RemoveGroupMembersErr = signal.ErrGroupChanged

	out, err := run(t, fake, groupsCmd, removeMembersCmd, familyTitle, aliceNumber)
	if !errors.Is(err, signal.ErrGroupChanged) || strings.TrimSpace(out) != "" {
		t.Errorf("failed removal = %q, %v", out, err)
	}

	if len(fake.GroupInfo[groupID].Members) != 3 {
		t.Error("failed removal changed members")
	}
}
