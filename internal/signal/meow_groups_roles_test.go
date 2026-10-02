//go:build cgo || libsignal_go

package signal_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/google/uuid"
)

func TestMemberRoleChangeExactFields(t *testing.T) { //nolint:cyclop // exact wire and validation outcomes
	t.Parallel()

	raw := rawGroup(time.Time{})
	targets := []signal.Recipient{{ACI: strings.ToUpper(memberACI)}, {ACI: memberACI}, {ACI: seededACI}}
	change, err := signal.MemberRoleChange(raw, seededACI, targets, signal.GroupRoleAdmin)

	want := &signalmeow.GroupChange{ModifyMemberRoles: []*signalmeow.RoleMember{{
		ACI: uuid.MustParse(memberACI), Role: signalmeow.GroupMember_ADMINISTRATOR,
	}}}
	if err != nil || !reflect.DeepEqual(change, want) {
		t.Fatalf("wire = %+v,%v", change, err)
	}

	if raw.Members[2].Role != signalmeow.GroupMember_DEFAULT || raw.Revision != 7 {
		t.Fatal("input changed")
	}

	change, err = signal.MemberRoleChange(raw, seededACI, []signal.Recipient{{ACI: memberACI}}, signal.GroupRoleMember)
	if err != nil || change != nil {
		t.Fatalf("noop = %+v,%v", change, err)
	}

	_, err = signal.MemberRoleChange(raw, memberACI, []signal.Recipient{{ACI: memberACI}}, signal.GroupRoleMember)
	if !errors.Is(err, signal.ErrGroupPermission) {
		t.Fatalf("unauthorized noop = %v", err)
	}

	_, err = signal.MemberRoleChange(raw, seededACI, []signal.Recipient{{ACI: memberACI}, {ACI: invitedACI}},
		signal.GroupRoleAdmin)
	if !errors.Is(err, signal.ErrInvalidGroupMember) {
		t.Fatalf("mixed invalid = %v", err)
	}

	raw.Members[2].Role = signalmeow.GroupMember_ADMINISTRATOR
	change, err = signal.MemberRoleChange(raw, seededACI, []signal.Recipient{{ACI: seededACI}}, signal.GroupRoleMember)

	want = &signalmeow.GroupChange{ModifyMemberRoles: []*signalmeow.RoleMember{{
		ACI: uuid.MustParse(seededACI), Role: signalmeow.GroupMember_DEFAULT,
	}}}
	if err != nil || !reflect.DeepEqual(change, want) {
		t.Fatalf("demotion wire = %+v,%v", change, err)
	}

	_, err = signal.MemberRoleChange(raw, seededACI, []signal.Recipient{{ACI: memberACI}, {ACI: seededACI}},
		signal.GroupRoleMember)
	if !errors.Is(err, signal.ErrLastAdmin) {
		t.Fatalf("last admin = %v", err)
	}

	raw.Revision = math.MaxUint32

	_, err = signal.MemberRoleChange(raw, seededACI, []signal.Recipient{{ACI: seededACI}}, signal.GroupRoleMember)
	if !errors.Is(err, signal.ErrUnknownGroup) {
		t.Fatalf("overflow = %v", err)
	}
}

func TestSetGroupMemberRoleOnce(t *testing.T) { //nolint:funlen,cyclop,gocognit,gocyclo // transport outcomes
	t.Parallel()

	for _, test := range []struct {
		name                                string
		patchErr, fetchErr, notifyErr, want error
		malformed, invalidFetch, uncertain  bool
	}{
		{name: "role change accepted"},
		{name: "role revision conflict", patchErr: signalmeow.ConflictError, want: signal.ErrGroupChanged},
		{name: "manifest conflict", patchErr: signalmeow.ContactManifestMismatchError, want: signal.ErrGroupChanged},
		{name: "forbidden", patchErr: signalmeow.AuthorizationFailedError, want: signalmeow.AuthorizationFailedError},
		{name: "rejected", patchErr: signalmeow.GroupPatchNotAcceptedError, want: signalmeow.GroupPatchNotAcceptedError},
		{name: "rate limit", patchErr: signalmeow.RateLimitError, want: signalmeow.RateLimitError},
		{name: "group disappeared", patchErr: signalmeow.NotFoundError, want: signalmeow.NotFoundError},
		{name: "deprecated", patchErr: signalmeow.DeprecatedVersionError, want: signalmeow.DeprecatedVersionError},
		{name: "network uncertain", patchErr: io.ErrClosedPipe, want: io.ErrClosedPipe, uncertain: true},
		{name: "decode uncertain", patchErr: io.ErrUnexpectedEOF, want: io.ErrUnexpectedEOF, uncertain: true},
		{name: "cancel uncertain", patchErr: context.Canceled, want: context.Canceled, uncertain: true},
		{name: "accepted malformed response", malformed: true},
		{name: "accepted fetch failure", fetchErr: io.ErrClosedPipe, want: io.ErrClosedPipe},
		{name: "accepted fetch cancellation", fetchErr: context.Canceled, want: context.Canceled},
		{name: "accepted invalid fetch", invalidFetch: true},
		{name: "notify failure", notifyErr: io.ErrClosedPipe},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			raw := rawGroup(time.Time{})
			raw.GroupMasterKey = types.SerializedGroupMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 32)))
			accepted := *raw
			accepted.Revision = 8

			sender := &additionSender{removalSender: removalSender{
				patchErr: test.patchErr, notifyErr: test.notifyErr,
				response: &signalpb.GroupChangeResponse{GroupChange: &signalpb.GroupChange{Actions: []byte{8, 8}}},
			}, fetched: &accepted, fetchErr: test.fetchErr}
			if test.malformed {
				sender.response = nil
			}

			if test.invalidFetch {
				sender.fetched = &signalmeow.Group{GroupIdentifier: "incorrect role response group", Revision: 8}
			}

			change := &signalmeow.GroupChange{ModifyMemberRoles: []*signalmeow.RoleMember{{
				ACI: uuid.MustParse(memberACI), Role: signalmeow.GroupMember_ADMINISTRATOR,
			}}}

			got, err := signal.SetGroupMemberRoleOnce(t.Context(), sender, raw, change, func() { sender.invalidated = true })
			if sender.patchCalls != 1 ||
				change.Revision != 8 ||
				change.GroupMasterKey != raw.GroupMasterKey ||
				(!test.malformed && !test.invalidFetch && !errors.Is(err, test.want)) {
				t.Fatalf("single patch = %+v,%v; sender %+v", got, err, sender)
			}

			if test.patchErr != nil {
				if got != nil ||
					sender.fetchCalls != 0 ||
					sender.notifyCalls != 0 ||
					errors.Is(err, signal.ErrGroupUpdateUncertain) != test.uncertain {
					t.Fatalf("patch failure = %+v,%v", got, err)
				}

				if test.uncertain && !strings.Contains(err.Error(), "inspect groups show") {
					t.Fatalf("uncertain guidance %v", err)
				}

				return
			}

			if got == nil ||
				got.GroupIdentifier != raw.GroupIdentifier ||
				got.Revision != 8 ||
				!sender.invalidated ||
				errors.Is(err, signal.ErrGroupUpdateUncertain) {
				t.Fatalf("accepted = %+v,%v", got, err)
			}

			if test.malformed || test.fetchErr != nil || test.invalidFetch {
				if err == nil || !strings.Contains(err.Error(), "inspect groups show before retrying") {
					t.Fatalf("acceptance guidance %v", err)
				}

				if test.malformed && (sender.fetchCalls != 0 || sender.notifyCalls != 0) {
					t.Fatal("used malformed response")
				}

				return
			}

			if got != &accepted ||
				sender.fetchCalls != 1 ||
				sender.fetchRevision != 8 ||
				sender.notifyCalls != 1 ||
				sender.notifiedBeforeEviction ||
				sender.raw != raw {
				t.Fatalf("authoritative result %+v; sender %+v", got, sender)
			}
		})
	}
}

func TestSetGroupMemberRolePreflightAndLifecycle(t *testing.T) {
	t.Parallel()

	cli, err := signal.Open(t.Context(), signal.Options{DataDir: seedAccount(t)})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		closeErr := cli.Close()
		if closeErr != nil {
			t.Error(closeErr)
		}
	})

	members := []signal.Recipient{{ACI: memberACI}}

	_, err = cli.SetGroupMemberRole(t.Context(), "unused", members, signal.GroupRoleAdmin)
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Fatalf("connection = %v", err)
	}

	_, err = cli.SetGroupMemberRole(t.Context(), "unused", members, signal.GroupRoleUnknown)
	if !errors.Is(err, signal.ErrInvalidGroupMember) {
		t.Fatalf("role = %v", err)
	}

	_, err = cli.SetGroupMemberRole(t.Context(), "unused", nil, signal.GroupRoleAdmin)
	if !errors.Is(err, signal.ErrInvalidGroupMember) {
		t.Fatalf("empty = %v", err)
	}

	err = cli.Close()
	if err != nil {
		t.Fatal(err)
	}

	cli = openOffline(t, seedAccount(t), signal.SendOnly())

	err = cli.Close()
	if err != nil {
		t.Fatal(err)
	}

	_, err = cli.SetGroupMemberRole(t.Context(), "unused", members, signal.GroupRoleAdmin)
	if !errors.Is(err, signal.ErrClosed) {
		t.Fatalf("closed = %v", err)
	}
}

func TestSetGroupMemberRoleCancelledAndUnlinked(t *testing.T) {
	t.Parallel()
	cli := openOffline(t, seedAccount(t), signal.SendOnly())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	members := []signal.Recipient{{ACI: memberACI}}

	_, err := cli.SetGroupMemberRole(ctx, "unused", members, signal.GroupRoleAdmin)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled = %v", err)
	}

	signal.LoseConnection(cli)

	_, err = cli.SetGroupMemberRole(t.Context(), "unused", members, signal.GroupRoleAdmin)
	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Fatalf("unlinked = %v", err)
	}
}
