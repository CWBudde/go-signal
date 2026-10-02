package signaltest_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	fakeLinkKey      = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	fakeLinkPassword = "AQEBAQEBAQEBAQEBAQEBAQ=="
)

func inviteFake() *signaltest.Fake {
	fake := settingsFake()
	group := fake.GroupInfo[roleFakeGroupID]
	group.MasterKey = fakeLinkKey
	fake.GroupInfo[roleFakeGroupID] = group
	fake.GroupLinkStates = map[string]signal.GroupLinkState{roleFakeGroupID: signal.GroupLinkEnabled}
	fake.GroupLinkPasswords = map[string]string{roleFakeGroupID: fakeLinkPassword}

	return fake
}

//nolint:cyclop // behavioral matrix
func TestFakeGroupLinkReadAndTransitions(t *testing.T) {
	t.Parallel()

	fake := inviteFake()
	cli := profileClient(t, fake, profileAliceACI, true)

	first, err := cli.GroupLink(t.Context(), "key")
	if err != nil || first.URL == "" || first.State != signal.GroupLinkEnabled || first.Revision != 7 {
		t.Fatalf("show %+v,%v", first, err)
	}

	disabled, err := cli.UpdateGroupLink(t.Context(), "key", signal.GroupLinkUpdate{State: new(signal.GroupLinkDisabled)})
	if err != nil || disabled.URL != "" || disabled.Revision != 8 ||
		fake.GroupLinkPasswords[roleFakeGroupID] != fakeLinkPassword {
		t.Fatalf("disable %+v,%v", disabled, err)
	}

	enabled, err := cli.UpdateGroupLink(t.Context(), "key", signal.GroupLinkUpdate{State: new(signal.GroupLinkEnabled)})
	if err != nil || enabled.URL != first.URL || enabled.Revision != 9 {
		t.Fatalf("reenable %+v,%v", enabled, err)
	}

	fake.UpdateGroupLinkErr = signal.ErrGroupChanged

	noop, err := cli.UpdateGroupLink(t.Context(), "key", signal.GroupLinkUpdate{State: new(signal.GroupLinkEnabled)})
	if err != nil || noop.Revision != 9 {
		t.Fatalf("noop %+v,%v", noop, err)
	}

	_, err = cli.UpdateGroupLink(t.Context(), "key", signal.GroupLinkUpdate{Reset: true})
	if !errors.Is(err, signal.ErrGroupChanged) || fake.GroupLinkPasswords[roleFakeGroupID] != fakeLinkPassword {
		t.Fatalf("rejected reset %v", err)
	}

	fake.UpdateGroupLinkErr = nil
	fake.GroupLinkFollowUpErr = io.ErrClosedPipe

	accepted, err := cli.UpdateGroupLink(t.Context(), "key", signal.GroupLinkUpdate{Reset: true})
	if !errors.Is(err, io.ErrClosedPipe) || accepted.ID != roleFakeGroupID || accepted.Revision != 10 ||
		accepted.URL != "" || accepted.State != "" || !strings.Contains(err.Error(), "inspect groups link show") ||
		fake.GroupLinkPasswords[roleFakeGroupID] == fakeLinkPassword {
		t.Fatalf("accepted %+v,%v", accepted, err)
	}
}

func TestFakeGroupLinkAccountAuthorization(t *testing.T) {
	t.Parallel()

	fake := inviteFake()
	cli := profileClient(t, fake, profileBobACI, true)

	got, err := cli.GroupLink(t.Context(), "key")
	if err != nil || got.URL == "" {
		t.Fatalf("member read %+v,%v", got, err)
	}

	_, err = cli.UpdateGroupLink(t.Context(), "key", signal.GroupLinkUpdate{State: new(signal.GroupLinkEnabled)})
	if !errors.Is(err, signal.ErrGroupPermission) {
		t.Fatalf("member write %v", err)
	}

	group := fake.GroupInfo[roleFakeGroupID]
	group.Members = group.Members[:1]
	fake.GroupInfo[roleFakeGroupID] = group

	_, err = cli.GroupLink(t.Context(), "key")
	if !errors.Is(err, signal.ErrNotAMember) {
		t.Fatalf("removed read %v", err)
	}
}

func TestFakeGroupLinkLifecycle(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name                                   string
		connected, closed, cancelled, unlinked bool
		want                                   error
	}{
		{name: "invite disconnected", want: signal.ErrNotConnected},
		{name: "invite closed", connected: true, closed: true, want: signal.ErrClosed},
		{name: "invite cancelled", connected: true, cancelled: true, want: context.Canceled},
		{name: "invite unlinked", connected: true, unlinked: true, want: signal.ErrDeviceUnlinked},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := inviteFake()
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

			_, err := cli.GroupLink(ctx, "key")
			if !errors.Is(err, test.want) {
				t.Fatalf("read %v", err)
			}

			_, err = cli.UpdateGroupLink(ctx, "key", signal.GroupLinkUpdate{Reset: true})
			if !errors.Is(err, test.want) || fake.GroupInfo[roleFakeGroupID].Revision != 7 {
				t.Fatalf("write %v", err)
			}
		})
	}
}

func TestFakeGroupLinkMissingSecretsAndUnknownRefs(t *testing.T) {
	t.Parallel()

	fake := inviteFake()
	fake.GroupLinkStates = nil
	fake.GroupLinkPasswords = nil
	cli := profileClient(t, fake, profileAliceACI, true)
	got, err := cli.UpdateGroupLink(t.Context(), "key", signal.GroupLinkUpdate{State: new(signal.GroupLinkApproval)})
	password := fake.GroupLinkPasswords[roleFakeGroupID]

	decoded, decodeErr := base64.StdEncoding.DecodeString(password)
	if err != nil || decodeErr != nil || len(decoded) != 16 || got.URL == "" {
		t.Fatalf("first enable %+v,%v", got, err)
	}

	for _, ref := range []string{
		"invite-secret-invalid-ref",
		base64.StdEncoding.EncodeToString([]byte("unknown-master-key-is-32-byte-long")),
	} {
		_, err = cli.GroupLink(t.Context(), ref)
		if err == nil || strings.Contains(err.Error(), ref) {
			t.Fatalf("read ref leak %v", err)
		}

		_, err = cli.UpdateGroupLink(t.Context(), ref, signal.GroupLinkUpdate{Reset: true})
		if err == nil || strings.Contains(err.Error(), ref) {
			t.Fatalf("write ref leak %v", err)
		}
	}
}

func TestFakeGroupLinkAdminDemotionRejectsNoop(t *testing.T) {
	t.Parallel()

	fake := inviteFake()
	cli := profileClient(t, fake, profileAliceACI, true)

	_, err := cli.GroupLink(t.Context(), "key")
	if err != nil {
		t.Fatal(err)
	}

	group := fake.GroupInfo[roleFakeGroupID]
	group.Members[0].Role = signal.GroupRoleMember
	fake.GroupInfo[roleFakeGroupID] = group

	_, err = cli.UpdateGroupLink(t.Context(), "key", signal.GroupLinkUpdate{State: new(signal.GroupLinkEnabled)})
	if !errors.Is(err, signal.ErrGroupPermission) || fake.GroupInfo[roleFakeGroupID].Revision != 7 {
		t.Fatalf("cached admin authorized noop: %v", err)
	}
}
