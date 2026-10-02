package signaltest_test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestFakeGroupBanAtomicAndAccountAware(t *testing.T) { //nolint:cyclop // sequential state transitions
	t.Parallel()

	fake := settingsFake()
	fake.GroupBanTime = time.UnixMilli(1720000000123).UTC()
	cli := profileClient(t, fake, profileAliceACI, true)
	targets := []signal.Recipient{{ACI: profileBobACI}, {ACI: strings.ToUpper(profileBobACI)}}

	got, err := cli.SetGroupBanned(t.Context(), "key", targets, true)
	if err != nil || got.Revision != 8 || len(got.Banned) != 1 || len(got.Members) != 1 ||
		!got.Banned[0].BannedAt.Equal(fake.GroupBanTime) {
		t.Fatalf("ban %+v,%v", got, err)
	}

	got.Banned[0].Recipient.ACI = "changed-ban-fake-ACI"
	if fake.GroupInfo[roleFakeGroupID].Banned[0].Recipient.ACI != profileBobACI {
		t.Fatal("aliases stored bans")
	}

	got, err = cli.Group(t.Context(), "key")
	if err != nil {
		t.Fatal(err)
	}

	got.Banned[0].Recipient.ACI = "changed-ban-fake-ACI"
	if fake.GroupInfo[roleFakeGroupID].Banned[0].Recipient.ACI != profileBobACI {
		t.Fatal("fetch aliases bans")
	}

	fake.SetGroupBannedErr = signal.ErrGroupChanged

	got, err = cli.SetGroupBanned(t.Context(), "key", targets, true)
	if err != nil || got.Revision != 8 {
		t.Fatalf("noop %+v,%v", got, err)
	}

	_, err = cli.SetGroupBanned(t.Context(), "key", targets, false)
	if !errors.Is(err, signal.ErrGroupChanged) || fake.GroupInfo[roleFakeGroupID].Revision != 8 {
		t.Fatalf("rejected %v", err)
	}

	fake.SetGroupBannedErr = nil
	fake.GroupBanFollowUpErr = io.ErrClosedPipe

	got, err = cli.SetGroupBanned(t.Context(), "key", targets, false)
	if !errors.Is(err, io.ErrClosedPipe) || got.ID != roleFakeGroupID || got.Revision != 9 || got.MasterKey != "" ||
		!strings.Contains(err.Error(), "inspect groups show") || len(fake.GroupInfo[roleFakeGroupID].Banned) != 0 ||
		len(fake.GroupInfo[roleFakeGroupID].Members) != 1 {
		t.Fatalf("accepted %+v,%v", got, err)
	}
}

func TestFakeGroupBanInvalidAndFreshActor(t *testing.T) {
	t.Parallel()

	fake := settingsFake()
	cli := profileClient(t, fake, profileBobACI, true)

	_, err := cli.SetGroupBanned(t.Context(), "key", []signal.Recipient{{ACI: profileAliceACI}}, false)
	if !errors.Is(err, signal.ErrGroupPermission) {
		t.Fatal(err)
	}

	err = cli.Close()
	if err != nil {
		t.Fatal(err)
	}

	cli = profileClient(t, fake, profileAliceACI, true)
	before := settingsFake().GroupInfo

	for _, targets := range [][]signal.Recipient{
		{{ACI: profileBobACI}, {ACI: profileAliceACI}},
		{{ACI: profileBobACI}, {PNI: profileAliceACI}},
	} {
		_, err = cli.SetGroupBanned(t.Context(), "key", targets, true)
		if err == nil || !reflect.DeepEqual(before, fake.GroupInfo) {
			t.Fatalf("atomicity %+v,%v", fake.GroupInfo, err)
		}
	}

	group := fake.GroupInfo[roleFakeGroupID]
	group.Members[0].Role = signal.GroupRoleMember
	fake.GroupInfo[roleFakeGroupID] = group

	_, err = cli.SetGroupBanned(t.Context(), "key", []signal.Recipient{{ACI: profileBobACI}}, false)
	if !errors.Is(err, signal.ErrGroupPermission) {
		t.Fatalf("stale actor %v", err)
	}
}

func TestFakeGroupBanLifecycle(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name                                   string
		connected, closed, cancelled, unlinked bool
		want                                   error
	}{
		{name: "role operation disconnected", want: signal.ErrNotConnected},
		{name: "role operation closed", connected: true, closed: true, want: signal.ErrClosed},
		{name: "role operation cancelled", connected: true, cancelled: true, want: context.Canceled},
		{name: "ban operation unlinked", connected: true, unlinked: true, want: signal.ErrDeviceUnlinked},
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

			_, err := cli.SetGroupBanned(ctx, roleFakeGroupID, []signal.Recipient{{ACI: profileBobACI}},
				true)
			if !errors.Is(err, test.want) || fake.GroupInfo[roleFakeGroupID].Revision != 7 {
				t.Fatalf("state = %+v,%v", fake.GroupInfo, err)
			}
		})
	}
}

func TestFakeGroupBanPreventsAddUntilUnbanned(t *testing.T) {
	t.Parallel()

	fake := settingsFake()
	cli := profileClient(t, fake, profileAliceACI, true)
	targets := []signal.Recipient{{ACI: profileBobACI}}

	_, err := cli.SetGroupBanned(t.Context(), "key", targets, true)
	if err != nil {
		t.Fatal(err)
	}

	_, err = cli.AddGroupMembers(t.Context(), "key", targets)
	if !errors.Is(err, signal.ErrInvalidGroupMember) {
		t.Fatalf("banned add %v", err)
	}

	_, err = cli.SetGroupBanned(t.Context(), "key", targets, false)
	if err != nil {
		t.Fatal(err)
	}

	got, err := cli.AddGroupMembers(t.Context(), "key", targets)
	if err != nil || len(got.Members) != 2 {
		t.Fatalf("unbanned add %+v,%v", got, err)
	}
}
