//go:build cgo || libsignal_go

package signal_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/events"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Users of an imported account in the privacy tests, absent from the storage service.
const (
	importedBlocked = "a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1"
	importedSharing = "b2b2b2b2-b2b2-4b2b-8b2b-b2b2b2b2b2b2"
	importedPNI     = "c3c3c3c3-c3c3-4c3c-8c3c-c3c3c3c3c3c3"
	importedE164    = "+15550199"
)

// seedGuards stores privacy guards in the seeded account, as an import would, armed at
// seededVersion.
func seedGuards(t *testing.T, dataDir string, guards ...mstore.PrivacyGuard) {
	t.Helper()

	withStore(t, dataDir, func(device *mstore.Device, _ *store.Store) {
		for i := range guards {
			guards[i].LocalPresent, guards[i].Protected, guards[i].ArmedVersion = true, true, seededVersion
		}

		err := device.PrivacyGuardStore.SeedPrivacyGuards(t.Context(), guards)
		if err != nil {
			t.Fatal(err)
		}
	})
}

func aciGuard(aci string, field mstore.PrivacyField, value bool) mstore.PrivacyGuard {
	return mstore.PrivacyGuard{
		Kind: mstore.PrivacyEntityACI, ID: uuid.MustParse(aci), Field: field, Value: mstore.PrivacyBool(value),
	}
}

// applyStorage applies update like signalmeow's background storage sync, on a separate handle.
func applyStorage(t *testing.T, dataDir string, update *signalmeow.StorageUpdate) {
	t.Helper()

	withStore(t, dataDir, func(device *mstore.Device, _ *store.Store) {
		err := signalmeow.NewClient(device, zerolog.Nop(), func(events.SignalEvent) bool { return true }).
			ApplyStorage(t.Context(), update)
		if err != nil {
			t.Fatal(err)
		}
	})
}

// TestSetBlockedRearmsGuard: the outgoing blocked list carries an imported block the storage
// service doesn't know, and a local unblock re-arms the imported guard, so that a later
// conflicting snapshot keeps the local command's value.
func TestSetBlockedRearmsGuard(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dataDir := seedAccount(t)
	seedContacts(t, dataDir)
	seedGuards(t, dataDir, aciGuard(importedBlocked, mstore.PrivacyFieldBlocked, true))
	withStore(t, dataDir, func(device *mstore.Device, _ *store.Store) {
		err := device.RecipientStore.StoreRecipient(ctx, &types.Recipient{ACI: uuid.MustParse(importedBlocked)})
		if err != nil {
			t.Fatal(err)
		}
	})
	client := openSeeded(t, dataDir)

	var sent []*signalpb.SyncMessage

	// A newer snapshot without overrides: only the storage service's and the imported blocks.
	update := storageUpdate(seededVersion+1, contactRecord(bobUser, true))

	err := signal.ApplyBlocked(ctx, client, update, []signal.Recipient{{ACI: daveUser}}, true, recordSends(&sent))
	if err != nil {
		t.Fatalf("ApplyBlocked: %v", err)
	}

	got, want := sent[0].GetBlocked().GetAcis(), []string{importedBlocked, bobUser, daveUser}
	if !slices.Equal(got, want) {
		t.Errorf("blocked acis = %v, want %v", got, want)
	}

	wantStoredBlocked(t, dataDir, map[string]bool{importedBlocked: true})

	err = signal.ApplyBlocked(ctx, client, update, []signal.Recipient{{ACI: importedBlocked}}, false, recordSends(&sent))
	if err != nil {
		t.Fatalf("ApplyBlocked: %v", err)
	}

	got, want = sent[1].GetBlocked().GetAcis(), []string{bobUser, daveUser}
	if !slices.Equal(got, want) {
		t.Errorf("blocked acis after unblock = %v, want %v", got, want)
	}

	applyStorage(t, dataDir, storageUpdate(seededVersion+2, contactRecord(importedBlocked, true)))
	wantStoredBlocked(t, dataDir, map[string]bool{importedBlocked: false})

	// The phone stored the unblock: the guard is released and later changes apply.
	applyStorage(t, dataDir, storageUpdate(seededVersion+3, contactRecord(importedBlocked, false)))
	applyStorage(t, dataDir, storageUpdate(seededVersion+4, contactRecord(importedBlocked, true)))
	wantStoredBlocked(t, dataDir, map[string]bool{importedBlocked: true})
}

// TestBlockedListGuardedPNI lists a blocked PNI-only user by number, and refuses to send a
// list without one, since the phone would unblock them.
func TestBlockedListGuardedPNI(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dataDir := seedAccount(t)
	seedContacts(t, dataDir)
	seedGuards(t, dataDir, mstore.PrivacyGuard{
		Kind: mstore.PrivacyEntityPNI, ID: uuid.MustParse(importedPNI), Field: mstore.PrivacyFieldBlocked,
		Value: mstore.PrivacyBool(true),
	})

	pniOnly := &types.Recipient{PNI: uuid.MustParse(importedPNI), Blocked: true}
	storePNIOnly := func() {
		withStore(t, dataDir, func(device *mstore.Device, _ *store.Store) {
			err := device.RecipientStore.StoreRecipient(t.Context(), pniOnly)
			if err != nil {
				t.Fatal(err)
			}
		})
	}

	storePNIOnly()

	client := openSeeded(t, dataDir)
	update := storageUpdate(seededVersion+1, contactRecord(bobUser, true))

	var sent []*signalpb.SyncMessage

	err := signal.ApplyBlocked(ctx, client, update, []signal.Recipient{{ACI: daveUser}}, true, recordSends(&sent))
	if !errors.Is(err, signal.ErrBlockedListIncomplete) || len(sent) != 0 {
		t.Fatalf("without a number: %v, %d sent", err, len(sent))
	}

	pniOnly.E164 = importedE164

	storePNIOnly()

	err = signal.ApplyBlocked(ctx, client, update, []signal.Recipient{{ACI: daveUser}}, true, recordSends(&sent))
	if err != nil {
		t.Fatal(err)
	}

	if got := sent[0].GetBlocked().GetNumbers(); !slices.Contains(got, importedE164) {
		t.Errorf("blocked numbers = %v, want %s", got, importedE164)
	}
}

// TestStoryAudiencesUseGuards projects story audiences from effective values: an imported
// block hides a user the storage service shows, and imported profile sharing makes a user
// absent from the storage service a My Story viewer.
func TestStoryAudiencesUseGuards(t *testing.T) {
	t.Parallel()

	dataDir := seedAccount(t)
	seedGuards(t, dataDir,
		aciGuard(aliceUser, mstore.PrivacyFieldBlocked, true),
		aciGuard(importedSharing, mstore.PrivacyFieldWhitelisted, true),
		aciGuard(bobUser, mstore.PrivacyFieldWhitelisted, false),
	)
	client := openSeeded(t, dataDir)

	contact := func(aci string, sharing bool) *signalmeow.DecryptedStorageRecord {
		return &signalmeow.DecryptedStorageRecord{StorageRecord: &signalpb.StorageRecord{
			Record: &signalpb.StorageRecord_Contact{Contact: &signalpb.ContactRecord{Aci: aci, Whitelisted: sharing}},
		}}
	}
	update := storageUpdate(seededVersion+1,
		contact(aliceUser, true), contact(bobUser, true), contact(carolUser, true),
		audienceRecord(signal.MyStoryID, "My Story", true))

	fetch := func(context.Context) (*signalmeow.StorageUpdate, error) { return update, nil }

	got, err := signal.FetchStoryAudiences(t.Context(), client, fetch)
	if err != nil || len(got.Audiences) != 1 {
		t.Fatalf("%+v %v", got, err)
	}

	viewers := make([]string, 0, len(got.Audiences[0].Recipients))
	for _, r := range got.Audiences[0].Recipients {
		viewers = append(viewers, r.ACI)
	}

	slices.Sort(viewers)

	if want := []string{importedSharing, carolUser}; !slices.Equal(viewers, want) {
		t.Errorf("My Story viewers = %v, want %v", viewers, want)
	}
}
