package cmd_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	promoteCmd = "promote"
	demoteCmd  = "demote"
)

func TestGroupsRolesGolden(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		args []string
	}{
		{"groups_promote", []string{groupsCmd, promoteCmd, familyTitle, aliceNumber, aliceACI, carolACI}},
		{"groups_promote_json", []string{"-o", formatJSON, groupsCmd, promoteCmd, familyTitle, aliceNumber}},
		{"groups_demote_self", []string{groupsCmd, demoteCmd, familyTitle, app.SelfRecipient}},
		{"groups_demote_self_json", []string{"-o", formatJSON, groupsCmd, demoteCmd, familyTitle, app.SelfRecipient}},
		{"groups_roles_noop_json", []string{"-o", formatJSON, groupsCmd, promoteCmd, familyTitle, app.SelfRecipient}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()
			if strings.Contains(test.name, demoteCmd) {
				group := fake.GroupInfo[groupID]
				group.Members[1].Role = signal.GroupRoleAdmin
				fake.GroupInfo[groupID] = group
			}

			out, err := run(t, fake, test.args...)
			if err != nil {
				t.Fatal(err)
			}

			golden(t, test.name, out)
		})
	}
}

func TestGroupsRolesInvalidInputBeforeOpen(t *testing.T) {
	t.Parallel()

	for _, verb := range []string{promoteCmd, demoteCmd} {
		for _, args := range [][]string{
			{groupsCmd, verb},
			{groupsCmd, verb, familyTitle},
			{groupsCmd, verb, " ", aliceACI},
			{groupsCmd, verb, "group:invalid-role-group", aliceACI},
			{groupsCmd, verb, familyTitle, "invalid-role-member"},
			{groupsCmd, verb, familyTitle, app.GroupPrefix + groupID},
		} {
			fake := groupsFake()

			_, err := run(t, fake, args...)
			if err == nil || len(fake.Opened()) != 0 {
				t.Fatalf("%v: error = %v; opened %v", args, err, fake.Opened())
			}
		}
	}
}

func TestGroupsRolesErrorsHaveNoSuccessOutput(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, verb string
		members    []string
		want       error
	}{
		{"last admin", demoteCmd, []string{app.SelfRecipient}, signal.ErrLastAdmin},
		{"invited target", promoteCmd, []string{aliceACI, bobACI}, signal.ErrInvalidGroupMember},
		{"requesting target", promoteCmd, []string{aliceACI, daveACI}, signal.ErrInvalidGroupMember},
		{"role conflict", promoteCmd, []string{aliceACI}, signal.ErrGroupChanged},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()
			if test.name == "role conflict" {
				fake.SetGroupMemberRoleErr = signal.ErrGroupChanged
			}

			before := fake.GroupInfo[groupID]
			before.Members = slices.Clone(before.Members)

			args := append([]string{groupsCmd, test.verb, familyTitle}, test.members...)

			out, err := run(t, fake, args...)
			if !errors.Is(err, test.want) || strings.TrimSpace(out) != "" ||
				!reflect.DeepEqual(fake.GroupInfo[groupID], before) {
				t.Fatalf("failure = %q, %v; group changed %v", out, err, !reflect.DeepEqual(fake.GroupInfo[groupID], before))
			}
		})
	}
}

func TestGroupsRolesAcceptedFailure(t *testing.T) {
	t.Parallel()

	fake := groupsFake()
	fake.GroupRoleFollowUpErr = signal.ErrUnknownGroup

	out, err := run(t, fake, groupsCmd, promoteCmd, familyTitle, aliceACI)
	if !errors.Is(err, signal.ErrUnknownGroup) || strings.TrimSpace(out) != "" ||
		!strings.Contains(err.Error(), "accepted") || !strings.Contains(err.Error(), "inspect groups show") ||
		fake.GroupInfo[groupID].Members[1].Role != signal.GroupRoleAdmin {
		t.Fatalf("accepted failure = %q, %v; stored %+v", out, err, fake.GroupInfo[groupID])
	}
}
