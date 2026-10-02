//nolint:goconst // literal expectations are independent fixtures
package signaltest_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func settingsFake() *signaltest.Fake {
	fake := profileFake()
	fake.GroupInfo = map[string]signal.Group{"group": {
		ID:                       "group",
		Title:                    "title",
		Description:              "old",
		Revision:                 7,
		MembersCanEditAttributes: true,
		Members: []signal.GroupMember{
			{
				Recipient: signal.Recipient{ACI: profileAliceACI},
				Role:      signal.GroupRoleAdmin,
			},
			{
				Recipient: signal.Recipient{ACI: profileBobACI},
				Role:      signal.GroupRoleMember,
			},
		},
	}}
	fake.GroupKeys = map[string]string{"key": "group"}

	return fake
}

func TestFakeGroupUpdateAtomicAndAccountAware(t *testing.T) { //nolint:cyclop // assertions cover atomic outcomes
	t.Parallel()

	fake := settingsFake()
	cli := profileClient(t, fake, profileBobACI, true)

	_, err := cli.UpdateGroup(t.Context(), "key",
		signal.GroupUpdate{
			Description:       new("new"),
			AnnouncementsOnly: new(false),
		})
	if !errors.Is(err, signal.ErrGroupPermission) ||
		fake.GroupInfo["group"].Description != "old" ||
		fake.GroupInfo["group"].Revision != 7 {
		t.Fatalf("mixed request changed state: %+v,%v", fake.GroupInfo, err)
	}

	got, err := cli.UpdateGroup(t.Context(), "key",
		signal.GroupUpdate{
			Description:  new("new"),
			TimerSeconds: new(uint32(10)),
		})
	if err != nil || got.Description != "new" || got.Revision != 8 || got.Role != signal.GroupRoleMember {
		t.Fatalf("account update = %+v,%v", got, err)
	}

	fake.UpdateGroupErr = signal.ErrGroupChanged

	got, err = cli.UpdateGroup(t.Context(), "group", signal.GroupUpdate{Description: new("new")})
	if err != nil || got.Revision != 8 {
		t.Fatalf("noop = %+v,%v", got, err)
	}

	_, err = cli.UpdateGroup(t.Context(), "group", signal.GroupUpdate{Description: new("changed")})
	if !errors.Is(err, signal.ErrGroupChanged) ||
		fake.GroupInfo["group"].Description != "new" ||
		fake.GroupInfo["group"].Revision != 8 {
		t.Fatalf("conflict = %+v,%v", fake.GroupInfo, err)
	}

	fake.UpdateGroupErr = nil
	fake.GroupUpdateFollowUpErr = io.ErrClosedPipe

	got, err = cli.UpdateGroup(t.Context(), "group", signal.GroupUpdate{Description: new("")})
	if !errors.Is(err, io.ErrClosedPipe) ||
		got.ID != "group" ||
		got.Revision != 9 ||
		got.MasterKey != "" ||
		!strings.Contains(err.Error(), "inspect groups show before retrying") ||
		fake.GroupInfo["group"].Description != "" {
		t.Fatalf("accepted failure = %+v,%v", got, err)
	}
}

func TestFakeGroupUpdateLifecycle(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name                                   string
		connected, closed, cancelled, unlinked bool
		want                                   error
	}{
		{name: "not connected", want: signal.ErrNotConnected},
		{name: "closed", connected: true, closed: true, want: signal.ErrClosed},
		{name: "cancelled", connected: true, cancelled: true, want: context.Canceled},
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
				err := cli.Close()
				if err != nil {
					t.Fatal(err)
				}
			}

			ctx := t.Context()

			if test.cancelled {
				var cancel context.CancelFunc

				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			_, err := cli.UpdateGroup(ctx, "group", signal.GroupUpdate{Description: new("new")})
			if !errors.Is(err, test.want) || fake.GroupInfo["group"].Revision != 7 {
				t.Fatalf("lifecycle = %v; group %+v", err, fake.GroupInfo)
			}
		})
	}
}
