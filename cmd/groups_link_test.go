package cmd_test

import (
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	linkEnabledState  = "enabled"
	linkDisabledState = "disabled"
	linkApprovalState = "enabled-with-approval"
	linkCmd           = "link"
	linkUpdateCmd     = "update"
	linkStateFlag     = "--state"
	linkResetFlag     = "--reset"
	linkMasterKey     = "bWFzdGVyLWtleS1tYXN0ZXIta2V5LW1hc3Rlci1rZXk="
	linkPassword      = "MDEyMzQ1Njc4OWFiY2RlZg=="
	privateInviteURL  = "https://signal.group/#private-command-link-secret"
)

func linkFake() *signaltest.Fake {
	fake := groupsFake()
	group := fake.GroupInfo[groupID]
	group.MasterKey = linkMasterKey
	fake.GroupInfo[groupID] = group
	fake.GroupKeys = map[string]string{linkMasterKey: groupID}
	fake.GroupLinkStates = map[string]signal.GroupLinkState{groupID: signal.GroupLinkEnabled}
	fake.GroupLinkPasswords = map[string]string{groupID: linkPassword}

	return fake
}

// Catches a missing route and update flags that do not reach the app.
func TestGroupsLinkGolden(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		state signal.GroupLinkState
		args  []string
	}{
		{"groups_link_show_disabled", signal.GroupLinkDisabled, []string{showCmd, familyTitle}},
		{"groups_link_show_enabled", signal.GroupLinkEnabled, []string{showCmd, familyTitle}},
		{"groups_link_show_approval", signal.GroupLinkApproval, []string{showCmd, familyTitle}},
		{
			"groups_link_update_disabled", signal.GroupLinkEnabled,
			[]string{linkUpdateCmd, familyTitle, linkStateFlag, linkDisabledState},
		},
		{
			"groups_link_update_enabled", signal.GroupLinkDisabled,
			[]string{linkUpdateCmd, familyTitle, linkStateFlag, linkEnabledState},
		},
		{
			"groups_link_update_approval", signal.GroupLinkEnabled,
			[]string{linkUpdateCmd, familyTitle, linkStateFlag, linkApprovalState},
		},
		{
			"groups_link_update_noop", signal.GroupLinkEnabled,
			[]string{linkUpdateCmd, familyTitle, linkStateFlag, linkEnabledState},
		},
	} {
		for _, jsonOutput := range []bool{false, true} {
			format := map[bool]string{false: formatPlain, true: formatJSON}[jsonOutput]

			t.Run(test.name+"/"+string(test.state)+"/"+format, func(t *testing.T) {
				t.Parallel()

				fake := linkFake()
				fake.GroupLinkStates[groupID] = test.state
				args := append([]string{groupsCmd, linkCmd}, test.args...)
				name := test.name

				if jsonOutput {
					args = append([]string{"-o", formatJSON}, args...)
					name += "_json"
				}

				out, err := run(t, fake, args...)
				if err != nil {
					t.Fatal(err)
				}

				golden(t, name, out)
			})
		}
	}
}

// Catches empty state/reset requests and secret-bearing arguments reaching account open.
func TestGroupsLinkPreflightBeforeOpen(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{showCmd},
		{showCmd, familyTitle, "extra-link-group"},
		{showCmd, familyTitle, linkStateFlag, linkEnabledState},
		{showCmd, familyTitle, linkResetFlag},
		{showCmd, " "},
		{showCmd, "group:invalid-link-group"},
		{showCmd, privateInviteURL},
		{linkUpdateCmd},
		{linkUpdateCmd, familyTitle},
		{linkUpdateCmd, familyTitle, "extra-link-group", linkResetFlag},
		{linkUpdateCmd, familyTitle, "--state="},
		{linkUpdateCmd, familyTitle, linkStateFlag, "private-invalid-state"},
		{linkUpdateCmd, familyTitle, "--reset=false"},
		{linkUpdateCmd, " ", linkResetFlag},
		{linkUpdateCmd, "group:invalid-link-group", linkResetFlag},
		{linkUpdateCmd, privateInviteURL, linkResetFlag},
	} {
		fake := linkFake()

		out, err := run(t, fake, append([]string{groupsCmd, linkCmd}, args...)...)
		if err == nil || len(fake.Opened()) != 0 || strings.TrimSpace(out) != "" {
			t.Fatalf("args %v: out %q, error %v, opened %v", args, out, err, fake.Opened())
		}

		if strings.Contains(err.Error(), privateInviteURL) || strings.Contains(err.Error(), "private-invalid-state") {
			t.Fatalf("secret input echoed: %v", err)
		}
	}
}

// Catches generic commands exposing links/passwords and unknown keys leaking in errors.
func TestGroupsLinkPrivacy(t *testing.T) {
	t.Parallel()

	for _, verb := range []string{showCmd, "list"} {
		fake := linkFake()

		args := []string{"-o", formatJSON, groupsCmd, verb}
		if verb == showCmd {
			args = append(args, familyTitle)
		}

		out, err := run(t, fake, args...)
		if err != nil {
			t.Fatal(err)
		}

		if strings.Contains(out, "signal.group") || strings.Contains(out, linkMasterKey) ||
			strings.Contains(out, linkPassword) {
			t.Fatalf("generic output exposed link: %s", out)
		}
	}
}

func TestGroupsLinkPrivateUnknownKeys(t *testing.T) {
	t.Parallel()

	unknownKey := strings.Repeat("A", 43) + "="
	for _, verb := range []string{showCmd, linkUpdateCmd} {
		args := []string{groupsCmd, linkCmd, verb, unknownKey}
		if verb == linkUpdateCmd {
			args = append(args, linkResetFlag)
		}

		out, err := run(t, linkFake(), args...)
		if !errors.Is(err, signal.ErrUnknownGroup) || strings.Contains(err.Error(), unknownKey) ||
			strings.TrimSpace(out) != "" {
			t.Fatalf("unknown key output %q, error %v", out, err)
		}
	}
}

// Catches missing reset/state combination forwarding, stale output and implicit membership changes.
func TestGroupsLinkReset(t *testing.T) { //nolint:cyclop // reset preserves membership and handles both states
	t.Parallel()

	for _, disabled := range []bool{false, true} {
		fake := linkFake()
		originalPassword := fake.GroupLinkPasswords[groupID]

		args := []string{"-o", formatJSON, groupsCmd, linkCmd, linkUpdateCmd, familyTitle, linkResetFlag}
		if disabled {
			args = append(args, linkStateFlag, linkDisabledState)
		}

		out, err := run(t, fake, args...)
		if err != nil {
			t.Fatal(err)
		}

		var doc struct {
			Version   int
			GroupLink signal.GroupLink
		}

		err = json.Unmarshal([]byte(out), &doc)
		if err != nil {
			t.Fatal(err)
		}

		if doc.Version != 1 || doc.GroupLink.ID != groupID || doc.GroupLink.Revision != 13 ||
			fake.GroupLinkPasswords[groupID] == originalPassword || len(fake.GroupInfo[groupID].Members) != 3 {
			t.Fatalf("reset result: %s", out)
		}

		if disabled {
			if doc.GroupLink.State != signal.GroupLinkDisabled || doc.GroupLink.URL != "" {
				t.Fatalf("disable/reset exposed URL: %s", out)
			}
		} else if doc.GroupLink.State != signal.GroupLinkEnabled ||
			!strings.HasPrefix(doc.GroupLink.URL, "https://signal.group/#") {
			t.Fatalf("reset result: %s", out)
		}
	}
}

// Catches success output on rejected, uncertain, or accepted-with-fetch-error updates.
func TestGroupsLinkFailureOutput(t *testing.T) { //nolint:cyclop // accepted and atomic failure contracts
	t.Parallel()

	for _, test := range []struct {
		name     string
		setup    func(*signaltest.Fake)
		want     error
		accepted bool
	}{
		{
			"link patch conflict", func(f *signaltest.Fake) { f.UpdateGroupLinkErr = signal.ErrGroupChanged },
			signal.ErrGroupChanged, false,
		},
		{
			"link uncertain update", func(f *signaltest.Fake) { f.UpdateGroupLinkErr = signal.ErrGroupUpdateUncertain },
			signal.ErrGroupUpdateUncertain, false,
		},
		{
			"link accepted fetch", func(f *signaltest.Fake) { f.GroupLinkFollowUpErr = signal.ErrUnknownGroup },
			signal.ErrUnknownGroup, true,
		},
		{"link unprivileged write", func(f *signaltest.Fake) {
			group := f.GroupInfo[groupID]
			group.Members[0].Role = signal.GroupRoleMember
			f.GroupInfo[groupID] = group
		}, signal.ErrGroupPermission, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := linkFake()
			test.setup(fake)
			before := fake.GroupInfo[groupID]
			before.Members = slices.Clone(before.Members)
			before.Pending = slices.Clone(before.Pending)
			before.Requesting = slices.Clone(before.Requesting)
			before.Banned = slices.Clone(before.Banned)
			passwords, states := maps.Clone(fake.GroupLinkPasswords), maps.Clone(fake.GroupLinkStates)

			out, err := run(t, fake, groupsCmd, linkCmd, linkUpdateCmd, familyTitle, linkStateFlag, linkDisabledState)
			if !errors.Is(err, test.want) || strings.TrimSpace(out) != "" ||
				strings.Contains(err.Error(), "accepted") != test.accepted {
				t.Fatalf("output %q, error %v", out, err)
			}

			if test.accepted {
				if fake.GroupInfo[groupID].Revision != 13 || fake.GroupLinkStates[groupID] != signal.GroupLinkDisabled ||
					!strings.Contains(err.Error(), "inspect groups link show "+groupID) {
					t.Fatalf("accepted result group %+v, error %v", fake.GroupInfo[groupID], err)
				}
			} else if !reflect.DeepEqual(before, fake.GroupInfo[groupID]) ||
				!maps.Equal(passwords, fake.GroupLinkPasswords) || !maps.Equal(states, fake.GroupLinkStates) {
				t.Fatalf("failure mutated group: %+v", fake.GroupInfo[groupID])
			}
		})
	}
}
