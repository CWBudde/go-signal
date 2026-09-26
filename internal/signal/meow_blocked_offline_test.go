//go:build cgo && !purego

package signal_test

import (
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/events"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/types"
)

func TestSetBlockedWithoutServer(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	alice := []signal.Recipient{{ACI: aliceUser}}

	client, err := signal.Open(ctx, signal.Options{DataDir: seedAccount(t)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer client.Close()

	err = client.SetBlocked(ctx, alice, true)
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Errorf("not connected: %v, want ErrNotConnected", err)
	}

	client = openOffline(t, seedAccount(t), signal.SendOnly())

	err = client.SetBlocked(ctx, []signal.Recipient{{Number: aliceE164}}, true)
	if !errors.Is(err, signal.ErrUnresolvable) {
		t.Errorf("without an ACI: %v, want ErrUnresolvable", err)
	}

	// The blocked list lives in the storage service, which needs the key from the phone.
	err = client.SetBlocked(ctx, alice, true)
	if !errors.Is(err, signal.ErrStorageKeyUnknown) {
		t.Errorf("without the storage key: %v, want ErrStorageKeyUnknown", err)
	}

	signal.LoseConnection(client)

	err = client.SetBlocked(ctx, alice, false)
	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Errorf("after a logout: %v, want ErrDeviceUnlinked", err)
	}

	err = client.Close()
	if err != nil {
		t.Fatal(err)
	}

	err = client.SetBlocked(ctx, alice, true)
	if !errors.Is(err, signal.ErrClosed) {
		t.Errorf("after Close: %v, want ErrClosed", err)
	}
}

func TestStorageSyncKeepsOverridesWithoutServer(t *testing.T) {
	t.Parallel()

	dataDir := seedAccount(t)
	seedContacts(t, dataDir)
	client := openOffline(t, dataDir)

	// signalmeow stored alice from a background storage sync. Whether that undid our block
	// can't be checked without the server, so the overrides stay; only frank's expired one goes.
	list := &events.ContactList{IsFromDB: true, Contacts: []*types.Recipient{{ACI: uuid.MustParse(aliceUser)}}}
	if !signal.Handle(client, list) {
		t.Error("storage contact list not acked")
	}

	wantOverrides(t, client, uuid.MustParse(aliceUser), uuid.MustParse(eveUser))
}
