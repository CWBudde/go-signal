//go:build cgo || libsignal_go

package signal_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/events"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/types"
	"github.com/google/uuid"
)

func identityStorage(t *testing.T, version uint64, key *libsignalgo.IdentityKey,
	state signalpb.ContactRecord_IdentityState,
) *signalmeow.StorageUpdate {
	t.Helper()
	contact := &signalpb.ContactRecord{
		Aci: aliceACI, Pni: aliceACI, IdentityKey: identityBytes(t, key), IdentityState: state,
	}

	return &signalmeow.StorageUpdate{Version: version, NewRecords: []*signalmeow.DecryptedStorageRecord{
		{StorageRecord: &signalpb.StorageRecord{Record: &signalpb.StorageRecord_Contact{Contact: contact}}},
	}}
}

func applyIdentityStorage(t *testing.T, env *trustEnv, update *signalmeow.StorageUpdate) {
	t.Helper()

	err := signal.ApplyContactIdentityStorage(t.Context(), env.client, update)
	if err != nil {
		t.Fatal(err)
	}
}

//nolint:cyclop // verify key history and PNI isolation through a replacement
func TestContactIdentityStorageImportAndReplacement(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	key, next := newIdentityKey(t), newIdentityKey(t)

	pni := libsignalgo.NewPNIServiceID(uuid.MustParse(aliceACI))

	_, pniErr := env.device.ACIIdentityStore.SaveIdentityKey(t.Context(), pni, key)
	if pniErr != nil {
		t.Fatal(pniErr)
	}

	applyIdentityStorage(t, env, identityStorage(t, 10, key, signalpb.ContactRecord_VERIFIED))

	if got := env.alice(t); got.Trust != signal.TrustVerified {
		t.Fatalf("import=%+v", got)
	}

	applyIdentityStorage(t, env, identityStorage(t, 11, next, signalpb.ContactRecord_DEFAULT))

	got := env.alice(t)
	if got.Trust != signal.TrustUnverified {
		t.Fatalf("replacement=%+v", got)
	}

	rec, err := env.data.Identity(t.Context(), aliceACI)
	if err != nil || !bytes.Equal(rec.Key, identityBytes(t, next)) ||
		!bytes.Equal(rec.PreviousKey, identityBytes(t, key)) || rec.ChangedAt.IsZero() {
		t.Fatalf("history=%+v,%v", rec, err)
	}

	pniRec, err := env.data.Identity(t.Context(), pni.String())
	if err != nil || pniRec.Trust != signal.TrustUnverified.String() || !bytes.Equal(pniRec.Key, identityBytes(t, key)) {
		t.Fatalf("PNI=%+v,%v", pniRec, err)
	}
}

func TestContactIdentityStorageLocalDecisionSurvivesDelivery(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	key := newIdentityKey(t)
	applyIdentityStorage(t, env, identityStorage(t, 10, key, signalpb.ContactRecord_DEFAULT))

	number, err := env.client.SafetyNumber(t.Context(), signal.Recipient{ACI: aliceACI})
	if err != nil {
		t.Fatal(err)
	}

	_, trustErr := env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: aliceACI}, number.Number)
	if trustErr != nil {
		t.Fatal(trustErr)
	}

	err = signal.FlushIdentityVerification(t.Context(), env.client,
		func(context.Context, *signalpb.SyncMessage) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	applyIdentityStorage(t, env, identityStorage(t, 11, key, signalpb.ContactRecord_DEFAULT))

	if env.alice(t).Trust != signal.TrustVerified {
		t.Fatal("stale storage downgraded delivered local verification")
	}

	applyIdentityStorage(t, env, identityStorage(t, 12, key, signalpb.ContactRecord_VERIFIED))
	applyIdentityStorage(t, env, identityStorage(t, 13, key, signalpb.ContactRecord_DEFAULT))

	if env.alice(t).Trust != signal.TrustUnverified {
		t.Fatal("changed remote decision did not reconcile after alignment")
	}
}

func TestContactIdentityStorageOldManifestAndFirstConflict(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	key, stale := newIdentityKey(t), newIdentityKey(t)
	env.saveAlice(t, key)
	applyIdentityStorage(t, env, identityStorage(t, 10, stale, signalpb.ContactRecord_VERIFIED))

	if rec, _ := env.data.Identity(t.Context(), aliceACI); !bytes.Equal(rec.Key, identityBytes(t, key)) {
		t.Fatal("first snapshot replaced known key")
	}

	applyIdentityStorage(t, env, identityStorage(t, 11, key, signalpb.ContactRecord_DEFAULT))
	applyIdentityStorage(t, env, identityStorage(t, 11, stale, signalpb.ContactRecord_VERIFIED))
	applyIdentityStorage(t, env, identityStorage(t, 9, stale, signalpb.ContactRecord_VERIFIED))

	if rec, _ := env.data.Identity(t.Context(), aliceACI); !bytes.Equal(rec.Key, identityBytes(t, key)) {
		t.Fatal("old manifest restored stale key")
	}

	applyIdentityStorage(t, env, identityStorage(t, 12, stale, signalpb.ContactRecord_UNVERIFIED))

	if env.alice(t).Trust != signal.TrustUntrusted {
		t.Fatal("remote changed identity state missing")
	}
}

func TestContactIdentityStorageRollbackAndRetry(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	key := newIdentityKey(t)
	database := timerSQL(t, env.dataDir)
	execTimerSQL(t, database, `CREATE TRIGGER fail_storage_identity BEFORE INSERT ON gosignal_identities
 BEGIN SELECT RAISE(ABORT,'storage identity failure'); END`)

	update := identityStorage(t, 10, key, signalpb.ContactRecord_VERIFIED)

	writeErr := signal.ApplyContactIdentityStorage(t.Context(), env.client, update)
	if writeErr == nil {
		t.Fatal("write failure hidden")
	}

	keys, err := env.data.StoredIdentityKeys(t.Context(), seededACI)
	if err != nil || keys[aliceACI] != nil {
		t.Fatalf("protocol write escaped rollback=%v,%v", keys[aliceACI], err)
	}

	execTimerSQL(t, database, `DROP TRIGGER fail_storage_identity`)
	applyIdentityStorage(t, env, update)

	if env.alice(t).Trust != signal.TrustVerified {
		t.Fatal("same manifest retry lost")
	}
}

func TestContactIdentityStorageIncomingDecisionAndKeyChange(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	key, next := newIdentityKey(t), newIdentityKey(t)
	applyIdentityStorage(t, env, identityStorage(t, 10, key, signalpb.ContactRecord_VERIFIED))
	signal.ConnectOffline(t.Context(), env.client, signal.SendOnly())

	if !signal.Handle(env.client, verificationEvent(t, key, signalpb.Verified_DEFAULT)) {
		t.Fatal("incoming failed")
	}

	applyIdentityStorage(t, env, identityStorage(t, 11, key, signalpb.ContactRecord_VERIFIED))

	if env.alice(t).Trust != signal.TrustUnverified {
		t.Fatal("storage undid incoming downgrade")
	}

	env.saveAlice(t, next)
	applyIdentityStorage(t, env, identityStorage(t, 12, key, signalpb.ContactRecord_VERIFIED))

	rec, err := env.data.Identity(t.Context(), aliceACI)
	if err != nil || !bytes.Equal(rec.Key, identityBytes(t, next)) ||
		rec.Trust != signal.TrustUntrusted.String() || !rec.PendingEvent {
		t.Fatalf("local replacement lost=%+v,%v", rec, err)
	}
}

func TestContactIdentityStorageDuplicateConflict(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name             string
		sameKey, reverse bool
	}{
		{"different key", false, false},
		{"different key reversed", false, true},
		{"different trust", true, false},
		{"different trust reversed", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			env := newTrustEnv(t)

			key, next := newIdentityKey(t), newIdentityKey(t)
			if test.sameKey {
				next = key
			}

			first := identityStorage(t, 10, key, signalpb.ContactRecord_VERIFIED)

			second := identityStorage(t, 10, next, signalpb.ContactRecord_DEFAULT)
			if test.reverse {
				first, second = second, first
			}

			first.NewRecords = append(first.NewRecords, second.NewRecords...)

			rejectedErr := signal.ApplyContactIdentityStorage(t.Context(), env.client, first)
			if rejectedErr == nil {
				t.Fatal("conflicting duplicate accepted by response order")
			}

			rec, err := env.data.Identity(t.Context(), aliceACI)
			if err != nil || rec != nil {
				t.Fatalf("partial duplicate import=%+v,%v", rec, err)
			}
		})
	}
}

func TestContactIdentityStorageIdenticalDuplicate(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	update := identityStorage(t, 10, newIdentityKey(t), signalpb.ContactRecord_VERIFIED)
	update.NewRecords = append(update.NewRecords, update.NewRecords[0])
	applyIdentityStorage(t, env, update)

	if env.alice(t).Trust != signal.TrustVerified {
		t.Fatal("identical duplicate ignored valid import")
	}

	pending, err := env.data.PendingIdentitySync(t.Context(), seededACI)
	if err != nil || len(pending) != 0 {
		t.Fatalf("storage echoed verification=%+v,%v", pending, err)
	}
}

func TestContactIdentityStorageInvalid(t *testing.T) {
	t.Parallel()

	for _, mutate := range []struct {
		name   string
		change func(*signalpb.ContactRecord)
	}{
		{"absent identity key", func(c *signalpb.ContactRecord) { c.IdentityKey = nil }},
		{"prefix", func(c *signalpb.ContactRecord) { c.IdentityKey[0] = 0 }},
		{"suffix", func(c *signalpb.ContactRecord) { c.IdentityKey = append(c.IdentityKey, 0) }},
		{"state", func(c *signalpb.ContactRecord) { c.IdentityState = 99 }},
		{"PNI only", func(c *signalpb.ContactRecord) { c.Aci = "" }},
		{"self", func(c *signalpb.ContactRecord) { c.Aci = seededACI }},
		{"conflicting ACI", func(c *signalpb.ContactRecord) { u := uuid.New(); c.AciBinary = u[:] }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			t.Parallel()
			env := newTrustEnv(t)
			update := identityStorage(t, 10, newIdentityKey(t), signalpb.ContactRecord_VERIFIED)
			mutate.change(update.NewRecords[0].StorageRecord.GetContact())
			applyIdentityStorage(t, env, update)

			rec, err := env.data.Identity(t.Context(), aliceACI)
			if err != nil || rec != nil {
				t.Fatalf("invalid import=%+v,%v", rec, err)
			}
		})
	}
}

func TestContactIdentityStorageIncompleteDownload(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	update := identityStorage(t, 10, newIdentityKey(t), signalpb.ContactRecord_VERIFIED)

	update.MissingRecords = []string{"not downloaded"}

	missingErr := signal.ApplyContactIdentityStorage(t.Context(), env.client, update)
	if missingErr == nil {
		t.Fatal("incomplete snapshot accepted")
	}

	rec, err := env.data.Identity(t.Context(), aliceACI)
	if err != nil || rec != nil {
		t.Fatalf("partial import=%+v,%v", rec, err)
	}

	update.MissingRecords = nil
	applyIdentityStorage(t, env, update)

	if env.alice(t).Trust != signal.TrustVerified {
		t.Fatal("retry lost")
	}
}

func TestContactIdentityStorageProtocolDivergence(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	key, next := newIdentityKey(t), newIdentityKey(t)
	applyIdentityStorage(t, env, identityStorage(t, 10, key, signalpb.ContactRecord_DEFAULT))

	_, protocolErr := env.device.IdentityKeyStore.SaveIdentityKey(t.Context(), serviceID(aliceACI), next)
	if protocolErr != nil {
		t.Fatal(protocolErr)
	}

	applyIdentityStorage(t, env, identityStorage(t, 11, key, signalpb.ContactRecord_VERIFIED))

	if env.alice(t).Trust != signal.TrustUnverified {
		t.Fatal("protocol divergence granted storage trust")
	}
}

func TestContactIdentityStorageSessionRollback(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	old, next := newKeyPair(t), newKeyPair(t)
	applyIdentityStorage(t, env, identityStorage(t, 10, old.GetIdentityKey(), signalpb.ContactRecord_DEFAULT))

	sessionErr := env.process(t, old, 1)
	if sessionErr != nil {
		t.Fatal(sessionErr)
	}

	database := timerSQL(t, env.dataDir)
	execTimerSQL(t, database, `CREATE TRIGGER fail_storage_session BEFORE DELETE ON signalmeow_sessions
 BEGIN SELECT RAISE(ABORT,'storage session failure'); END`)

	update := identityStorage(t, 11, next.GetIdentityKey(), signalpb.ContactRecord_VERIFIED)

	cleanupErr := signal.ApplyContactIdentityStorage(t.Context(), env.client, update)
	if cleanupErr == nil {
		t.Fatal("cleanup failure hidden")
	}

	if env.alice(t).Trust != signal.TrustUnverified || env.aliceSessions(t) != 1 {
		t.Fatal("cleanup failure escaped rollback")
	}

	keys, err := env.data.StoredIdentityKeys(t.Context(), seededACI)
	if err != nil || !bytes.Equal(keys[aliceACI], serialize(t, old)) {
		t.Fatal("protocol rollback failed")
	}

	execTimerSQL(t, database, `DROP TRIGGER fail_storage_session`)
	applyIdentityStorage(t, env, update)

	if env.alice(t).Trust != signal.TrustVerified || env.aliceSessions(t) != 0 {
		t.Fatal("retry failed to replace key and remove old session")
	}
}

func TestContactIdentityStorageProtectionSurvivesRestart(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	key := newIdentityKey(t)
	applyIdentityStorage(t, env, identityStorage(t, 10, key, signalpb.ContactRecord_DEFAULT))

	number, err := env.client.SafetyNumber(t.Context(), signal.Recipient{ACI: aliceACI})
	if err != nil {
		t.Fatal(err)
	}

	_, trustErr := env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: aliceACI}, number.Number)
	if trustErr != nil {
		t.Fatal(trustErr)
	}

	err = signal.FlushIdentityVerification(t.Context(), env.client,
		func(context.Context, *signalpb.SyncMessage) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	closeErr := env.client.Close()
	if closeErr != nil {
		t.Fatal(closeErr)
	}

	restarted := openOffline(t, env.dataDir, signal.SendOnly())

	err = signal.ApplyContactIdentityStorage(t.Context(), restarted,
		identityStorage(t, 11, key, signalpb.ContactRecord_DEFAULT))
	if err != nil {
		t.Fatal(err)
	}

	identities, err := restarted.Identities(t.Context(), &signal.Recipient{ACI: aliceACI})
	if err != nil || len(identities) != 1 || identities[0].Trust != signal.TrustVerified {
		t.Fatalf("restart protection=%+v,%v", identities, err)
	}
}

//nolint:cyclop,funlen // real transaction rollback, concurrent-visible settings and retry
func TestContactIdentityStoragePrivacyRollback(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	cli := signal.StorageProtocolClient(t.Context(), env.client)
	cli.EventHandler = func(events.SignalEvent) bool { return true }

	peer := uuid.MustParse(aliceACI)

	_, err := cli.Store.RecipientStore.LoadAndUpdateRecipient(t.Context(), peer, uuid.Nil,
		func(r *types.Recipient) (bool, error) { r.Blocked = true; return true, nil })
	if err != nil {
		t.Fatal(err)
	}

	blocked, err := cli.Store.RecipientStore.IsBlocked(t.Context(), peer)
	if err != nil || !blocked {
		t.Fatal("fixture not blocked")
	}

	original := &signalpb.AccountRecord{ReadReceipts: true}

	cli.Store.AccountRecord = original

	settingsErr := cli.Store.DeviceStore.PutDevice(t.Context(), &cli.Store.DeviceData)
	if settingsErr != nil {
		t.Fatal(settingsErr)
	}

	update := identityStorage(t, 10, newIdentityKey(t), signalpb.ContactRecord_VERIFIED)
	update.NewRecords = append(update.NewRecords, &signalmeow.DecryptedStorageRecord{
		StorageRecord: &signalpb.StorageRecord{Record: &signalpb.StorageRecord_Account{
			Account: &signalpb.AccountRecord{ReadReceipts: false},
		}},
	})
	update.MissingRecords = []string{"not retrieved"}
	handler := cli.StorageUpdateHandler
	visibleUnblock, visibleSettings := false, false

	cli.StorageUpdateHandler = func(ctx context.Context, got *signalmeow.StorageUpdate) error {
		// Read as a concurrent caller, outside the transaction context.
		blocked, readErr := cli.Store.RecipientStore.IsBlocked(context.Background(), peer) //nolint:contextcheck
		if readErr != nil {
			return fmt.Errorf("read committed block state: %w", readErr)
		}

		visibleUnblock = !blocked
		visibleSettings = cli.Store.AccountRecord != original

		return handler(ctx, got)
	}

	rejectedErr := cli.ApplyStorage(t.Context(), update)
	if rejectedErr == nil {
		t.Fatal("invalid update succeeded")
	}

	blocked, err = cli.Store.RecipientStore.IsBlocked(t.Context(), peer)

	loader, ok := cli.Store.RecipientStore.(interface {
		LoadRecipientByACI(ctx context.Context, aci uuid.UUID) (*types.Recipient, error)
	})
	if !ok {
		t.Fatal("recipient loader missing")
	}

	recipient, loadErr := loader.LoadRecipientByACI(t.Context(), peer)
	if err != nil || loadErr != nil || !blocked || !recipient.Blocked || visibleUnblock {
		t.Errorf("failed update unblocked contact: cached=%v, persisted=%+v, during=%v", blocked, recipient, visibleUnblock)
	}

	if cli.Store.AccountRecord != original || visibleSettings {
		t.Fatal("failed update published account settings")
	}

	update.MissingRecords = nil

	cli.StorageUpdateHandler = handler

	retryErr := cli.ApplyStorage(t.Context(), update)
	if retryErr != nil {
		t.Fatal(retryErr)
	}

	blocked, err = cli.Store.RecipientStore.IsBlocked(t.Context(), peer)
	if err != nil || blocked || cli.Store.AccountRecord.GetReadReceipts() {
		t.Fatal("successful retry did not publish settings")
	}
}
