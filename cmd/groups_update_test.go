package cmd_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	groupDescriptionFlag             = "--description"
	groupTimerFlag                   = "--timer"
	groupTimerErrorLabel             = "timer"
	groupPermissionMembers           = "members"
	groupAnnouncementFalse           = "--announcements-only=false"
	groupSettingsNewDescription      = "Updated description"
	groupSettingsOriginalDescription = "All of us\nand the dog"
)

func TestGroupsUpdateInvalidInput(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"no settings", []string{familyTitle}, "at least one"},
		{"empty group", []string{"", groupDescriptionFlag, groupSettingsNewDescription}, "empty group"},
		{"malformed group", []string{"group:broken", groupDescriptionFlag, groupSettingsNewDescription}, "group"},
		{"negative timer", []string{familyTitle, groupTimerFlag, "-1"}, groupTimerErrorLabel},
		{"fractional timer", []string{familyTitle, groupTimerFlag, "1.5"}, groupTimerErrorLabel},
		{"overflow timer", []string{familyTitle, groupTimerFlag, "4294967296"}, groupTimerErrorLabel},
		{"invalid edit permission", []string{familyTitle, "--edit-permission", "everyone"}, groupPermissionMembers},
		{"empty add permission", []string{familyTitle, "--add-member-permission="}, groupPermissionMembers},
		{"invalid UTF-8", []string{familyTitle, groupDescriptionFlag, "\xff"}, "UTF-8"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()

			out, err := run(t, fake, append([]string{groupsCmd, updateCmd}, test.args...)...)
			if err == nil || !strings.Contains(err.Error(), test.want) || out != "" {
				t.Fatalf("output/error = %q / %v, want %q", out, err, test.want)
			}

			if len(fake.Opened()) != 0 {
				t.Error("invalid request opened the account")
			}
		})
	}
}

func TestGroupsUpdateCommandsGolden(t *testing.T) {
	t.Parallel()

	combined := []string{
		groupDescriptionFlag, "Weekend plans", groupTimerFlag, "86400", groupAnnouncementFalse,
		"--edit-permission", groupPermissionMembers, "--add-member-permission", groupPermissionMembers,
	}
	for _, test := range []struct {
		name string
		args []string
	}{
		{"groups_update", append([]string{groupsCmd, updateCmd, familyTitle}, combined...)},
		{"groups_update_json", append([]string{"-o", formatJSON, groupsCmd, updateCmd, familyTitle}, combined...)},
		{
			"groups_update_clear_json",
			[]string{
				"-o", formatJSON, groupsCmd, updateCmd, familyTitle,
				"--description=", groupTimerFlag, "0", groupAnnouncementFalse,
			},
		},
		{
			"groups_update_unchanged_json",
			[]string{"-o", formatJSON, groupsCmd, updateCmd, familyTitle, "--announcements-only"},
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

func TestGroupsUpdatePreservesOmittedSettings(t *testing.T) { //nolint:cyclop // supplied and preserved fields
	const description = "  New\n世界  " //nolint:gosmopolitan // Unicode description fixture

	t.Parallel()

	for _, test := range []struct {
		name string
		args []string
		want signal.Group
	}{
		{
			"description only",
			[]string{groupDescriptionFlag, description},
			signal.Group{Description: description, Timer: 7 * 24 * time.Hour, AnnouncementsOnly: true},
		},
		{"empty description", []string{"--description="}, signal.Group{Timer: 7 * 24 * time.Hour, AnnouncementsOnly: true}},
		{
			"zero timer",
			[]string{groupTimerFlag, "0"},
			signal.Group{Description: groupSettingsOriginalDescription, AnnouncementsOnly: true},
		},
		{
			"explicit false",
			[]string{groupAnnouncementFalse},
			signal.Group{Description: groupSettingsOriginalDescription, Timer: 7 * 24 * time.Hour},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()
			initial := fake.GroupInfo[groupID]

			out, err := run(t, fake, append([]string{groupsCmd, updateCmd, familyTitle}, test.args...)...)
			if err != nil || out == "" {
				t.Fatalf("output/error = %q / %v", out, err)
			}

			got := fake.GroupInfo[groupID]
			if got.Description != test.want.Description || got.Timer != test.want.Timer ||
				got.AnnouncementsOnly != test.want.AnnouncementsOnly || got.MembersCanEditAttributes ||
				got.MembersCanAddMembers || got.Title != initial.Title || got.Revision != initial.Revision+1 ||
				!reflect.DeepEqual(got.Members, initial.Members) {
				t.Errorf("group = %+v, want settings %+v and unchanged members/title", got, test.want)
			}
		})
	}
}

func TestGroupsUpdateErrorsHaveNoSuccessOutput(t *testing.T) {
	t.Parallel()

	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "conflict", true: "accepted follow-up"}[accepted], func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()
			want := signal.ErrGroupChanged

			fake.UpdateGroupErr = want
			if accepted {
				want = errGroupUpdateFollowUp
				fake.UpdateGroupErr = nil
				fake.GroupUpdateFollowUpErr = want
			}

			out, err := run(t, fake, groupsCmd, updateCmd, familyTitle, groupDescriptionFlag, groupSettingsNewDescription)
			if !errors.Is(err, want) || out != "" {
				t.Fatalf("output/error = %q / %v", out, err)
			}

			if accepted && !strings.Contains(err.Error(), "groups show") {
				t.Errorf("missing accepted-change inspection hint: %v", err)
			}
		})
	}
}

var errGroupUpdateFollowUp = errors.New("group verification failed")

func TestGroupsUpdateUncertainErrorDoesNotClaimAcceptance(t *testing.T) {
	t.Parallel()

	fake := groupsFake()
	fake.UpdateGroupErr = signal.ErrGroupUpdateUncertain

	out, err := run(t, fake, groupsCmd, updateCmd, familyTitle, groupDescriptionFlag, groupSettingsNewDescription)
	if !errors.Is(err, signal.ErrGroupUpdateUncertain) || out != "" || strings.Contains(err.Error(), "accepted") {
		t.Fatalf("uncertain output/error = %q / %v", out, err)
	}

	if fake.GroupInfo[groupID].Revision != 12 {
		t.Error("uncertain test transport unexpectedly changed server state")
	}
}

func TestGroupsUpdateMixedPermissionsAreAtomic(t *testing.T) {
	t.Parallel()

	fake := groupsFake()
	initial := fake.GroupInfo[groupID]
	initial.Members[0].Role = signal.GroupRoleMember
	initial.MembersCanEditAttributes = true
	fake.GroupInfo[groupID] = initial

	out, err := run(t, fake, groupsCmd, updateCmd, familyTitle,
		groupDescriptionFlag, groupSettingsNewDescription, "--add-member-permission", "admins")
	if !errors.Is(err, signal.ErrGroupPermission) || out != "" {
		t.Fatalf("output/error = %q / %v", out, err)
	}

	if !reflect.DeepEqual(fake.GroupInfo[groupID], initial) {
		t.Error("mixed forbidden request partially changed the group")
	}
}
