//go:build cgo || libsignal_go

package signal_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/google/uuid"
)

func TestSetGroupBannedOnce(t *testing.T) { //nolint:funlen,cyclop,gocognit,gocyclo // transport outcomes
	t.Parallel()

	for name, test := range map[string]struct {
		patchErr, fetchErr, notifyErr, want error
		malformed, invalidFetch, uncertain  bool
	}{
		"unban accepted":        {},
		"ban revision conflict": {patchErr: signalmeow.ConflictError, want: signal.ErrGroupChanged},
		"ban manifest conflict": {patchErr: signalmeow.ContactManifestMismatchError, want: signal.ErrGroupChanged},
		"ban forbidden": {
			patchErr: signalmeow.AuthorizationFailedError, want: signalmeow.AuthorizationFailedError,
		},
		"ban rejected": {
			patchErr: signalmeow.GroupPatchNotAcceptedError, want: signalmeow.GroupPatchNotAcceptedError,
		},
		"ban rate limit":        {patchErr: signalmeow.RateLimitError, want: signalmeow.RateLimitError},
		"ban group disappeared": {patchErr: signalmeow.NotFoundError, want: signalmeow.NotFoundError},
		"ban deprecated": {
			patchErr: signalmeow.DeprecatedVersionError, want: signalmeow.DeprecatedVersionError,
		},
		"ban network uncertain":           {patchErr: io.ErrClosedPipe, want: io.ErrClosedPipe, uncertain: true},
		"ban decode uncertain":            {patchErr: io.ErrUnexpectedEOF, want: io.ErrUnexpectedEOF, uncertain: true},
		"ban cancel uncertain":            {patchErr: context.Canceled, want: context.Canceled, uncertain: true},
		"ban accepted malformed response": {malformed: true},
		"ban accepted fetch failure":      {fetchErr: io.ErrClosedPipe, want: io.ErrClosedPipe},
		"ban accepted fetch cancellation": {fetchErr: context.Canceled, want: context.Canceled},
		"ban accepted invalid fetch":      {invalidFetch: true},
		"ban notify failure":              {notifyErr: io.ErrClosedPipe},
	} {
		t.Run(name, func(t *testing.T) {
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
				sender.fetched = &signalmeow.Group{GroupIdentifier: "incorrect ban response group", Revision: 8}
			}

			sid := libsignalgo.NewACIServiceID(uuid.MustParse(memberACI))
			change := &signalmeow.GroupChange{DeleteBannedMembers: []*libsignalgo.ServiceID{&sid}}

			got, err := signal.SetGroupBannedOnce(t.Context(), sender, raw, change, func() { sender.invalidated = true })
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
				!reflect.DeepEqual(sender.raw, raw) {
				t.Fatalf("authoritative result %+v; sender %+v", got, sender)
			}
		})
	}
}

func TestSetGroupBannedPreflightAndLifecycle(t *testing.T) {
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

	_, err = cli.SetGroupBanned(t.Context(), "unused", members, true)
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Fatalf("connection = %v", err)
	}

	_, err = cli.SetGroupBanned(t.Context(), "unused", []signal.Recipient{{PNI: memberACI}}, true)
	if !errors.Is(err, signal.ErrUnresolvable) {
		t.Fatalf("PNI = %v", err)
	}

	_, err = cli.SetGroupBanned(t.Context(), "unused", nil, true)
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

	_, err = cli.SetGroupBanned(t.Context(), "unused", members, true)
	if !errors.Is(err, signal.ErrClosed) {
		t.Fatalf("closed = %v", err)
	}
}

func TestSetGroupBannedCancelledAndUnlinked(t *testing.T) {
	t.Parallel()
	cli := openOffline(t, seedAccount(t), signal.SendOnly())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	members := []signal.Recipient{{ACI: memberACI}}

	_, err := cli.SetGroupBanned(ctx, "unused", members, true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled = %v", err)
	}

	signal.LoseConnection(cli)

	_, err = cli.SetGroupBanned(t.Context(), "unused", members, true)
	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Fatalf("unlinked = %v", err)
	}
}

func TestBannedMembersChangeExactFields(t *testing.T) { //nolint:cyclop // exact wire assertions
	t.Parallel()

	raw := rawGroup(time.Time{})
	bannedAt := time.UnixMilli(1720000000123).UTC()
	targets := []signal.Recipient{
		{ACI: memberACI},
		{ACI: strings.ToUpper(memberACI)},
		{ACI: invitedACI},
		{ACI: joinerACI},
		{ACI: newMemberACI},
	}

	change, err := signal.BannedMembersChange(raw, seededACI, targets, true, bannedAt)
	aci := uuid.MustParse(memberACI)
	pending := libsignalgo.NewACIServiceID(uuid.MustParse(invitedACI))
	requester := uuid.MustParse(joinerACI)

	want := &signalmeow.GroupChange{
		DeleteMembers: []*uuid.UUID{&aci}, DeletePendingMembers: []*libsignalgo.ServiceID{&pending},
		DeleteRequestingMembers: []*uuid.UUID{&requester},
	}

	for _, target := range []string{memberACI, invitedACI, joinerACI, newMemberACI} {
		want.AddBannedMembers = append(want.AddBannedMembers, &signalmeow.BannedMember{
			ServiceID: libsignalgo.NewACIServiceID(uuid.MustParse(target)), Timestamp: 1720000000123,
		})
	}

	if err != nil || !reflect.DeepEqual(change, want) {
		t.Fatalf("ban wire %+v,%v want %+v", change, err, want)
	}

	raw.BannedMembers = want.AddBannedMembers
	change, err = signal.BannedMembersChange(raw, seededACI, targets, false, bannedAt)
	want = &signalmeow.GroupChange{}

	for _, target := range []string{memberACI, invitedACI, joinerACI, newMemberACI} {
		sid := libsignalgo.NewACIServiceID(uuid.MustParse(target))
		want.DeleteBannedMembers = append(want.DeleteBannedMembers, &sid)
	}

	if err != nil || !reflect.DeepEqual(change, want) {
		t.Fatalf("unban-only wire %+v,%v", change, err)
	}

	change, err = signal.BannedMembersChange(raw, seededACI, []signal.Recipient{{ACI: newMemberACI}}, true, bannedAt)
	if err != nil || change != nil {
		t.Fatalf("repeat %+v,%v", change, err)
	}

	change, err = signal.BannedMembersChange(raw, seededACI, []signal.Recipient{{ACI: memberACI}}, true, bannedAt)

	want = &signalmeow.GroupChange{DeleteMembers: []*uuid.UUID{&aci}}

	if err != nil || !reflect.DeepEqual(change, want) {
		t.Fatalf("inconsistent repeat %+v,%v", change, err)
	}
}

func TestConvertGroupBannedACIAndPNI(t *testing.T) {
	t.Parallel()

	raw := rawGroup(time.Time{})
	aci := libsignalgo.NewACIServiceID(uuid.MustParse(memberACI))
	pni := libsignalgo.NewPNIServiceID(uuid.MustParse(memberACI))
	raw.BannedMembers = []*signalmeow.BannedMember{nil, {ServiceID: aci, Timestamp: 1720000000123}, {ServiceID: pni}}
	got := signal.ConvertGroup(raw, seededACI)

	want := []signal.BannedMember{
		{Recipient: signal.Recipient{ACI: memberACI}, BannedAt: time.UnixMilli(1720000000123).UTC()},
		{Recipient: signal.Recipient{PNI: memberACI}},
	}

	if !reflect.DeepEqual(got.Banned, want) {
		t.Fatalf("converted bans %+v", got.Banned)
	}
}

func TestSetGroupBannedNotificationRecipients(t *testing.T) {
	t.Parallel()

	raw := rawGroup(time.Time{})
	raw.GroupMasterKey = types.SerializedGroupMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	before := *raw
	before.Members = slices.Clone(raw.Members)
	before.PendingMembers = slices.Clone(raw.PendingMembers)
	before.RequestingMembers = slices.Clone(raw.RequestingMembers)

	change, err := signal.BannedMembersChange(raw, seededACI, []signal.Recipient{
		{ACI: memberACI}, {ACI: invitedACI}, {ACI: joinerACI}, {ACI: newMemberACI},
	}, true, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	accepted := *raw
	accepted.Revision++
	sender := &additionSender{
		removalSender: removalSender{response: &signalpb.GroupChangeResponse{
			GroupChange: &signalpb.GroupChange{Actions: []byte{8, 8}},
		}}, fetched: &accepted,
	}

	_, err = signal.SetGroupBannedOnce(t.Context(), sender, raw, change, func() { sender.invalidated = true })
	if err != nil {
		t.Fatal(err)
	}

	want := before

	want.Members = append(slices.Clone(before.Members), &signalmeow.GroupMember{
		ACI: uuid.MustParse(joinerACI), Role: signalmeow.GroupMember_DEFAULT,
	})
	if !reflect.DeepEqual(sender.raw, &want) || !reflect.DeepEqual(raw, &before) {
		t.Fatalf("notification changed original or wrong recipients: %+v", sender.raw)
	}
	// Preventive targets and unbanned users must never receive the group master key.
	sender.notifyCalls = 0
	sid := libsignalgo.NewACIServiceID(uuid.MustParse(newMemberACI))
	change = &signalmeow.GroupChange{DeleteBannedMembers: []*libsignalgo.ServiceID{&sid}}

	_, err = signal.SetGroupBannedOnce(t.Context(), sender, raw, change, func() { sender.invalidated = true })
	if err != nil || !reflect.DeepEqual(sender.raw, raw) {
		t.Fatalf("unban notification %+v,%v", sender.raw, err)
	}
}
