package cmd_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestGroupsPNIReporting(t *testing.T) {
	t.Parallel()

	for _, operation := range []string{listCmd, showCmd} {
		for _, format := range []string{formatPlain, formatJSON} {
			t.Run(operation+"_"+format, func(t *testing.T) {
				t.Parallel()

				fake := groupsFake()
				fake.Linked[0].PNI = declinePNI
				group := fake.GroupInfo[clubID]
				group.Pending[0].Recipient = signal.Recipient{PNI: declinePNI}
				fake.GroupInfo = map[string]signal.Group{clubID: group}
				fake.GroupErrs = nil

				args := []string{"-o", format, groupsCmd, operation}
				if operation == showCmd {
					args = append(args, clubID)
				}

				out, err := run(t, fake, args...)
				if err != nil {
					t.Fatal(err)
				}

				expected := "invited"
				if format == formatJSON {
					expected = `"membership":"pending"`
				} else if operation == showCmd {
					expected = "Our role:              invited"
				}

				if !strings.Contains(out, expected) {
					t.Fatalf("PNI invitation missing from output: %s", out)
				}

				golden(t, "groups_pni_"+operation+"_"+format, out)
			})
		}
	}
}

func TestGroupsJoinPNIRequiresAcceptance(t *testing.T) {
	t.Parallel()
	fake, link := commandJoinFixture(t, signal.GroupJoinMember, false)
	account := fake.Linked[0]
	account.PNI = declinePNI

	fake.Linked[0] = account
	for key, group := range fake.GroupJoinServer {
		group.Pending = []signal.PendingMember{{Recipient: signal.Recipient{PNI: declinePNI}, Role: signal.GroupRoleMember}}
		fake.GroupJoinServer[key] = group
		fake.GroupJoinKnownKeys = map[string]map[string]string{account.ACI: {key: group.ID}}
	}

	out, err := run(t, fake, groupsCmd, joinCmd, link)
	if !errors.Is(err, signal.ErrGroupInvitationRequiresAcceptance) || out != "" {
		t.Fatalf("PNI join = %q, %v", out, err)
	}

	for _, group := range fake.GroupJoinServer {
		if group.Revision != 7 || len(group.Members) != 1 {
			t.Fatal("submitted a join instead of requiring acceptance")
		}
	}

	for _, args := range [][]string{{groupsCmd, listCmd}, {groupsCmd, showCmd, groupID}} {
		out, err := run(t, fake, append([]string{"-o", formatJSON}, args...)...)
		if err != nil || !strings.Contains(out, `"membership":"pending"`) {
			t.Fatalf("retained PNI reporting = %q, %v", out, err)
		}
	}
}

func TestGroupsPNISelectedAccount(t *testing.T) {
	t.Parallel()

	fake := groupsFake()
	first := fake.Linked[0]
	first.PNI = bobACI
	second := signal.Account{ACI: carolACI, PNI: declinePNI, Number: "+15550101", DeviceID: 2}
	fake.Linked = []signal.Account{first, second}
	group := fake.GroupInfo[clubID]
	group.Pending[0].Recipient = signal.Recipient{PNI: declinePNI}

	fake.GroupInfo[clubID] = group
	for _, test := range []struct {
		account    string
		membership string
	}{{first.ACI, "none"}, {second.ACI, "pending"}} {
		out, err := run(t, fake, "--account", test.account, "-o", formatJSON, groupsCmd, showCmd, clubID)
		if err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(out, `"membership":"`+test.membership+`"`) {
			t.Fatalf("account %s: %s", test.account, out)
		}
	}

	_, err := run(t, fake, "--account", second.ACI, groupsCmd, renameCmd, clubID, "Changed")
	if !errors.Is(err, signal.ErrNotAMember) {
		t.Fatalf("PNI invitation authorized rename: %v", err)
	}
}
