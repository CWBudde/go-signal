package cmd_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

func TestGroupsRemovePNI(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"PNI:" + declinePNI, aliceNumber} {
		for _, format := range []string{formatPlain, formatJSON} {
			t.Run(target+format, func(t *testing.T) {
				t.Parallel()

				fake := groupsFake()
				fake.Directory[0].PNI = declinePNI
				group := fake.GroupInfo[groupID]
				group.Pending = append(group.Pending, signal.PendingMember{Recipient: signal.Recipient{PNI: declinePNI}})
				fake.GroupInfo[groupID] = group

				out, err := run(t, fake, "-o", format, groupsCmd, removeMembersCmd, familyTitle, target, target)
				if err != nil {
					t.Fatal(err)
				}

				got := fake.GroupInfo[groupID]
				if len(got.Pending) != 1 || got.Pending[0].Recipient.ACI != bobACI || got.Revision != 13 {
					t.Fatalf("PNI invitation remains: %+v", got)
				}

				kind := "explicit"
				if target == aliceNumber {
					kind = "number"

					if len(got.Members) != 2 {
						t.Fatal("resolved ACI member remains")
					}
				} else if len(got.Members) != 3 {
					t.Fatal("explicit PNI removed an ACI member")
				}

				golden(t, "groups_remove_pni_"+kind+"_"+format, out)
			})
		}
	}
}

func TestGroupsRemovePNIFailure(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, target string
		want         error
	}{
		{"malformed", "PNI:bad", app.ErrInvalidRecipient},
		{"zero", "PNI:00000000-0000-0000-0000-000000000000", app.ErrInvalidRecipient},
		{"absent", "PNI:" + bobACI, signal.ErrInvalidGroupMember},
		{"own PNI", "PNI:" + declinePNI, signal.ErrInvalidGroupMember},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()
			fake.Linked[0].PNI = declinePNI
			group := fake.GroupInfo[groupID]
			group.Pending = append(group.Pending, signal.PendingMember{Recipient: signal.Recipient{PNI: declinePNI}})
			fake.GroupInfo[groupID] = group

			out, err := run(t, fake, groupsCmd, removeMembersCmd, familyTitle, aliceACI, test.target)
			if !errors.Is(err, test.want) || strings.TrimSpace(out) != "" {
				t.Fatalf("failure = %q, %v; want %v", out, err, test.want)
			}

			if !reflect.DeepEqual(fake.GroupInfo[groupID], group) {
				t.Fatal("failed batch changed group")
			}

			if errors.Is(test.want, app.ErrInvalidRecipient) && len(fake.Opened()) != 0 {
				t.Fatal("invalid syntax opened account")
			}
		})
	}
}

func TestGroupsRemovePNISelectedAccount(t *testing.T) {
	t.Parallel()

	for _, selectOwn := range []bool{true, false} {
		t.Run(map[bool]string{true: "own", false: "other"}[selectOwn], func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()
			fake.Linked[0].PNI = declinePNI
			other := fake.Linked[0]
			other.ACI, other.PNI, other.Number = carolACI, bobACI, "+15550888"
			fake.Linked = append(fake.Linked, other)
			group := fake.GroupInfo[groupID]
			group.Members[2].Role = signal.GroupRoleAdmin
			group.Pending = append(group.Pending, signal.PendingMember{Recipient: signal.Recipient{PNI: declinePNI}})
			fake.GroupInfo[groupID] = group

			selected := other.ACI
			if selectOwn {
				selected = fake.Linked[0].ACI
			}

			_, err := run(t, fake, "-a", selected, groupsCmd, removeMembersCmd, groupID, "PNI:"+declinePNI)
			if selectOwn {
				if !errors.Is(err, signal.ErrInvalidGroupMember) || !reflect.DeepEqual(fake.GroupInfo[groupID], group) {
					t.Fatalf("selected own PNI = %v, group %+v", err, fake.GroupInfo[groupID])
				}
			} else if err != nil || len(fake.GroupInfo[groupID].Pending) != 1 {
				t.Fatalf("other selected account = %v, group %+v", err, fake.GroupInfo[groupID])
			}
		})
	}
}
