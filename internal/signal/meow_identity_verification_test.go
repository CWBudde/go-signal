//go:build cgo || libsignal_go

package signal_test

import (
	"bytes"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/events"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/google/uuid"
)

func verificationEvent(
	t *testing.T, key *libsignalgo.IdentityKey, state signalpb.Verified_State,
) *events.IdentityVerification {
	t.Helper()

	serialized, err := key.Serialize()
	if err != nil {
		t.Fatal(err)
	}

	return &events.IdentityVerification{ACI: uuid.MustParse(aliceACI), IdentityKey: serialized, State: state}
}

func TestHandleIdentityVerificationStates(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		wire signalpb.Verified_State
		want signal.TrustLevel
	}{
		{signalpb.Verified_DEFAULT, signal.TrustUnverified},
		{signalpb.Verified_VERIFIED, signal.TrustVerified},
		{signalpb.Verified_UNVERIFIED, signal.TrustUntrusted},
	} {
		t.Run(test.wire.String(), func(t *testing.T) {
			t.Parallel()
			testIdentityVerificationState(t, test.wire, test.want)
		})
	}
}

//nolint:cyclop,funlen // state transition, history, isolation, ACK and restart scenario
func testIdentityVerificationState(t *testing.T, wire signalpb.Verified_State, want signal.TrustLevel) {
	t.Helper()
	env := newTrustEnv(t)
	old, key := newIdentityKey(t), newIdentityKey(t)
	env.saveAlice(t, old)
	env.saveAlice(t, key)

	pni := libsignalgo.NewPNIServiceID(uuid.MustParse(aliceACI))

	_, err := env.device.ACIIdentityStore.SaveIdentityKey(t.Context(), pni, key)
	if err != nil {
		t.Fatal(err)
	}

	signal.ConnectOffline(t.Context(), env.client, signal.SendOnly())

	before, err := env.data.Identity(t.Context(), aliceACI)
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if !signal.Handle(env.client, verificationEvent(t, key, wire)) {
			t.Fatal("verification refused")
		}
	}

	after, err := env.data.Identity(t.Context(), aliceACI)
	if err != nil || after == nil || after.Trust != want.String() || after.PendingEvent ||
		!after.FirstSeen.Equal(before.FirstSeen) || !after.ChangedAt.Equal(before.ChangedAt) ||
		!bytes.Equal(after.PreviousKey, before.PreviousKey) {
		t.Fatalf("trust/history=%+v,%v", after, err)
	}

	if got := env.alice(t); got.Trust != want {
		t.Fatalf("identity trust=%v", got.Trust)
	}

	if env.trusted(t, aliceACI, key, sending) != want.Trusted() {
		t.Fatal("sending policy not updated")
	}

	record, err := env.data.Identity(t.Context(), pni.String())
	if err != nil || record == nil || record.Trust != signal.TrustUnverified.String() {
		t.Fatalf("PNI changed=%+v,%v", record, err)
	}

	if !signal.Acked(env.client) {
		t.Fatal("internal verification missing ACK flush flag")
	}

	select {
	case evt := <-env.client.Events():
		t.Fatalf("public verification event=%T", evt)
	default:
	}

	err = env.client.Close()
	if err != nil {
		t.Fatal(err)
	}

	if signal.Handle(env.client, verificationEvent(t, key, wire)) {
		t.Fatal("closed client accepted verification")
	}

	restarted := openOffline(t, env.dataDir, signal.SendOnly())

	identities, err := restarted.Identities(t.Context(), &signal.Recipient{ACI: aliceACI})
	if err != nil || len(identities) != 1 || identities[0].Trust != want {
		t.Fatalf("restart identity=%+v,%v", identities, err)
	}
}

func TestHandleIdentityVerificationFailureAndRetry(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	key := newIdentityKey(t)
	env.saveAlice(t, key)
	signal.ConnectOffline(t.Context(), env.client, signal.SendOnly())
	database := timerSQL(t, env.dataDir)
	execTimerSQL(t, database, `CREATE TRIGGER fail_verification BEFORE INSERT ON gosignal_identities
 BEGIN SELECT RAISE(ABORT,'verification test failure'); END`)

	evt := verificationEvent(t, key, signalpb.Verified_VERIFIED)
	if signal.Handle(env.client, evt) || signal.Acked(env.client) {
		t.Fatal("failed persistence acknowledged")
	}

	if got := env.alice(t); got.Trust != signal.TrustUnverified {
		t.Fatalf("failed write changed trust=%v", got.Trust)
	}

	execTimerSQL(t, database, `DROP TRIGGER fail_verification`)

	if !signal.Handle(env.client, evt) || !signal.Acked(env.client) {
		t.Fatal("redelivery not acknowledged")
	}

	if got := env.alice(t); got.Trust != signal.TrustVerified {
		t.Fatalf("retry trust=%v", got.Trust)
	}
}

func TestHandleIdentityVerificationStale(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	key, replacement := newIdentityKey(t), newIdentityKey(t)
	env.saveAlice(t, key)
	env.saveAlice(t, replacement)
	signal.ConnectOffline(t.Context(), env.client)

	if !signal.Handle(env.client, verificationEvent(t, key, signalpb.Verified_VERIFIED)) {
		t.Fatal("stale update not ignored")
	}

	if got := env.alice(t); got.Trust != signal.TrustUntrusted {
		t.Fatalf("replacement trusted=%v", got.Trust)
	}

	record, err := env.data.Identity(t.Context(), aliceACI)
	if err != nil || !record.PendingEvent {
		t.Fatalf("new warning cleared=%+v,%v", record, err)
	}

	evt := verificationEvent(t, replacement, signalpb.Verified_VERIFIED)

	evt.ACI = uuid.New()
	if !signal.Handle(env.client, evt) {
		t.Fatal("unknown update not ignored")
	}

	record, err = env.data.Identity(t.Context(), evt.ACI.String())
	if err != nil || record != nil {
		t.Fatalf("unknown imported=%+v,%v", record, err)
	}
}

//nolint:cyclop // real stale-session rollback, redelivery and matching-session preservation
func TestHandleIdentityVerificationStaleSession(t *testing.T) {
	t.Parallel()
	env := newTrustEnv(t)
	old, current := newKeyPair(t), newKeyPair(t)

	err := env.process(t, old, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Older accounts can have a current identity plus a session with the old key.
	_, err = env.device.IdentityKeyStore.SaveIdentityKey(t.Context(), serviceID(aliceACI), current.GetIdentityKey())
	if err != nil {
		t.Fatal(err)
	}

	err = env.data.PutIdentity(t.Context(), store.IdentityRecord{
		ServiceID: aliceACI, Key: serialize(t, current), Trust: signal.TrustUntrusted.String(), PendingEvent: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	signal.ConnectOffline(t.Context(), env.client, signal.SendOnly())
	database := timerSQL(t, env.dataDir)
	execTimerSQL(t, database, `CREATE TRIGGER fail_verification_session BEFORE DELETE ON signalmeow_sessions
 BEGIN SELECT RAISE(ABORT,'verification session failure'); END`)

	evt := verificationEvent(t, current.GetIdentityKey(), signalpb.Verified_VERIFIED)
	if signal.Handle(env.client, evt) || signal.Acked(env.client) {
		t.Fatal("failed session cleanup acknowledged")
	}

	if got := env.alice(t); got.Trust != signal.TrustUntrusted {
		t.Fatalf("cleanup failure committed trust=%v", got.Trust)
	}

	if count := env.aliceSessions(t); count != 1 {
		t.Fatalf("rollback lost session: %d", count)
	}

	execTimerSQL(t, database, `DROP TRIGGER fail_verification_session`)

	if !signal.Handle(env.client, evt) {
		t.Fatal("redelivery refused")
	}

	if count := env.aliceSessions(t); count != 0 {
		t.Fatalf("stale sessions=%d", count)
	}

	err = env.process(t, current, 2)
	if err != nil {
		t.Fatalf("verified replacement refused: %v", err)
	}

	if !signal.Handle(env.client, evt) {
		t.Fatal("duplicate refused")
	}

	if count := env.aliceSessions(t); count != 1 {
		t.Fatalf("matching sessions=%d", count)
	}
}
