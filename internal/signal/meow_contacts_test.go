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

// seedContacts stores ourselves, alice (named, accepted), bob (blocked, no name), carol (only a
// nickname and a PNI) and dave (nothing), plus block overrides: alice blocked, eve (not in the
// store) blocked, and an expired one for frank.
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
			{ACI: aliceUser, Blocked: true, SetAt: now},
			{ACI: eveUser, Blocked: true, SetAt: now},
			{ACI: frankUser, Blocked: true, SetAt: now.Add(-signal.BlockOverrideTTL - time.Hour)},
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

func TestBlockOverridesSettle(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dataDir := seedAccount(t)
	seedContacts(t, dataDir)
	client := openSeeded(t, dataDir)

	// As on connect: the storage state is unknown. The overrides are applied to the store,
	// eve gets a row, and frank's expired override goes.
	signal.SettleOverrides(ctx, client, nil)
	wantOverrides(t, client, uuid.MustParse(aliceUser), uuid.MustParse(eveUser))

	if !storedBlocked(t, dataDir, uuid.MustParse(aliceUser)) || !storedBlocked(t, dataDir, uuid.MustParse(eveUser)) {
		t.Error("overrides not applied to the store")
	}

	// A storage sync that agrees on alice ends her override; eve's state is unknown.
	signal.SettleOverrides(ctx, client, map[string]bool{aliceUser: true})
	wantOverrides(t, client, uuid.MustParse(eveUser))

	// signalmeow stores eve as not blocked from the storage service: the override puts it back.
	withStore(t, dataDir, func(device *mstore.Device, _ *store.Store) {
		err := device.RecipientStore.StoreRecipient(ctx, &types.Recipient{ACI: uuid.MustParse(eveUser)})
		if err != nil {
			t.Fatal(err)
		}
	})

	signal.StorageSynced(ctx, client, []*types.Recipient{{ACI: uuid.MustParse(eveUser), Blocked: false}})
	wantOverrides(t, client, uuid.MustParse(eveUser))

	if !storedBlocked(t, dataDir, uuid.MustParse(eveUser)) {
		t.Error("eve's block was not re-applied")
	}

	// Once the storage service has it, the override ends.
	signal.StorageSynced(ctx, client, []*types.Recipient{{ACI: uuid.MustParse(eveUser), Blocked: true}})
	wantOverrides(t, client)
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

// blockedUpdate is a storage service update with alice (blocked, with number and time), bob
// (blocked), carol (not blocked), a blocked number without ACI, and two groups (one blocked).
func blockedUpdate(blockedKey, otherKey []byte) *signalmeow.StorageUpdate {
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

	return &signalmeow.StorageUpdate{NewRecords: []*signalmeow.DecryptedStorageRecord{
		contact(&signalpb.ContactRecord{AciBinary: alice[:], E164: aliceE164, Blocked: true, BlockedAtTimestamp: 1000}),
		contact(&signalpb.ContactRecord{Aci: bobUser, Blocked: true}),
		contact(&signalpb.ContactRecord{Aci: carolUser, E164: "+15550103"}),
		contact(&signalpb.ContactRecord{E164: "+15550199", Blocked: true}),
		group(blockedKey, true),
		group(otherKey, false),
	}}
}

func TestBlockedSyncMessage(t *testing.T) {
	t.Parallel()

	blockedKey := bytes.Repeat([]byte{1}, libsignalgo.GroupMasterKeyLength)
	otherKey := bytes.Repeat([]byte{2}, libsignalgo.GroupMasterKeyLength)

	list, err := signal.BlockedFromStorage(blockedUpdate(blockedKey, otherKey))
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

func TestBlockedSyncMessageEmpty(t *testing.T) {
	t.Parallel()

	// No manifest: nothing blocked.
	empty, err := signal.BlockedFromStorage(nil)
	if err != nil || len(empty.SyncMessage().GetBlocked().GetAcis()) != 0 {
		t.Errorf("empty list: %v", err)
	}
}
