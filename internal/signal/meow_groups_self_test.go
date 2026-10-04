//go:build cgo || libsignal_go

package signal_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/google/uuid"
)

func TestConvertGroupForSelfPNI(t *testing.T) {
	t.Parallel()

	self := signal.Recipient{ACI: seededACI, PNI: bobACI}
	raw := &signalmeow.Group{PendingMembers: []*signalmeow.PendingMember{{
		ServiceID: libsignalgo.NewPNIServiceID(uuid.MustParse(bobACI)), Role: signalmeow.GroupMember_ADMINISTRATOR,
	}}}

	group := signal.ConvertGroupForSelf(raw, self)
	if group.Membership != signal.MembershipPending || group.Role != signal.GroupRoleAdmin {
		t.Fatalf("converted membership = %v/%v", group.Membership, group.Role)
	}

	if len(group.Pending) != 1 || group.Pending[0].Recipient.PNI != bobACI || group.Pending[0].Recipient.ACI != "" {
		t.Fatalf("lost typed PNI: %+v", group.Pending)
	}
}

func TestGroupJoinOwnPNIRequiresAcceptance(t *testing.T) {
	t.Parallel()
	harness := newJoinHarness(t)
	harness.known = true
	harness.raw.Members = nil
	harness.raw.PendingMembers = []*signalmeow.PendingMember{{
		ServiceID: libsignalgo.NewPNIServiceID(uuid.MustParse(bobACI)), Role: signalmeow.GroupMember_DEFAULT,
	}}

	result, err := signal.JoinGroupAs(t.Context(), harness.ops, signal.Recipient{ACI: seededACI, PNI: bobACI},
		"https://signal.group/#"+joinFragment(32, 16))
	if !errors.Is(err, signal.ErrGroupInvitationRequiresAcceptance) || result.Accepted || result.Verified {
		t.Fatalf("join = %+v, %v", result, err)
	}

	want := strings.Fields("invalidate known fetch invalidate")
	if !reflect.DeepEqual(harness.calls, want) {
		t.Fatalf("join performed extra operations: %v", harness.calls)
	}
}
