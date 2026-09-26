//go:build cgo || purego

package signal_test

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/events"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "go.mau.fi/mautrix-signal/pkg/signalmeow/store"
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

func TestVerifyStorageStored(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dataDir := seedAccount(t)
	seedContacts(t, dataDir)

	storedKey, unstoredKey := randomKey(t), randomKey(t)
	storeGroupKey(t, dataDir, base64.StdEncoding.EncodeToString(storedKey))

	client := openSeeded(t, dataDir)
	bob := uuid.MustParse(bobUser)

	record := func(r *signalpb.StorageRecord) *signalmeow.DecryptedStorageRecord {
		return &signalmeow.DecryptedStorageRecord{StorageRecord: r}
	}
	contact := func(c *signalpb.ContactRecord) *signalmeow.DecryptedStorageRecord {
		return record(&signalpb.StorageRecord{Record: &signalpb.StorageRecord_Contact{Contact: c}})
	}
	group := func(key []byte) *signalmeow.DecryptedStorageRecord {
		return record(&signalpb.StorageRecord{
			Record: &signalpb.StorageRecord_GroupV2{GroupV2: &signalpb.GroupV2Record{MasterKey: key}},
		})
	}

	// What signalmeow stores is there (alice by string ACI, bob by binary ACI, the group); what
	// it skips isn't checked: contacts without an ACI or with a malformed one, a group key of the
	// wrong length, other record types.
	healthy := storageUpdate(seededVersion,
		contactRecord(aliceUser, false),
		contact(&signalpb.ContactRecord{AciBinary: bob[:], Blocked: true}),
		contact(&signalpb.ContactRecord{Pni: carolPNI}),
		contact(&signalpb.ContactRecord{E164: "+15550199"}),
		contact(&signalpb.ContactRecord{Aci: "not-a-uuid"}),
		group(storedKey),
		group([]byte("short")),
		record(&signalpb.StorageRecord{Record: &signalpb.StorageRecord_Account{Account: &signalpb.AccountRecord{}}}),
	)

	for _, update := range []*signalmeow.StorageUpdate{nil, storageUpdate(seededVersion), healthy} {
		err := signal.VerifyStorageStored(ctx, client, update)
		if err != nil {
			t.Errorf("VerifyStorageStored(%v) = %v, want nil", update, err)
		}
	}

	// frank has no row and the other group no key: signalmeow's sync didn't store them.
	healthy.NewRecords = append(healthy.NewRecords, contactRecord(frankUser, false), group(unstoredKey))

	err := signal.VerifyStorageStored(ctx, client, healthy)
	if !errors.Is(err, signal.ErrStorageNotStored) || !strings.Contains(err.Error(), "1 contacts, 1 groups missing") {
		t.Errorf("VerifyStorageStored = %v, want ErrStorageNotStored with 1 contact and 1 group", err)
	}

	// Checking created no row for frank.
	wantNoRow(t, dataDir, uuid.MustParse(frankUser))
}

// wantNoRow checks that the store has no recipient row for aci.
func wantNoRow(t *testing.T, dataDir string, aci uuid.UUID) {
	t.Helper()

	withStore(t, dataDir, func(device *mstore.Device, _ *store.Store) {
		loader, ok := device.RecipientStore.(interface {
			LoadRecipientByACI(ctx context.Context, aci uuid.UUID) (*types.Recipient, error)
		})
		if !ok {
			t.Fatal("no LoadRecipientByACI")
		}

		rcpt, err := loader.LoadRecipientByACI(t.Context(), aci)
		if err != nil || rcpt != nil {
			t.Errorf("row of %s = %+v, %v; want none", aci, rcpt, err)
		}
	})
}
