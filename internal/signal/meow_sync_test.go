//go:build cgo

package signal_test

import (
	"log/slog"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/events"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/types"
)

func TestContactListWakesSync(t *testing.T) {
	t.Parallel()

	client, err := signal.Open(t.Context(), signal.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer client.Close()

	arrived, handle, stop := signal.ContactListHook(client)
	defer stop()

	// Contacts that a storage sync changed are no reply to the contact request.
	if !handle(&events.ContactList{Contacts: []*types.Recipient{{}}, IsFromDB: true}) {
		t.Error("storage contact list not acked")
	}

	select {
	case n := <-arrived:
		t.Fatalf("storage contact list woke the waiter (%d contacts)", n)
	default:
	}

	// signalmeow has stored the phone's list; the event is acked and wakes the waiter, even
	// though it isn't delivered on Events.
	if !handle(&events.ContactList{Contacts: []*types.Recipient{{}, {}}}) {
		t.Error("contact list not acked")
	}

	select {
	case n := <-arrived:
		if n != 2 {
			t.Errorf("waiter got %d contacts, want 2", n)
		}
	default:
		t.Fatal("contact list didn't wake the waiter")
	}

	// A second list doesn't block the handler when the waiter has one already.
	handle(&events.ContactList{})
	handle(&events.ContactList{})
}

func TestSyncCounts(t *testing.T) {
	t.Parallel()

	dataDir := seedAccount(t)
	seedSynced(t, dataDir)

	client, err := signal.Open(t.Context(), signal.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer client.Close()

	contacts, groups, err := signal.SyncCounts(t.Context(), client)
	if err != nil || contacts != 2 || groups != 1 {
		t.Errorf("counts = %d contacts, %d groups, %v; want 2 and 1", contacts, groups, err)
	}
}

// seedSynced stores what a sync would in the seeded account's database: ourselves, two
// contacts, a user known only by ACI (no contact), and a group.
func seedSynced(t *testing.T, dataDir string) {
	t.Helper()

	ctx := t.Context()

	dir, err := store.OpenDir(dataDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	data, err := dir.OpenAccount(ctx, seededACI, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}

	defer data.Close()

	device, err := data.Devices.DeviceByACI(ctx, uuid.MustParse(seededACI))
	if err != nil || device == nil {
		t.Fatalf("load device: %v", err)
	}

	for _, rcpt := range []*types.Recipient{
		{ACI: device.ACI, E164: seededNumber},
		{ACI: uuid.New(), E164: "+15550111"},
		{ACI: uuid.New(), ContactName: "Alice"},
		{ACI: uuid.New()},
	} {
		err = device.RecipientStore.StoreRecipient(ctx, rcpt)
		if err != nil {
			t.Fatalf("store recipient: %v", err)
		}
	}

	err = device.GroupStore.StoreMasterKey(ctx, "group-id", "master-key")
	if err != nil {
		t.Fatalf("store group: %v", err)
	}
}
