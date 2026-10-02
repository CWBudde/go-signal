package cmd_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	banCmd   = "ban"
	unbanCmd = "unban"
)

func TestGroupsBansGolden(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		args      []string
		prebanned bool
	}{
		{"groups_ban", []string{groupsCmd, banCmd, familyTitle, aliceNumber, aliceACI, bobACI, daveACI}, false},
		{"groups_ban_json", []string{"-o", formatJSON, groupsCmd, banCmd, familyTitle, aliceNumber, bobACI, daveACI}, false},
		{"groups_unban", []string{groupsCmd, unbanCmd, familyTitle, aliceNumber}, true},
		{"groups_unban_json", []string{"-o", formatJSON, groupsCmd, unbanCmd, familyTitle, aliceNumber}, true},
		{"groups_ban_noop_json", []string{"-o", formatJSON, groupsCmd, banCmd, familyTitle, aliceNumber, aliceACI}, true},
		{"groups_unban_noop_json", []string{"-o", formatJSON, groupsCmd, unbanCmd, familyTitle, aliceNumber}, false},
		{"groups_show_banned", []string{groupsCmd, showCmd, familyTitle}, true},
		{"groups_show_banned_json", []string{"-o", formatJSON, groupsCmd, showCmd, familyTitle}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()
			bannedAt := time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC)

			fake.GroupBanTime = bannedAt
			if test.prebanned {
				group := fake.GroupInfo[groupID]
				group.Members = append(group.Members[:1:1], group.Members[2:]...)
				group.Banned = []signal.BannedMember{
					{Recipient: signal.Recipient{ACI: aliceACI}, BannedAt: bannedAt.Add(-time.Hour)},
					{Recipient: signal.Recipient{PNI: bobACI}},
				}
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

func TestGroupsBansPreflightBeforeOpen(t *testing.T) {
	t.Parallel()

	for _, verb := range []string{banCmd, unbanCmd} {
		for _, args := range [][]string{
			{groupsCmd, verb},
			{groupsCmd, verb, familyTitle},
			{groupsCmd, verb, " ", aliceACI},
			{groupsCmd, verb, "group:invalid-ban-group", aliceACI},
			{groupsCmd, verb, familyTitle, "invalid-ban-user"},
			{groupsCmd, verb, familyTitle, app.GroupPrefix + groupID},
			{groupsCmd, verb, familyTitle, app.SelfRecipient},
		} {
			fake := groupsFake()

			_, err := run(t, fake, args...)
			if err == nil || len(fake.Opened()) != 0 {
				t.Fatalf("args %v: error %v, opened %v", args, err, fake.Opened())
			}
		}
	}
}

func TestGroupsBansFailureOutput(t *testing.T) { //nolint:cyclop // accepted and atomic failure contracts
	t.Parallel()

	for _, verb := range []string{banCmd, unbanCmd} {
		for _, test := range []struct {
			name                          string
			accepted, uncertain, ordinary bool
			target                        string
			want                          error
		}{
			{name: "ban conflict", target: aliceACI, want: signal.ErrGroupChanged},
			{name: "accepted fetch failure", accepted: true, target: aliceACI, want: signal.ErrUnknownGroup},
			{name: "uncertain", uncertain: true, target: aliceACI, want: signal.ErrGroupUpdateUncertain},
			{name: "unauthorized", ordinary: true, target: aliceACI, want: signal.ErrGroupPermission},
			{name: "selected number", target: testAccount().Number, want: app.ErrInvalidRecipient},
		} {
			t.Run(verb+"/"+test.name, func(t *testing.T) {
				t.Parallel()

				fake := groupsFake()
				if verb == unbanCmd {
					group := fake.GroupInfo[groupID]
					group.Members = append(group.Members[:1:1], group.Members[2:]...)
					group.Banned = []signal.BannedMember{{Recipient: signal.Recipient{ACI: aliceACI}}}
					fake.GroupInfo[groupID] = group
				}

				switch {
				case test.accepted:
					fake.GroupBanFollowUpErr = signal.ErrUnknownGroup
				case test.uncertain:
					fake.SetGroupBannedErr = signal.ErrGroupUpdateUncertain
				case test.ordinary:
					g := fake.GroupInfo[groupID]
					g.Members[0].Role = signal.GroupRoleMember
					fake.GroupInfo[groupID] = g
				case errors.Is(test.want, signal.ErrGroupChanged):
					fake.SetGroupBannedErr = signal.ErrGroupChanged
				}

				before := fake.GroupInfo[groupID]
				before.Members = slices.Clone(before.Members)
				before.Pending = slices.Clone(before.Pending)
				before.Requesting = slices.Clone(before.Requesting)
				before.Banned = slices.Clone(before.Banned)

				out, err := run(t, fake, groupsCmd, verb, familyTitle, aliceNumber, test.target)
				if !errors.Is(err, test.want) || strings.TrimSpace(out) != "" {
					t.Fatalf("output %q, error %v", out, err)
				}

				if test.accepted {
					assertAcceptedBanFailure(t, verb, err, before, fake.GroupInfo[groupID])
				} else if !reflect.DeepEqual(before, fake.GroupInfo[groupID]) ||
					(test.uncertain && strings.Contains(err.Error(), "accepted")) {
					t.Fatalf("failed request mutated state: %v", err)
				}
			})
		}
	}
}

func assertAcceptedBanFailure(t *testing.T, verb string, err error, before, after signal.Group) {
	t.Helper()

	wantBans := 1
	if verb == unbanCmd {
		wantBans = 0
	}

	if len(after.Banned) != wantBans || after.Revision != 13 || reflect.DeepEqual(before, after) ||
		!strings.Contains(err.Error(), "accepted") || !strings.Contains(err.Error(), "inspect groups show") {
		t.Fatalf("accepted failure %v; stored %+v", err, after)
	}
}

func TestGroupsBanAbsentAndUnbanDoesNotReadd(t *testing.T) { //nolint:cyclop // sequential preventive ban and unban
	t.Parallel()

	fake := groupsFake()
	absent := "ffffffff-ffff-4fff-8fff-ffffffffffff"

	out, err := run(t, fake, "-o", formatJSON, groupsCmd, banCmd, familyTitle, absent)
	if err != nil {
		t.Fatal(err)
	}

	var doc struct {
		Version int
		Group   struct {
			Revision uint32
			Banned   []struct{ ACI string }
		}
	}

	err = json.Unmarshal([]byte(out), &doc)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Version != 1 || doc.Group.Revision != 13 || len(doc.Group.Banned) != 1 || doc.Group.Banned[0].ACI != absent {
		t.Fatalf("result %s", out)
	}

	_, err = run(t, fake, groupsCmd, unbanCmd, familyTitle, absent)
	if err != nil {
		t.Fatal(err)
	}

	group := fake.GroupInfo[groupID]
	if group.Revision != 14 || len(group.Banned) != 0 || len(group.Members) != 3 {
		t.Fatalf("unban added membership: %+v", group)
	}
}
