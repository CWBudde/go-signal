package cmd_test

import (
	"testing"
)

const (
	memberFlag = "--member"
	createCmd  = "create"
)

func TestGroupsCreateGolden(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		args []string
	}{
		{"groups_create", []string{groupsCmd, createCmd, "Weekend 🐶", memberFlag, aliceNumber, memberFlag, aliceACI}},
		{"groups_create_json", []string{"-o", formatJSON, groupsCmd, createCmd, "Weekend 🐶", memberFlag, aliceNumber}},
		{"groups_create_self", []string{groupsCmd, createCmd, "Private"}},
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

func TestGroupsCreateInvalidInput(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{groupsCmd, createCmd},
		{groupsCmd, createCmd, " "},
		{groupsCmd, createCmd, familyTitle, "extra"},
		{groupsCmd, createCmd, familyTitle, memberFlag, "bad"},
		{groupsCmd, createCmd, familyTitle, memberFlag, "group:" + groupID},
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
