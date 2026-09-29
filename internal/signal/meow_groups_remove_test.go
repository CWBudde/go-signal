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
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

func TestRemoveMembersChange(t *testing.T) {
	t.Parallel()

	group := signal.ConvertGroup(rawGroup(time.Time{}), seededACI)
	targets := []signal.Recipient{{ACI: memberACI}, {ACI: invitedACI}, {ACI: joinerACI}, {ACI: strings.ToUpper(memberACI)}}
	change, next, err := signal.RemoveMembersChange(group, seededACI, targets)
	memberID, requesterID := uuid.MustParse(memberACI), uuid.MustParse(joinerACI)
	invitedID := libsignalgo.NewACIServiceID(uuid.MustParse(invitedACI))

	want := &signalmeow.GroupChange{
		DeleteMembers:           []*uuid.UUID{&memberID},
		DeletePendingMembers:    []*libsignalgo.ServiceID{&invitedID},
		DeleteRequestingMembers: []*uuid.UUID{&requesterID},
	}
	if err != nil || !reflect.DeepEqual(change, want) {
		t.Fatalf("change = %+v, %v", change, err)
	}

	if got := []int{len(next.Members), len(next.Pending), len(next.Requesting)}; !reflect.DeepEqual(got, []int{1, 1, 0}) {
		t.Fatalf("next = %+v", next)
	}

	counts := []int{len(group.Members), len(group.Pending), len(group.Requesting)}
	if !reflect.DeepEqual(counts, []int{2, 2, 1}) {
		t.Fatal("changed input group")
	}

	change, _, err = signal.RemoveMembersChange(group, memberACI, targets)
	if !errors.Is(err, signal.ErrGroupPermission) || change != nil {
		t.Fatalf("unauthorized = %+v, %v", change, err)
	}
}

type removalSender struct {
	invalidated             bool
	notifiedBeforeEviction  bool
	patchCalls, notifyCalls int
	patchErr, notifyErr     error
	response                *signalpb.GroupChangeResponse
	raw                     *signalmeow.Group
	context                 *signalpb.GroupContextV2
}

func (s *removalSender) EncryptAndSignGroupChange(context.Context, *signalmeow.GroupChange) (
	*signalpb.GroupChangeResponse, error,
) {
	s.patchCalls++
	return s.response, s.patchErr
}

func (s *removalSender) SendGroupUpdate(_ context.Context, raw *signalmeow.Group,
	ctx *signalpb.GroupContextV2, _ *signalmeow.GroupChange,
) (*signalmeow.GroupMessageSendResult, error) {
	s.notifyCalls++
	s.notifiedBeforeEviction = !s.invalidated
	s.raw, s.context = raw, ctx

	return nil, s.notifyErr
}

func TestRemoveGroupMembersOnce(t *testing.T) {
	t.Parallel()

	notificationErr := io.ErrClosedPipe
	for _, test := range []struct {
		name                string
		patchErr, notifyErr error
		wantErr             error
	}{
		{"success", nil, nil, nil},
		{"conflict", signalmeow.ConflictError, nil, signal.ErrGroupChanged},
		{"manifest conflict", signalmeow.ContactManifestMismatchError, nil, signal.ErrGroupChanged},
		{"forbidden", signalmeow.AuthorizationFailedError, nil, signalmeow.AuthorizationFailedError},
		{"notification failure after commit", nil, notificationErr, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			raw := rawGroup(time.Time{})
			raw.GroupMasterKey = types.SerializedGroupMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 32)))
			raw.Members = append(raw.Members[:1], raw.Members[2:]...)
			sender := &removalSender{
				patchErr: test.patchErr, notifyErr: test.notifyErr,
				response: &signalpb.GroupChangeResponse{GroupChange: &signalpb.GroupChange{Actions: []byte{8, 8}}},
			}
			memberID := uuid.MustParse(memberACI)
			change := &signalmeow.GroupChange{DeleteMembers: []*uuid.UUID{&memberID}}

			err := signal.RemoveGroupMembersOnce(t.Context(), sender, raw, change, func() {
				sender.invalidated = true
			})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("update = %v, want %v", err, test.wantErr)
			}

			if sender.patchCalls != 1 || change.Revision != raw.Revision+1 || change.GroupMasterKey != raw.GroupMasterKey {
				t.Fatalf("patch calls %d, change %+v", sender.patchCalls, change)
			}

			if test.patchErr != nil {
				if sender.notifyCalls != 0 {
					t.Fatal("notified after failed patch")
				}

				return
			}

			assertRemovalNotification(t, sender, raw)
		})
	}
}

func assertRemovalNotification(t *testing.T, sender *removalSender, raw *signalmeow.Group) {
	t.Helper()

	if sender.notifiedBeforeEviction || sender.notifyCalls != 1 || sender.raw != raw ||
		len(sender.raw.Members) != 2 || len(sender.raw.PendingMembers) != 2 {
		t.Fatalf("notification lost original members: %+v", sender)
	}

	if sender.context.GetRevision() != 8 || len(sender.context.GetMasterKey()) != 32 {
		t.Fatalf("notification context: %+v", sender.context)
	}

	var signed signalpb.GroupChange

	err := proto.Unmarshal(sender.context.GetGroupChange(), &signed)
	if err != nil || !proto.Equal(&signed, sender.response.GetGroupChange()) {
		t.Fatalf("signed notification: %+v, %v", &signed, err)
	}
}

func TestRemoveGroupMembersOnceInvalidState(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, key string
		revision  uint32
	}{
		{"invalid base64", "!", 0},
		{"short key", base64.StdEncoding.EncodeToString([]byte{1}), 0},
		{"revision overflow", base64.StdEncoding.EncodeToString(make([]byte, 32)), math.MaxUint32},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			sender := &removalSender{}
			raw := &signalmeow.Group{GroupMasterKey: types.SerializedGroupMasterKey(test.key), Revision: test.revision}

			err := signal.RemoveGroupMembersOnce(t.Context(), sender, raw, &signalmeow.GroupChange{}, func() {
				sender.invalidated = true
			})
			if !errors.Is(err, signal.ErrUnknownGroup) || sender.patchCalls != 0 || sender.notifyCalls != 0 {
				t.Fatalf("invalid state = %v, sender %+v", err, sender)
			}
		})
	}
}

func TestRemoveGroupMembersOnceMalformedResponse(t *testing.T) {
	t.Parallel()

	for _, response := range []*signalpb.GroupChangeResponse{nil, {}, {GroupChange: &signalpb.GroupChange{}}} {
		sender := &removalSender{response: response}
		raw := &signalmeow.Group{
			GroupMasterKey: types.SerializedGroupMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 32))),
		}

		err := signal.RemoveGroupMembersOnce(t.Context(), sender, raw, &signalmeow.GroupChange{}, func() {
			sender.invalidated = true
		})
		if err == nil || !strings.Contains(err.Error(), "inspect groups show before retrying") ||
			sender.patchCalls != 1 || sender.notifyCalls != 0 {
			t.Fatalf("malformed response = %v, sender %+v", err, sender)
		}
	}
}
