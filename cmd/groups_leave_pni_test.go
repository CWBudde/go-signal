package cmd_test

import (
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

const declinePNI = "44444444-4444-4444-8444-444444444444"

func TestGroupsLeavePNI(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := groupsFake()
			fake.Linked[0].PNI = declinePNI
			group := fake.GroupInfo[clubID]
			group.Pending[0].Recipient = signal.Recipient{PNI: declinePNI}
			fake.GroupInfo[clubID] = group

			out, err := run(t, fake, "-o", format, groupsCmd, leaveCmd, clubID, yes)
			if err != nil {
				t.Fatal(err)
			}

			golden(t, "groups_leave_pni_"+format, out)

			if len(fake.Leaves()) != 1 || fake.Leaves()[0].ACI != fake.Linked[0].ACI {
				t.Error("wrong account or leave count")
			}

			_, err = run(t, fake, groupsCmd, leaveCmd, clubID, yes)
			if !errors.Is(err, signal.ErrNotAMember) {
				t.Errorf("repeat decline = %v", err)
			}
		})
	}
}

func TestGroupsLeavePNIOtherAccount(t *testing.T) {
	t.Parallel()

	fake := groupsFake()
	fake.Linked[0].PNI = declinePNI
	group := fake.GroupInfo[clubID]
	group.Pending[0].Recipient = signal.Recipient{PNI: bobACI}
	fake.GroupInfo[clubID] = group

	out, err := run(t, fake, groupsCmd, leaveCmd, clubID, yes)
	if !errors.Is(err, signal.ErrNotAMember) || out != "" || len(fake.Leaves()) != 0 {
		t.Fatalf("foreign PNI decline = %q, %v, leaves %v", out, err, fake.Leaves())
	}
}

func TestGroupsLeavePNISelectedAccount(t *testing.T) {
	t.Parallel()

	fake := groupsFake()
	first := fake.Linked[0]
	first.PNI = bobACI
	second := signal.Account{ACI: carolACI, PNI: declinePNI, Number: "+15550101", DeviceID: 2}
	fake.Linked = []signal.Account{first, second}
	group := fake.GroupInfo[clubID]
	group.Pending[0].Recipient = signal.Recipient{PNI: declinePNI}
	fake.GroupInfo[clubID] = group

	_, err := run(t, fake, "--account", first.ACI, groupsCmd, leaveCmd, clubID, yes)
	if !errors.Is(err, signal.ErrNotAMember) || len(fake.Leaves()) != 0 {
		t.Fatalf("wrong selected account: %v", err)
	}

	out, err := run(t, fake, "--account", second.ACI, groupsCmd, leaveCmd, clubID, yes)
	if err != nil {
		t.Fatal(err)
	}

	golden(t, "groups_leave_pni_plain", out)

	if len(fake.Leaves()) != 1 || fake.Leaves()[0].ACI != second.ACI {
		t.Fatal("declined with wrong account")
	}
}
