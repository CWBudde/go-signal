package signaltest_test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

const roleFakeGroupID = "group"

func TestFakeMemberRoleAtomicAndAccountAware(t *testing.T) { //nolint:cyclop // sequential state transitions
	t.Parallel()

	fake := settingsFake()
	cli := profileClient(t, fake, profileAliceACI, true)
	targets := []signal.Recipient{{ACI: profileBobACI}, {ACI: strings.ToUpper(profileBobACI)}}

	got, err := cli.SetGroupMemberRole(t.Context(), "key", targets, signal.GroupRoleAdmin)
	if err != nil ||
		got.Revision != 8 ||
		got.Role != signal.GroupRoleAdmin ||
		got.Members[1].Role != signal.GroupRoleAdmin {
		t.Fatalf("promote = %+v,%v", got, err)
	}

	if targets[1].ACI != strings.ToUpper(profileBobACI) {
		t.Fatal("mutated targets")
	}

	got.Members[1].Role = signal.GroupRoleMember
	if fake.GroupInfo[roleFakeGroupID].Members[1].Role != signal.GroupRoleAdmin {
		t.Fatal("result aliases fake state")
	}

	fake.SetGroupMemberRoleErr = signal.ErrGroupChanged

	got, err = cli.SetGroupMemberRole(t.Context(), roleFakeGroupID, targets, signal.GroupRoleAdmin)
	if err != nil || got.Revision != 8 {
		t.Fatalf("noop = %+v,%v", got, err)
	}

	_, err = cli.SetGroupMemberRole(t.Context(), roleFakeGroupID, targets, signal.GroupRoleMember)
	if !errors.Is(err, signal.ErrGroupChanged) || fake.GroupInfo[roleFakeGroupID].Revision != 8 {
		t.Fatalf("rejection = %v", err)
	}

	fake.SetGroupMemberRoleErr = nil
	fake.GroupRoleFollowUpErr = io.ErrClosedPipe

	got, err = cli.SetGroupMemberRole(t.Context(), roleFakeGroupID, []signal.Recipient{{ACI: profileAliceACI}},
		signal.GroupRoleMember)
	if !errors.Is(err, io.ErrClosedPipe) ||
		got.ID != roleFakeGroupID ||
		got.Revision != 9 ||
		got.MasterKey != "" ||
		!strings.Contains(err.Error(), "inspect groups show before retrying") ||
		fake.GroupInfo[roleFakeGroupID].Members[0].Role != signal.GroupRoleMember {
		t.Fatalf("accepted failure = %+v,%v", got, err)
	}

	fake.GroupRoleFollowUpErr = nil

	_, err = cli.SetGroupMemberRole(t.Context(), roleFakeGroupID, targets, signal.GroupRoleMember)
	if !errors.Is(err, signal.ErrGroupPermission) {
		t.Fatalf("demoted actor = %v", err)
	}
}

func TestFakeMemberRoleLifecycle(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name                                   string
		connected, closed, cancelled, unlinked bool
		want                                   error
	}{
		{name: "role operation disconnected", want: signal.ErrNotConnected},
		{name: "role operation closed", connected: true, closed: true, want: signal.ErrClosed},
		{name: "role operation cancelled", connected: true, cancelled: true, want: context.Canceled},
		{name: "unlinked", connected: true, unlinked: true, want: signal.ErrDeviceUnlinked},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := settingsFake()
			if test.unlinked {
				fake.Incoming = []signal.Event{&signal.Connection{State: signal.StateLoggedOut}}
			}

			cli := profileClient(t, fake, profileAliceACI, test.connected)
			if test.closed {
				closeErr := cli.Close()
				if closeErr != nil {
					t.Fatal(closeErr)
				}
			}

			ctx := t.Context()

			if test.cancelled {
				var cancel context.CancelFunc

				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			_, err := cli.SetGroupMemberRole(ctx, roleFakeGroupID, []signal.Recipient{{ACI: profileBobACI}},
				signal.GroupRoleAdmin)
			if !errors.Is(err, test.want) || fake.GroupInfo[roleFakeGroupID].Revision != 7 {
				t.Fatalf("state = %+v,%v", fake.GroupInfo, err)
			}
		})
	}
}

func TestFakeMemberRoleInvalidBatch(t *testing.T) {
	t.Parallel()

	fake := settingsFake()
	cli := profileClient(t, fake, profileBobACI, true)

	_, err := cli.SetGroupMemberRole(t.Context(), roleFakeGroupID, []signal.Recipient{{ACI: profileBobACI}},
		signal.GroupRoleMember)
	if !errors.Is(err, signal.ErrGroupPermission) {
		t.Fatalf("account authorization = %v", err)
	}

	err = cli.Close()
	if err != nil {
		t.Fatal(err)
	}

	cli = profileClient(t, fake, profileAliceACI, true)
	before := settingsFake().GroupInfo

	_, err = cli.SetGroupMemberRole(t.Context(), roleFakeGroupID,
		[]signal.Recipient{{ACI: profileBobACI}, {ACI: "ffffffff-ffff-4fff-8fff-ffffffffffff"}},
		signal.GroupRoleAdmin)
	if !errors.Is(err, signal.ErrInvalidGroupMember) || !reflect.DeepEqual(before, fake.GroupInfo) {
		t.Fatalf("atomicity = %+v,%v", fake.GroupInfo, err)
	}
}
