//go:build cgo

package signal_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "go.mau.fi/mautrix-signal/pkg/signalmeow/store"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/types"
)

// Users in the contact tests.
const (
	aliceUser = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	bobUser   = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	carolUser = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	carolPNI  = "c0c0c0c0-c0c0-4c0c-8c0c-c0c0c0c0c0c0"
	daveUser  = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	eveUser   = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	frankUser = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	aliceE164 = "+15550101"
)

// missingRecord is the storage ID of a record that a fetch couldn't read.
const missingRecord = "ZXZl"

// withStore runs fn on a separate handle of the seeded account's database, like another
// process (or signalmeow's storage sync) writing to it.
func withStore(t *testing.T, dataDir string, run func(device *mstore.Device, data *store.Store)) {
	t.Helper()

	dir, err := store.OpenDir(dataDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	data, err := dir.OpenAccount(t.Context(), seededACI, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}

	defer data.Close()

	device, err := data.Devices.DeviceByACI(t.Context(), uuid.MustParse(seededACI))
	if err != nil || device == nil {
		t.Fatalf("load device: %v", err)
	}

	run(device, data)
}

// storedBlocked reads the blocked flag of aci from the store, bypassing the overrides.
func storedBlocked(t *testing.T, dataDir string, aci uuid.UUID) bool {
	t.Helper()

	var blocked bool

	withStore(t, dataDir, func(device *mstore.Device, _ *store.Store) {
		loader, ok := device.RecipientStore.(interface {
			LoadRecipientByACI(ctx context.Context, aci uuid.UUID) (*types.Recipient, error)
		})
		if !ok {
			t.Fatal("no LoadRecipientByACI")
		}

		rcpt, err := loader.LoadRecipientByACI(t.Context(), aci)
		if err != nil {
			t.Fatal(err)
		}

		blocked = rcpt != nil && rcpt.Blocked
	})

	return blocked
}

// seededVersion is the storage service's manifest version the seeded block overrides were made
// against.
const seededVersion = 5

// seedContacts stores ourselves, alice (named, accepted), bob (blocked, no name), carol (only a
// nickname and a PNI) and dave (nothing), plus block overrides made against seededVersion: alice
// blocked, eve (not in the store) blocked, and an expired one for frank.
func seedContacts(t *testing.T, dataDir string) {
	t.Helper()

	withStore(t, dataDir, func(device *mstore.Device, data *store.Store) {
		ctx := t.Context()

		for _, rcpt := range []*types.Recipient{
			{ACI: device.ACI, E164: seededNumber, Profile: types.Profile{Name: "Me"}},
			{
				ACI: uuid.MustParse(aliceUser), E164: aliceE164, ContactName: "Alice Smith", Profile: types.Profile{Name: "Ali"},
				Whitelisted: new(true),
			},
			{ACI: uuid.MustParse(bobUser), Blocked: true},
			{ACI: uuid.MustParse(carolUser), PNI: uuid.MustParse(carolPNI), Nickname: "carol"},
			{ACI: uuid.MustParse(daveUser)},
		} {
			err := device.RecipientStore.StoreRecipient(ctx, rcpt)
			if err != nil {
				t.Fatalf("store recipient: %v", err)
			}
		}

		now := time.Now()

		for _, override := range []store.BlockOverride{
			{ACI: aliceUser, Blocked: true, SetAt: now, StorageVersion: seededVersion},
			{ACI: eveUser, Blocked: true, SetAt: now, StorageVersion: seededVersion},
			{
				ACI: frankUser, Blocked: true, SetAt: now.Add(-signal.BlockOverrideTTL - time.Hour),
				StorageVersion: seededVersion,
			},
		} {
			err := data.SetBlockOverride(ctx, override)
			if err != nil {
				t.Fatal(err)
			}
		}
	})
}

func openSeeded(t *testing.T, dataDir string) signal.Client {
	t.Helper()

	client, err := signal.Open(t.Context(), signal.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

func TestContactsFromStore(t *testing.T) {
	t.Parallel()

	dataDir := seedAccount(t)
	seedContacts(t, dataDir)
	client := openSeeded(t, dataDir)

	contacts, err := client.Contacts(t.Context())
	if err != nil {
		t.Fatalf("Contacts: %v", err)
	}

	slices.SortFunc(contacts, func(a, b signal.Contact) int { return strings.Compare(a.ACI, b.ACI) })

	// Without us, carol (signalmeow lists no one with only a nickname) and dave (nothing known).
	// alice shows as blocked: her override is pending.
	want := []signal.Contact{
		{
			Recipient:   signal.Recipient{ACI: aliceUser, Number: aliceE164},
			ContactName: "Alice Smith", ProfileName: "Ali", Blocked: true, Accepted: new(true),
		},
		{Recipient: signal.Recipient{ACI: bobUser}, Blocked: true},
	}

	if len(contacts) != len(want) {
		t.Fatalf("Contacts = %+v, want %+v", contacts, want)
	}

	for i := range want {
		if !sameContact(contacts[i], want[i]) {
			t.Errorf("contact %d = %+v, want %+v", i, contacts[i], want[i])
		}
	}
}

func sameContact(a, b signal.Contact) bool {
	accepted := func(c signal.Contact) string {
		if c.Accepted == nil {
			return "?"
		}

		return map[bool]string{true: "y", false: "n"}[*c.Accepted]
	}

	a.Accepted, b.Accepted = nil, nil

	return a == b && accepted(a) == accepted(b)
}

func TestContactLookup(t *testing.T) {
	t.Parallel()

	dataDir := seedAccount(t)
	seedContacts(t, dataDir)
	client := openSeeded(t, dataDir)

	tests := []struct {
		rcpt signal.Recipient
		want string // ACI
	}{
		{signal.Recipient{Number: aliceE164}, aliceUser},
		{signal.Recipient{ACI: carolUser}, carolUser},
		{signal.Recipient{PNI: carolPNI}, carolUser},
		{signal.Recipient{ACI: seededACI}, seededACI},
	}

	for _, test := range tests {
		got, err := client.Contact(t.Context(), test.rcpt)
		if err != nil || got.ACI != test.want {
			t.Errorf("Contact(%+v) = %+v, %v; want %s", test.rcpt, got, err, test.want)
		}
	}

	for _, rcpt := range []signal.Recipient{{Number: "+15550199"}, {ACI: frankUser}, {Username: "carol.01"}} {
		_, err := client.Contact(t.Context(), rcpt)
		if !errors.Is(err, signal.ErrUnknownContact) {
			t.Errorf("Contact(%+v) error = %v, want ErrUnknownContact", rcpt, err)
		}
	}

	// Looking up doesn't create rows.
	_, err := client.Contact(t.Context(), signal.Recipient{ACI: frankUser})
	if !errors.Is(err, signal.ErrUnknownContact) {
		t.Errorf("second lookup of frank: %v", err)
	}
}

var errStorageDown = errors.New("storage service unreachable")

// unblockInStore clears the blocked flag of aci in the store, as signalmeow's storage sync does.
func unblockInStore(t *testing.T, dataDir string, aci uuid.UUID) {
	t.Helper()

	setStoredBlocked(t, dataDir, aci, false)
}

// setStoredBlocked sets the blocked flag of aci in the store, bypassing the overrides.
func setStoredBlocked(t *testing.T, dataDir string, aci uuid.UUID, blocked bool) {
	t.Helper()

	withStore(t, dataDir, func(device *mstore.Device, _ *store.Store) {
		_, err := device.RecipientStore.LoadAndUpdateRecipient(t.Context(), aci, uuid.Nil,
			func(rcpt *types.Recipient) (bool, error) {
				rcpt.Blocked = blocked

				return true, nil
			})
		if err != nil {
			t.Fatal(err)
		}
	})
}

// wantStoredBlocked checks the blocked flag of each user in the store.
func wantStoredBlocked(t *testing.T, dataDir string, want map[string]bool) {
	t.Helper()

	for aci, blocked := range want {
		if got := storedBlocked(t, dataDir, uuid.MustParse(aci)); got != blocked {
			t.Errorf("stored blocked state of %s = %v, want %v", aci, got, blocked)
		}
	}
}

// contactRecord is a storage service contact record of the user aci.
func contactRecord(aci string, blocked bool) *signalmeow.DecryptedStorageRecord {
	return &signalmeow.DecryptedStorageRecord{StorageRecord: &signalpb.StorageRecord{
		Record: &signalpb.StorageRecord_Contact{Contact: &signalpb.ContactRecord{Aci: aci, Blocked: blocked}},
	}}
}

// storageUpdate is a complete fetch of the storage service at the manifest version.
func storageUpdate(version uint64, records ...*signalmeow.DecryptedStorageRecord) *signalmeow.StorageUpdate {
	return &signalmeow.StorageUpdate{Version: version, NewRecords: records}
}

// fakeStorage answers the fetches of the storage service like the server: nothing (204) while
// the manifest has the version asked about.
type fakeStorage struct {
	update *signalmeow.StorageUpdate
	err    error
	asked  []uint64
}

func (s *fakeStorage) fetch(_ context.Context, since uint64) (*signalmeow.StorageUpdate, error) {
	s.asked = append(s.asked, since)

	if s.err != nil {
		return nil, s.err
	}

	if s.update.Version == since {
		return nil, nil //nolint:nilnil // signalmeow's answer to the server's 204
	}

	return s.update, nil
}

// recordSends returns a send function for ApplyBlocked that records the messages in sent.
func recordSends(sent *[]*signalpb.SyncMessage) func(*signalpb.SyncMessage) error {
	return func(msg *signalpb.SyncMessage) error {
		*sent = append(*sent, msg)

		return nil
	}
}

func TestBlockOverridesSettle(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dataDir := seedAccount(t)
	seedContacts(t, dataDir)
	client := openSeeded(t, dataDir)
	alice, eve := uuid.MustParse(aliceUser), uuid.MustParse(eveUser)

	// As on connect: nothing fetched. The overrides are applied to the store, eve gets a row,
	// and frank's expired override goes.
	signal.SettleOverrides(ctx, client, nil)
	wantOverrides(t, client, alice, eve)
	wantStoredBlocked(t, dataDir, map[string]bool{aliceUser: true, eveUser: true})

	// Our own storage sync finds the version the overrides were made against: the phone hasn't
	// written our change yet, so the sync's undoing of eve's block is undone again.
	unblockInStore(t, dataDir, eve)
	signal.SettleOverrides(ctx, client, storageUpdate(seededVersion, contactRecord(aliceUser, false)))
	wantOverrides(t, client, alice, eve)
	wantStoredBlocked(t, dataDir, map[string]bool{aliceUser: true, eveUser: true})

	// An older version (from a fetch that overtook a later one) changes nothing either.
	signal.SettleOverrides(ctx, client, storageUpdate(seededVersion-1))
	wantOverrides(t, client, alice, eve)

	// A later version: the phone has written the storage service since, and it wins, whether it
	// has our change (alice) or not (eve, unblocked on the phone again).
	signal.SettleOverrides(ctx, client, storageUpdate(seededVersion+1,
		contactRecord(aliceUser, true), contactRecord(eveUser, false)))
	wantOverrides(t, client)
	wantStoredBlocked(t, dataDir, map[string]bool{aliceUser: true, eveUser: false})
}

func TestBlockOverridesIncompleteFetch(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dataDir := seedAccount(t)
	seedContacts(t, dataDir)
	client := openSeeded(t, dataDir)

	signal.SettleOverrides(ctx, client, nil)

	partial := &signalmeow.StorageUpdate{
		Version:        seededVersion + 1,
		NewRecords:     []*signalmeow.DecryptedStorageRecord{contactRecord(aliceUser, false)},
		MissingRecords: []string{missingRecord},
	}

	// A later version with records missing ends alice's override (her record was read, and the
	// store takes its state), but not eve's: that fetch doesn't know her state.
	signal.SettleOverrides(ctx, client, partial)
	wantOverrides(t, client, uuid.MustParse(eveUser))
	wantStoredBlocked(t, dataDir, map[string]bool{aliceUser: false, eveUser: true})

	// Nor is eve's override imposed on the store again: the phone may have unblocked her.
	unblockInStore(t, dataDir, uuid.MustParse(eveUser))
	signal.SettleOverrides(ctx, client, partial)
	wantOverrides(t, client, uuid.MustParse(eveUser))
	wantStoredBlocked(t, dataDir, map[string]bool{eveUser: false})

	// A later fetch that knows eve's state ends her override, too, and the store takes it.
	signal.SettleOverrides(ctx, client, storageUpdate(seededVersion+2, contactRecord(eveUser, true)))
	wantOverrides(t, client)
	wantStoredBlocked(t, dataDir, map[string]bool{eveUser: true})
}

func TestBlockOverridesExpire(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dataDir := seedAccount(t)
	seedContacts(t, dataDir)

	// frank's override (expired) was applied to the store back then.
	frank, dave := uuid.MustParse(frankUser), uuid.MustParse(daveUser)
	setStoredBlocked(t, dataDir, frank, true)

	client := openSeeded(t, dataDir)

	// A fetch that read frank's record: the expired override ends, and the store takes the
	// storage service's state. The others are re-applied (their version is still current).
	signal.SettleOverrides(ctx, client, &signalmeow.StorageUpdate{
		Version:        seededVersion,
		NewRecords:     []*signalmeow.DecryptedStorageRecord{contactRecord(frankUser, false)},
		MissingRecords: []string{missingRecord},
	})
	wantOverrides(t, client, uuid.MustParse(aliceUser), uuid.MustParse(eveUser))
	wantStoredBlocked(t, dataDir, map[string]bool{frankUser: false, aliceUser: true, eveUser: true})

	// Without a fetch that knows, an expired override still ends; the store keeps its state
	// until signalmeow's next storage sync rewrites it from the contact record.
	withStore(t, dataDir, func(_ *mstore.Device, data *store.Store) {
		err := data.SetBlockOverride(ctx, store.BlockOverride{
			ACI: daveUser, Blocked: true, SetAt: time.Now().Add(-signal.BlockOverrideTTL - time.Hour),
			StorageVersion: seededVersion,
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	setStoredBlocked(t, dataDir, dave, true)

	signal.SettleOverrides(ctx, client, nil)
	wantOverrides(t, client, uuid.MustParse(aliceUser), uuid.MustParse(eveUser))
	wantStoredBlocked(t, dataDir, map[string]bool{daveUser: true})
}

// TestBlockOverridesBackgroundSync follows a block through signalmeow's background storage syncs,
// which report only the contacts whose record changed, not the manifest version.
func TestBlockOverridesBackgroundSync(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dataDir := seedAccount(t)
	seedContacts(t, dataDir)
	client := openSeeded(t, dataDir)
	alice, eve := uuid.MustParse(aliceUser), uuid.MustParse(eveUser)

	signal.SettleOverrides(ctx, client, nil)

	storage := &fakeStorage{update: storageUpdate(seededVersion)}

	// A sync that changed only dave leaves the overridden users alone: no need to ask.
	signal.StorageSynced(ctx, client, []*types.Recipient{{ACI: uuid.MustParse(daveUser)}}, storage.fetch)

	if len(storage.asked) != 0 {
		t.Errorf("fetched the storage service since %v for an unrelated change", storage.asked)
	}

	// The sync stored eve as not blocked before the phone wrote our change: the storage service
	// still has the overrides' version, so eve's block is re-applied.
	unblockInStore(t, dataDir, eve)
	signal.StorageSynced(ctx, client, []*types.Recipient{{ACI: eve}}, storage.fetch)

	if want := []uint64{seededVersion}; !slices.Equal(storage.asked, want) {
		t.Errorf("fetched since %v, want %v", storage.asked, want)
	}

	wantOverrides(t, client, alice, eve)
	wantStoredBlocked(t, dataDir, map[string]bool{eveUser: true})

	// If the storage service can't be asked, nothing later has been seen: the overrides stay.
	storage.err = errStorageDown

	unblockInStore(t, dataDir, eve)
	signal.StorageSynced(ctx, client, []*types.Recipient{{ACI: eve}}, storage.fetch)
	wantOverrides(t, client, alice, eve)
	wantStoredBlocked(t, dataDir, map[string]bool{eveUser: true})

	// The phone applied our list and wrote the storage service; eve is blocked there as here, so
	// signalmeow reports no change. Then the user unblocks eve on the phone, and that sync
	// changes her: the phone wins, and the overrides end.
	storage.err = nil
	storage.update = storageUpdate(seededVersion+2, contactRecord(aliceUser, true), contactRecord(eveUser, false))

	unblockInStore(t, dataDir, eve)
	signal.StorageSynced(ctx, client, []*types.Recipient{{ACI: eve}}, storage.fetch)
	wantOverrides(t, client)
	wantStoredBlocked(t, dataDir, map[string]bool{aliceUser: true, eveUser: false})

	// The next block doesn't send eve's old block along.
	var sent []*signalpb.SyncMessage

	err := signal.ApplyBlocked(ctx, client, storage.update, []signal.Recipient{{ACI: carolUser}}, true,
		recordSends(&sent))
	if err != nil {
		t.Fatalf("ApplyBlocked: %v", err)
	}

	if len(sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(sent))
	}

	if got, want := sent[0].GetBlocked().GetAcis(), []string{aliceUser, carolUser}; !slices.Equal(got, want) {
		t.Errorf("blocked acis = %v, want %v", got, want)
	}
}

func TestApplyBlocked(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dataDir := seedAccount(t)
	seedContacts(t, dataDir)
	client := openSeeded(t, dataDir)

	blockedKey := bytes.Repeat([]byte{1}, libsignalgo.GroupMasterKeyLength)
	otherKey := bytes.Repeat([]byte{2}, libsignalgo.GroupMasterKeyLength)

	var sent []*signalpb.SyncMessage

	// At the overrides' version, eve's pending block goes along (alice's is in the storage
	// service by now). carol gets an override; bob is blocked there already.
	err := signal.ApplyBlocked(ctx, client, blockedUpdate(seededVersion, blockedKey, otherKey),
		[]signal.Recipient{{ACI: carolUser}, {ACI: bobUser}}, true, recordSends(&sent))
	if err != nil {
		t.Fatalf("ApplyBlocked: %v", err)
	}

	msg := sent[0].GetBlocked()
	wantBlockedGroup(t, msg, blockedKey)

	if want := []string{aliceUser, bobUser, carolUser, eveUser}; !slices.Equal(msg.GetAcis(), want) {
		t.Errorf("blocked acis = %v, want %v", msg.GetAcis(), want)
	}

	wantOverrides(t, client, uuid.MustParse(aliceUser), uuid.MustParse(carolUser), uuid.MustParse(eveUser))
	wantOverrideVersion(t, client, carolUser, seededVersion)
	wantStoredBlocked(t, dataDir, map[string]bool{bobUser: true, carolUser: true, eveUser: true})

	// The storage service has changed since: the phone wrote it, so what it says wins over the
	// pending overrides, which neither go along nor survive.
	err = signal.ApplyBlocked(ctx, client, storageUpdate(seededVersion+1, contactRecord(bobUser, true)),
		[]signal.Recipient{{ACI: daveUser}}, true, recordSends(&sent))
	if err != nil {
		t.Fatalf("ApplyBlocked: %v", err)
	}

	if got, want := sent[1].GetBlocked().GetAcis(), []string{bobUser, daveUser}; !slices.Equal(got, want) {
		t.Errorf("blocked acis = %v, want %v", got, want)
	}

	wantOverrides(t, client, uuid.MustParse(daveUser))
	wantOverrideVersion(t, client, daveUser, seededVersion+1)
	wantStoredBlocked(t, dataDir, map[string]bool{
		aliceUser: false, bobUser: true, carolUser: false, daveUser: true, eveUser: false,
	})
}

func TestApplyBlockedIncompleteList(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dataDir := seedAccount(t)
	seedContacts(t, dataDir)
	client := openSeeded(t, dataDir)

	record := func(r *signalpb.StorageRecord) *signalmeow.DecryptedStorageRecord {
		return &signalmeow.DecryptedStorageRecord{StorageRecord: r}
	}

	tests := []struct {
		name   string
		update *signalmeow.StorageUpdate
	}{
		{"no manifest", nil},
		{"missing records", &signalmeow.StorageUpdate{
			Version:        seededVersion,
			NewRecords:     []*signalmeow.DecryptedStorageRecord{contactRecord(bobUser, true)},
			MissingRecords: []string{missingRecord},
		}},
		{"blocked contact with neither ACI nor number", storageUpdate(seededVersion, record(&signalpb.StorageRecord{
			Record: &signalpb.StorageRecord_Contact{Contact: &signalpb.ContactRecord{Pni: carolPNI, Blocked: true}},
		}))},
		{"blocked group without a valid master key", storageUpdate(seededVersion, record(&signalpb.StorageRecord{
			Record: &signalpb.StorageRecord_GroupV2{GroupV2: &signalpb.GroupV2Record{MasterKey: []byte{1, 2}, Blocked: true}},
		}))},
	}

	for _, test := range tests {
		var sent []*signalpb.SyncMessage

		err := signal.ApplyBlocked(ctx, client, test.update, []signal.Recipient{{ACI: daveUser}}, true,
			recordSends(&sent))
		if !errors.Is(err, signal.ErrBlockedListIncomplete) {
			t.Errorf("%s: error = %v, want ErrBlockedListIncomplete", test.name, err)
		}

		if len(sent) != 0 {
			t.Errorf("%s: sent %v", test.name, sent)
		}

		// Nothing changed: no override for dave, and the store doesn't have him blocked.
		wantOverrides(t, client, uuid.MustParse(aliceUser), uuid.MustParse(eveUser), uuid.MustParse(frankUser))
		wantStoredBlocked(t, dataDir, map[string]bool{daveUser: false})
	}
}

// wantOverrideVersion checks the storage service version of aci's override.
func wantOverrideVersion(t *testing.T, client signal.Client, aci string, want uint64) {
	t.Helper()

	overrides, err := signal.BlockOverrides(t.Context(), client)
	if err != nil {
		t.Fatal(err)
	}

	for _, override := range overrides {
		if override.ACI == aci && override.StorageVersion != want {
			t.Errorf("override of %s has storage version %d, want %d", aci, override.StorageVersion, want)
		}
	}
}

func wantOverrides(t *testing.T, client signal.Client, acis ...uuid.UUID) {
	t.Helper()

	overrides, err := signal.BlockOverrides(t.Context(), client)
	if err != nil {
		t.Fatal(err)
	}

	got := make([]string, 0, len(overrides))
	for _, override := range overrides {
		got = append(got, override.ACI)
	}

	want := make([]string, 0, len(acis))
	for _, aci := range acis {
		want = append(want, aci.String())
	}

	if !slices.Equal(got, want) {
		t.Errorf("overrides = %v, want %v", got, want)
	}
}

// blockedUpdate is a storage service update at the manifest version with alice (blocked, with
// number and time), bob (blocked), carol (not blocked), a blocked number without ACI, and two
// groups (one blocked).
func blockedUpdate(version uint64, blockedKey, otherKey []byte) *signalmeow.StorageUpdate {
	alice := uuid.MustParse(aliceUser)

	contact := func(c *signalpb.ContactRecord) *signalmeow.DecryptedStorageRecord {
		return &signalmeow.DecryptedStorageRecord{StorageRecord: &signalpb.StorageRecord{
			Record: &signalpb.StorageRecord_Contact{Contact: c},
		}}
	}
	group := func(key []byte, blocked bool) *signalmeow.DecryptedStorageRecord {
		return &signalmeow.DecryptedStorageRecord{StorageRecord: &signalpb.StorageRecord{
			Record: &signalpb.StorageRecord_GroupV2{GroupV2: &signalpb.GroupV2Record{MasterKey: key, Blocked: blocked}},
		}}
	}

	return storageUpdate(version,
		contact(&signalpb.ContactRecord{AciBinary: alice[:], E164: aliceE164, Blocked: true, BlockedAtTimestamp: 1000}),
		contact(&signalpb.ContactRecord{Aci: bobUser, Blocked: true}),
		contact(&signalpb.ContactRecord{Aci: carolUser, E164: "+15550103"}),
		contact(&signalpb.ContactRecord{E164: "+15550199", Blocked: true}),
		group(blockedKey, true),
		group(otherKey, false),
	)
}

func TestBlockedSyncMessage(t *testing.T) {
	t.Parallel()

	blockedKey := bytes.Repeat([]byte{1}, libsignalgo.GroupMasterKeyLength)
	otherKey := bytes.Repeat([]byte{2}, libsignalgo.GroupMasterKeyLength)

	list, err := signal.BlockedFromStorage(blockedUpdate(seededVersion, blockedKey, otherKey))
	if err != nil {
		t.Fatal(err)
	}

	// Unblock alice (and her number), block carol.
	carol := uuid.MustParse(carolUser)
	list.Set(uuid.MustParse(aliceUser), "", false, time.UnixMilli(3000))
	list.Set(carol, "+15550103", true, time.UnixMilli(2000))

	msg := list.SyncMessage().GetBlocked()
	wantBlockedGroup(t, msg, blockedKey)

	if want := []string{bobUser, carolUser}; !slices.Equal(msg.GetAcis(), want) ||
		len(msg.GetAcisBinary()) != 2 || !bytes.Equal(msg.GetAcisBinary()[1], carol[:]) {
		t.Errorf("acis = %v, acisBinary = %x; want %v", msg.GetAcis(), msg.GetAcisBinary(), want)
	}

	if want := []string{"+15550103", "+15550199"}; !slices.Equal(msg.GetNumbers(), want) {
		t.Errorf("numbers = %v, want %v", msg.GetNumbers(), want)
	}

	wantBlockedTimes(t, msg)
}

// wantBlockedTimes checks the current fields of msg: bob without a time, carol with hers.
func wantBlockedTimes(t *testing.T, msg *signalpb.SyncMessage_Blocked) {
	t.Helper()

	blockedACIs, blockedE164s := msg.GetBlockedAcis(), msg.GetBlockedE164S()
	if len(blockedACIs) != 2 || blockedACIs[0].Timestamp != nil || blockedACIs[1].GetTimestamp() != 2000 ||
		len(blockedE164s) != 2 || blockedE164s[0].GetE164() != "+15550103" {
		t.Errorf("blockedAcis = %v, blockedE164s = %v; want bob without and carol with a timestamp",
			blockedACIs, blockedE164s)
	}
}

// wantBlockedGroup checks that msg blocks only the group with the master key.
func wantBlockedGroup(t *testing.T, msg *signalpb.SyncMessage_Blocked, key []byte) {
	t.Helper()

	groupID, err := libsignalgo.GroupMasterKey(key).GroupIdentifier()
	if err != nil {
		t.Fatal(err)
	}

	groups := msg.GetGroupIds()
	if len(groups) != 1 || !bytes.Equal(groups[0], groupID[:]) || len(msg.GetBlockedGroups()) != 1 {
		t.Errorf("groupIds = %x, want only %x", groups, groupID[:])
	}
}
