//go:build cgo || libsignal_go

package signal_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
)

// Local trust must durably queue the exact ACI decision without connecting.
func TestTrustIdentityQueuesVerification(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	env.saveAlice(t, newIdentityKey(t))

	id, err := env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: aliceACI}, "")
	if err != nil || id.Trust != signal.TrustUnverified {
		t.Fatalf("trust=%+v,%v", id, err)
	}

	database := timerSQL(t, env.dataDir)

	var (
		peer, trust string
		key         []byte
	)

	err = database.QueryRowContext(t.Context(),
		`SELECT service_id,identity_key,trust FROM gosignal_identity_sync`).Scan(&peer, &key, &trust)
	if err != nil || peer != aliceACI || trust != "trusted-unverified" || len(key) != 33 {
		t.Fatalf("queued=%s,%x,%s,%v", peer, key, trust, err)
	}
}

func queuedCount(t *testing.T, env *trustEnv) int {
	t.Helper()

	var count int

	err := timerSQL(t, env.dataDir).QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM gosignal_identity_sync`).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}

	return count
}

func TestTrustIdentityQueueRollback(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	env.saveAlice(t, newIdentityKey(t))
	env.saveAlice(t, newIdentityKey(t))
	database := timerSQL(t, env.dataDir)
	execTimerSQL(t, database, `CREATE TRIGGER fail_outbox BEFORE INSERT ON gosignal_identity_sync
 BEGIN SELECT RAISE(ABORT,'test queue failure'); END`)

	_, err := env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: aliceACI}, "")
	if err == nil || env.alice(t).Trust != signal.TrustUntrusted || queuedCount(t, env) != 0 {
		t.Fatalf("queue failure changed trust: %v", err)
	}

	execTimerSQL(t, database, `DROP TRIGGER fail_outbox`)

	_, err = env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: aliceACI}, "")
	if err != nil || env.alice(t).Trust != signal.TrustUnverified ||
		queuedCount(t, env) != 1 {
		t.Fatalf("queue retry=%v", err)
	}
}

func TestTrustIdentityPNIDoesNotQueue(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)

	pni := libsignalgo.NewPNIServiceID(serviceID(aliceACI).UUID)

	_, err := env.device.ACIIdentityStore.SaveIdentityKey(t.Context(), pni, newIdentityKey(t))
	if err != nil {
		t.Fatal(err)
	}

	_, err = env.client.TrustIdentity(t.Context(), signal.Recipient{PNI: aliceACI}, "")
	if err != nil {
		t.Fatal(err)
	}

	if queuedCount(t, env) != 0 {
		t.Fatal("PNI queued for ACI sync")
	}
}

func TestIncomingVerificationSupersedesQueue(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	key := newIdentityKey(t)
	env.saveAlice(t, key)

	_, err := env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: aliceACI}, "")
	if err != nil {
		t.Fatal(err)
	}

	signal.ConnectOffline(t.Context(), env.client, signal.SendOnly())

	if !signal.Handle(env.client, verificationEvent(t, key, signalpb.Verified_VERIFIED)) {
		t.Fatal("update refused")
	}

	if queuedCount(t, env) != 0 {
		t.Fatal("incoming verification left local echo queued")
	}
}

// A failed or uncertain send preserves local trust and retries after restart.
//
//nolint:cyclop,funlen // failure, restart and durable recovery scenario
func TestFlushIdentityVerificationRetry(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	key := newIdentityKey(t)
	env.saveAlice(t, key)

	number, err := env.client.SafetyNumber(t.Context(), signal.Recipient{ACI: aliceACI})
	if err != nil {
		t.Fatal(err)
	}

	_, err = env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: aliceACI}, number.Number)
	if err != nil {
		t.Fatal(err)
	}

	serialized, err := key.Serialize()
	if err != nil {
		t.Fatal(err)
	}

	sends := 0
	send := func(_ context.Context, msg *signalpb.SyncMessage) error {
		sends++

		verified := msg.GetVerified()
		if verified == nil || verified.GetDestinationAci() != aliceACI || verified.State == nil ||
			verified.GetState() != signalpb.Verified_VERIFIED || !bytes.Equal(verified.GetIdentityKey(), serialized) {
			t.Fatalf("wire verification=%+v", verified)
		}

		return io.ErrUnexpectedEOF
	}

	err = signal.FlushIdentityVerification(t.Context(), env.client, send)
	if !errors.Is(err, io.ErrUnexpectedEOF) || sends != 1 || queuedCount(t, env) != 1 ||
		env.alice(t).Trust != signal.TrustVerified {
		t.Fatalf("partial sync=%v sends=%d", err, sends)
	}

	err = env.client.Close()
	if err != nil {
		t.Fatal(err)
	}

	restarted, err := signal.Open(t.Context(), signal.Options{DataDir: env.dataDir})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = restarted.Close() })

	err = signal.FlushIdentityVerification(t.Context(), restarted,
		func(_ context.Context, msg *signalpb.SyncMessage) error {
			sends++

			if msg.GetVerified().GetState() != signalpb.Verified_VERIFIED {
				t.Fatal("retry changed state")
			}

			return nil
		})
	if err != nil || sends != 2 || queuedCount(t, env) != 0 {
		t.Fatalf("retry=%v sends=%d", err, sends)
	}
}

func TestFlushIdentityVerificationStale(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	env.saveAlice(t, newIdentityKey(t))

	_, err := env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: aliceACI}, "")
	if err != nil {
		t.Fatal(err)
	}

	env.saveAlice(t, newIdentityKey(t))

	err = signal.FlushIdentityVerification(t.Context(), env.client, func(context.Context, *signalpb.SyncMessage) error {
		t.Fatal("stale decision sent")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if queuedCount(t, env) != 0 || env.alice(t).Trust != signal.TrustUntrusted {
		t.Fatal("stale decision kept/applied")
	}
}

func TestFlushIdentityVerificationKeepsNewDecision(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	env.saveAlice(t, newIdentityKey(t))

	rcpt := signal.Recipient{ACI: aliceACI}

	_, err := env.client.TrustIdentity(t.Context(), rcpt, "")
	if err != nil {
		t.Fatal(err)
	}

	number, err := env.client.SafetyNumber(t.Context(), rcpt)
	if err != nil {
		t.Fatal(err)
	}

	err = signal.FlushIdentityVerification(t.Context(), env.client,
		func(ctx context.Context, msg *signalpb.SyncMessage) error {
			if msg.GetVerified().GetState() != signalpb.Verified_DEFAULT {
				t.Fatal("unverified mapped to blocked")
			}

			_, trustErr := env.client.TrustIdentity(ctx, rcpt, number.Number)

			return trustErr //nolint:wrapcheck // callback forwards facade failure
		})
	if err != nil || queuedCount(t, env) != 1 {
		t.Fatalf("old send cleared newer decision: %v", err)
	}

	err = signal.FlushIdentityVerification(t.Context(), env.client,
		func(ctx context.Context, msg *signalpb.SyncMessage) error {
			if msg.GetVerified().GetState() != signalpb.Verified_VERIFIED {
				t.Fatal("new decision lost")
			}

			return nil
		})
	if err != nil || queuedCount(t, env) != 0 {
		t.Fatalf("new decision retry=%v", err)
	}
}

func TestFlushIdentityVerificationAcceptedButNotRecorded(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	env.saveAlice(t, newIdentityKey(t))

	_, err := env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: aliceACI}, "")
	if err != nil {
		t.Fatal(err)
	}

	database := timerSQL(t, env.dataDir)
	// Install after pruning: simulate a write failure after server acceptance.
	err = signal.FlushIdentityVerification(t.Context(), env.client,
		func(ctx context.Context, _ *signalpb.SyncMessage) error {
			_, triggerErr := database.ExecContext(ctx, `CREATE TRIGGER fail_sync_ack BEFORE DELETE ON gosignal_identity_sync
 BEGIN SELECT RAISE(ABORT,'test ack failure'); END`)
			if triggerErr != nil {
				t.Fatal(triggerErr)
			}

			return nil
		})
	if err == nil || queuedCount(t, env) != 1 {
		t.Fatal("failed ACK lost retry")
	}

	execTimerSQL(t, database, `DROP TRIGGER fail_sync_ack`)

	err = signal.FlushIdentityVerification(t.Context(), env.client,
		func(context.Context, *signalpb.SyncMessage) error { return nil })
	if err != nil || queuedCount(t, env) != 0 {
		t.Fatalf("duplicate retry=%v", err)
	}
}

func TestIncomingVerificationQueueRollback(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	key := newIdentityKey(t)
	env.saveAlice(t, key)

	_, err := env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: aliceACI}, "")
	if err != nil {
		t.Fatal(err)
	}

	signal.ConnectOffline(t.Context(), env.client, signal.SendOnly())
	database := timerSQL(t, env.dataDir)
	execTimerSQL(t, database, `CREATE TRIGGER fail_queue_supersede BEFORE DELETE ON gosignal_identity_sync
 BEGIN SELECT RAISE(ABORT,'test supersede failure'); END`)

	update := verificationEvent(t, key, signalpb.Verified_VERIFIED)
	if signal.Handle(env.client, update) || signal.Acked(env.client) || env.alice(t).Trust != signal.TrustUnverified ||
		queuedCount(t, env) != 1 {
		t.Fatal("failed supersede committed/ACKed")
	}

	execTimerSQL(t, database, `DROP TRIGGER fail_queue_supersede`)

	if !signal.Handle(env.client, update) || env.alice(t).Trust != signal.TrustVerified || queuedCount(t, env) != 0 {
		t.Fatal("redelivery did not supersede queued local decision")
	}
}

func TestFlushIdentityVerificationCanceled(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	env.saveAlice(t, newIdentityKey(t))

	_, err := env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: aliceACI}, "")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err = signal.FlushIdentityVerification(ctx, env.client, func(context.Context, *signalpb.SyncMessage) error {
		t.Fatal("canceled flush sent")
		return nil
	})
	if !errors.Is(err, context.Canceled) || queuedCount(t, env) != 1 {
		t.Fatalf("cancellation lost queue=%v", err)
	}
}

// A protocol-only replacement must invalidate an otherwise matching facade decision.
func TestFlushIdentityVerificationProtocolReplacement(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	env.saveAlice(t, newIdentityKey(t))

	_, err := env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: aliceACI}, "")
	if err != nil {
		t.Fatal(err)
	}

	replacement, err := newIdentityKey(t).Serialize()
	if err != nil {
		t.Fatal(err)
	}

	database := timerSQL(t, env.dataDir)

	_, err = database.ExecContext(t.Context(), `UPDATE signalmeow_identity_keys SET key=?
 WHERE account_id=? AND their_service_id=?`, replacement, seededACI, aliceACI)
	if err != nil {
		t.Fatal(err)
	}

	err = signal.FlushIdentityVerification(t.Context(), env.client, func(context.Context, *signalpb.SyncMessage) error {
		t.Fatal("protocol-mismatched verification sent")
		return nil
	})
	if err != nil || queuedCount(t, env) != 0 {
		t.Fatalf("protocol replacement pruning=%v", err)
	}
}

func TestAccountSyncReportsVerificationFailure(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	signal.ConnectOffline(t.Context(), env.client, signal.SendOnly())

	result, err := signal.FinishSyncWithVerificationFailure(t.Context(), env.client,
		signal.SyncResult{MasterKey: true, Storage: true, ContactList: true}, io.ErrUnexpectedEOF)
	if !errors.Is(err, signal.ErrSyncIncomplete) || !errors.Is(err, io.ErrUnexpectedEOF) || !result.Complete() {
		t.Fatalf("completed contacts hid pending verification: %+v,%v", result, err)
	}

	account, err := env.client.Account(t.Context())
	if err != nil || !account.LastSync.IsZero() {
		t.Fatalf("partial sync recorded success: %+v,%v", account, err)
	}
}

func TestTrustIdentityRespectsConcurrentDowngrade(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	key := newIdentityKey(t)
	env.saveAlice(t, key)

	rcpt := signal.Recipient{ACI: aliceACI}

	number, err := env.client.SafetyNumber(t.Context(), rcpt)
	if err != nil {
		t.Fatal(err)
	}

	_, err = env.client.TrustIdentity(t.Context(), rcpt, number.Number)
	if err != nil {
		t.Fatal(err)
	}

	signal.ConnectOffline(t.Context(), env.client, signal.SendOnly())

	level, err := signal.TrustIdentityAfterSnapshot(t.Context(), env.client, rcpt, func() {
		if !signal.Handle(env.client, verificationEvent(t, key, signalpb.Verified_DEFAULT)) {
			t.Fatal("concurrent downgrade refused")
		}
	})
	if err != nil || level != signal.TrustUnverified || env.alice(t).Trust != signal.TrustUnverified {
		t.Fatalf("trust without comparison restored stale verification: %v,%v", level, err)
	}

	err = signal.FlushIdentityVerification(t.Context(), env.client,
		func(_ context.Context, msg *signalpb.SyncMessage) error {
			if msg.GetVerified().GetState() != signalpb.Verified_DEFAULT {
				t.Fatal("stale verification queued")
			}

			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
}

//nolint:cyclop // real legacy session, atomic rollback and recovery scenario
func TestTrustIdentityQueueSessionRollback(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)

	oldKey, newKey := newKeyPair(t), newKeyPair(t)

	err := env.process(t, oldKey, 1)
	if err != nil {
		t.Fatal(err)
	}

	err = env.data.PutIdentity(t.Context(), store.IdentityRecord{
		ServiceID: aliceACI, Key: serialize(t, newKey), Trust: signal.TrustUntrusted.String(),
		PreviousKey: serialize(t, oldKey),
	})
	if err != nil {
		t.Fatal(err)
	}

	database := timerSQL(t, env.dataDir)
	execTimerSQL(t, database, `CREATE TRIGGER fail_session_outbox BEFORE INSERT ON gosignal_identity_sync
 BEGIN SELECT RAISE(ABORT,'test queue failure'); END`)

	_, err = env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: aliceACI}, "")
	if err == nil || env.alice(t).Trust != signal.TrustUntrusted || env.aliceSessions(t) != 1 || queuedCount(t, env) != 0 {
		t.Fatal("queue failure did not roll back session/trust")
	}

	protocolKey, err := env.device.ACIIdentityStore.GetIdentityKey(t.Context(), serviceID(aliceACI))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(identityBytes(t, protocolKey), serialize(t, oldKey)) {
		t.Fatal("failed queue changed protocol key")
	}

	execTimerSQL(t, database, `DROP TRIGGER fail_session_outbox`)

	_, err = env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: aliceACI}, "")
	if err != nil || env.aliceSessions(t) != 0 || queuedCount(t, env) != 1 {
		t.Fatalf("retry=%v", err)
	}

	protocolKey, err = env.device.ACIIdentityStore.GetIdentityKey(t.Context(), serviceID(aliceACI))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(identityBytes(t, protocolKey), serialize(t, newKey)) {
		t.Fatal("retry did not commit current protocol key")
	}

	err = env.process(t, newKey, 2)
	if err != nil {
		t.Fatal(err)
	}
}

func identityBytes(t *testing.T, key *libsignalgo.IdentityKey) []byte {
	t.Helper()

	serialized, err := key.Serialize()
	if err != nil {
		t.Fatal(err)
	}

	return serialized
}
