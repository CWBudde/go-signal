//go:build cgo || libsignal_go

package signal_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
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
)

func TestAddMembersChange(t *testing.T) {
	t.Parallel()

	raw := rawGroup(time.Time{})
	raw.AccessControl = &signalmeow.GroupAccessControl{Members: signalmeow.AccessControl_MEMBER}
	change, err := signal.AddMembersChange(raw, seededACI, []signal.Recipient{
		{ACI: newMemberACI}, {ACI: strings.ToUpper(newMemberACI)}, {ACI: memberACI}, {ACI: invitedACI}, {ACI: joinerACI},
	})

	want := &signalmeow.GroupChange{
		AddMembers: []*signalmeow.AddMember{{GroupMember: signalmeow.GroupMember{
			ACI: uuid.MustParse(newMemberACI), Role: signalmeow.GroupMember_DEFAULT,
		}}},
		PromoteRequestingMembers: []*signalmeow.RoleMember{{
			ACI: uuid.MustParse(joinerACI), Role: signalmeow.GroupMember_DEFAULT,
		}},
	}
	if err != nil || !reflect.DeepEqual(change, want) {
		t.Fatalf("change = %+v, %v", change, err)
	}

	if len(raw.Members) != 3 || len(raw.PendingMembers) != 2 || len(raw.RequestingMembers) != 1 {
		t.Fatal("builder modified original group")
	}

	unchanged, err := signal.AddMembersChange(raw, seededACI, []signal.Recipient{
		{ACI: seededACI}, {ACI: memberACI}, {ACI: invitedACI},
	})
	if err != nil || len(unchanged.AddMembers) != 0 || len(unchanged.PromoteRequestingMembers) != 0 {
		t.Fatalf("existing members caused change: %+v, %v", unchanged, err)
	}
}

func TestAddMembersChangePolicy(t *testing.T) {
	t.Parallel()

	raw := rawGroup(time.Time{})
	raw.AccessControl = &signalmeow.GroupAccessControl{Members: signalmeow.AccessControl_MEMBER}

	// Only admins may approve requests, even if ordinary members can invite others.
	_, err := signal.AddMembersChange(raw, memberACI, []signal.Recipient{{ACI: newMemberACI}, {ACI: joinerACI}})
	if !errors.Is(err, signal.ErrGroupPermission) {
		t.Fatalf("nonadmin approval = %v", err)
	}

	raw.BannedMembers = []*signalmeow.BannedMember{{ServiceID: libsignalgo.NewACIServiceID(uuid.MustParse(newMemberACI))}}

	_, err = signal.AddMembersChange(raw, seededACI, []signal.Recipient{{ACI: newMemberACI}})
	if !errors.Is(err, signal.ErrInvalidGroupMember) {
		t.Fatalf("banned member = %v", err)
	}
}

func TestConvertGroupMemberAddPermissions(t *testing.T) {
	t.Parallel()

	raw := rawGroup(time.Time{})
	raw.AccessControl = &signalmeow.GroupAccessControl{Members: signalmeow.AccessControl_MEMBER}

	if !signal.ConvertGroup(raw, seededACI).MembersCanAddMembers {
		t.Fatal("member-add permissions lost in conversion")
	}

	raw.AccessControl = nil
	if signal.ConvertGroup(raw, seededACI).MembersCanAddMembers {
		t.Fatal("missing access control permitted additions")
	}
}

func TestAddGroupMembersOnceInvalidResponse(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		response  *signalpb.GroupChangeResponse
		fetched   *signalmeow.Group
		wantFetch int
	}{
		{"missing signed response", nil, nil, 0},
		{"missing group", &signalpb.GroupChangeResponse{GroupChange: &signalpb.GroupChange{Actions: []byte{8, 8}}}, nil, 1},
		{
			"stale group",
			&signalpb.GroupChangeResponse{GroupChange: &signalpb.GroupChange{Actions: []byte{8, 8}}},
			rawGroup(time.Time{}), 1,
		},
		{
			"wrong group",
			&signalpb.GroupChangeResponse{GroupChange: &signalpb.GroupChange{Actions: []byte{8, 8}}},
			&signalmeow.Group{GroupIdentifier: "other-group", Revision: 8}, 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			raw := rawGroup(time.Time{})
			raw.GroupMasterKey = types.SerializedGroupMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 32)))
			sender := &additionSender{removalSender: removalSender{response: test.response}, fetched: test.fetched}

			got, err := signal.AddGroupMembersOnce(t.Context(), sender, raw, &signalmeow.GroupChange{},
				func() { sender.invalidated = true })
			if got == nil || got.Revision != 8 || err == nil ||
				!strings.Contains(err.Error(), "inspect groups show before retrying") ||
				sender.patchCalls != 1 || sender.fetchCalls != test.wantFetch {
				t.Fatalf("invalid response = %+v, %v; sender %+v", got, err, sender)
			}
		})
	}
}

type additionSender struct {
	removalSender

	fetchCalls    int
	fetched       *signalmeow.Group
	fetchErr      error
	fetchRevision uint32
}

func (s *additionSender) RetrieveGroupByID(_ context.Context, _ types.GroupIdentifier, revision uint32) (
	*signalmeow.Group, *signalmeow.SendEndorsementCache, error,
) {
	s.fetchCalls++
	s.fetchRevision = revision

	return s.fetched, nil, s.fetchErr
}

func TestAddGroupMembersOnce(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name                          string
		patchErr, fetchErr, notifyErr error
		want                          error
	}{
		{"accepted pending invitation", nil, nil, nil, nil},
		{"conflict", signalmeow.ConflictError, nil, nil, signal.ErrGroupChanged},
		{"server failure", io.ErrUnexpectedEOF, nil, nil, io.ErrUnexpectedEOF},
		{"committed but refetch failed", nil, io.ErrClosedPipe, nil, io.ErrClosedPipe},
		{"committed but notification failed", nil, nil, io.ErrClosedPipe, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			raw := rawGroup(time.Time{})
			raw.GroupMasterKey = types.SerializedGroupMasterKey(base64.StdEncoding.EncodeToString(make([]byte, 32)))
			accepted := *raw
			accepted.Revision = 8
			accepted.PendingMembers = []*signalmeow.PendingMember{{
				ServiceID: libsignalgo.NewACIServiceID(uuid.MustParse(newMemberACI)), Role: signalmeow.GroupMember_DEFAULT,
			}}
			sender := &additionSender{
				removalSender: removalSender{
					patchErr: test.patchErr, notifyErr: test.notifyErr,
					response: &signalpb.GroupChangeResponse{GroupChange: &signalpb.GroupChange{Actions: []byte{8, 8}}},
				},
				fetched: &accepted, fetchErr: test.fetchErr,
			}
			change := &signalmeow.GroupChange{
				AddMembers: []*signalmeow.AddMember{{GroupMember: signalmeow.GroupMember{
					ACI: uuid.MustParse(newMemberACI), Role: signalmeow.GroupMember_DEFAULT,
				}}},
				PromoteRequestingMembers: []*signalmeow.RoleMember{{
					ACI: uuid.MustParse(joinerACI), Role: signalmeow.GroupMember_DEFAULT,
				}},
			}

			got, err := signal.AddGroupMembersOnce(t.Context(), sender, raw, change, func() { sender.invalidated = true })
			if !errors.Is(err, test.want) || sender.patchCalls != 1 {
				t.Fatalf("addition = %+v, %v; patches %d", got, err, sender.patchCalls)
			}

			if test.patchErr != nil {
				assertRejectedAddition(t, sender, got)
				return
			}

			assertAdditionNotification(t, sender, raw)
			assertCommittedAddition(t, got, err, &accepted, test.fetchErr)
		})
	}
}

func assertAdditionNotification(t *testing.T, sender *additionSender, raw *signalmeow.Group) {
	t.Helper()

	if sender.fetchCalls != 1 || sender.fetchRevision != 8 || sender.notifyCalls != 1 || sender.notifiedBeforeEviction {
		t.Fatalf("post-commit operations = %+v", sender)
	}

	if len(sender.raw.Members) != 4 || sender.raw.Members[3].ACI.String() != joinerACI || len(raw.Members) != 3 {
		t.Fatal("approved requester missing from notification or input mutated")
	}
}

func assertRejectedAddition(t *testing.T, sender *additionSender, got *signalmeow.Group) {
	t.Helper()

	if got != nil || sender.fetchCalls != 0 || sender.notifyCalls != 0 {
		t.Fatal("failed mutation returned accepted state, fetched or notified")
	}
}

func assertCommittedAddition(t *testing.T, got *signalmeow.Group, err error,
	accepted *signalmeow.Group, fetchErr error,
) {
	t.Helper()

	if fetchErr != nil {
		if !strings.Contains(err.Error(), "change was accepted") || got == nil || got.Revision != 8 {
			t.Fatalf("missing partial-success result: %+v, %v", got, err)
		}

		return
	}

	if got != accepted || len(got.PendingMembers) != 1 {
		t.Fatalf("did not return authoritative pending membership: %+v", got)
	}
}
