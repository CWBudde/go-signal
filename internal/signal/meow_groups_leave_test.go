//go:build cgo || libsignal_go

package signal_test

import (
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/google/uuid"
)

func TestLeaveChangePNI(t *testing.T) {
	t.Parallel()

	self := signal.Recipient{ACI: seededACI, PNI: invitedPNI}
	group := signal.Group{Pending: []signal.PendingMember{
		{Recipient: signal.Recipient{PNI: invitedPNI}, Role: signal.GroupRoleMember},
		{Recipient: signal.Recipient{ACI: memberACI}, Role: signal.GroupRoleMember},
	}}

	change, promoted, err := signal.LeaveChangeAs(group, self, nil)
	if err != nil {
		t.Fatal(err)
	}

	want := libsignalgo.NewPNIServiceID(uuid.MustParse(invitedPNI))
	if len(change.DeletePendingMembers) != 1 || *change.DeletePendingMembers[0] != want ||
		len(change.DeleteMembers) != 0 || len(change.DeleteRequestingMembers) != 0 || len(promoted) != 0 {
		t.Fatalf("PNI decline change = %+v, promoted %v", change, promoted)
	}

	assertEncryptedPNIDeletion(t, *change.DeletePendingMembers[0], want)

	if len(group.Pending) != 2 {
		t.Error("input changed")
	}
}

func TestLeaveChangeBothInvitations(t *testing.T) {
	t.Parallel()

	self := signal.Recipient{ACI: seededACI, PNI: invitedPNI}
	group := signal.Group{Pending: []signal.PendingMember{
		{Recipient: signal.Recipient{ACI: seededACI}, Role: signal.GroupRoleMember},
		{Recipient: signal.Recipient{PNI: invitedPNI}, Role: signal.GroupRoleMember},
	}}

	change, _, err := signal.LeaveChangeAs(group, self, nil)
	if err != nil || len(change.DeletePendingMembers) != 2 {
		t.Fatalf("dual decline = %+v, %v", change, err)
	}

	_, _, err = signal.LeaveChangeAs(group, self, []signal.Recipient{{ACI: memberACI}})
	if !errors.Is(err, signal.ErrInvalidPromotion) {
		t.Errorf("invitee promotion = %v", err)
	}
}

func assertEncryptedPNIDeletion(t *testing.T, serviceID, want libsignalgo.ServiceID) {
	t.Helper()
	// Exercise the pinned backend's encryption API with a typed PNI, not its UUID as an ACI.
	secret, err := libsignalgo.DeriveGroupSecretParamsFromMasterKey(libsignalgo.GroupMasterKey(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}

	encrypted, err := secret.EncryptServiceID(serviceID)
	if err != nil {
		t.Fatal(err)
	}

	got, err := secret.DecryptServiceID(*encrypted)
	if err != nil || got != want {
		t.Fatalf("encrypted deletion = %v, %v", got, err)
	}
}
