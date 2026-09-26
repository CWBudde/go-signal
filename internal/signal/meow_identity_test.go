//go:build cgo && !purego

package signal_test

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	mstore "go.mau.fi/mautrix-signal/pkg/signalmeow/store"
)

const (
	sending   = libsignalgo.SignalDirectionSending
	receiving = libsignalgo.SignalDirectionReceiving
)

var errRollback = errors.New("rollback")

// trustEnv is a seeded account whose device has the trust wrapper installed, plus a client on
// the same data dir for the facade methods.
type trustEnv struct {
	device *mstore.Device
	data   *store.Store
	trust  *signal.IdentityTrust
	client signal.Client
	now    time.Time
}

func newTrustEnv(t *testing.T) *trustEnv {
	t.Helper()

	dataDir := seedAccount(t)

	dir, err := store.OpenDir(dataDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	data, err := dir.OpenAccount(t.Context(), seededACI, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = data.Close() })

	device, err := data.Devices.DeviceByACI(t.Context(), uuid.MustParse(seededACI))
	if err != nil || device == nil {
		t.Fatalf("load device: %v", err)
	}

	env := &trustEnv{device: device, data: data, now: time.Date(2026, 9, 20, 12, 30, 0, 0, time.UTC)}
	env.trust = signal.InstallTrust(device, data, slog.New(slog.DiscardHandler), func() time.Time { return env.now })

	env.client, err = signal.Open(t.Context(), signal.Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = env.client.Close() })

	return env
}

// saveAlice is libsignal storing alice's key after decrypting (or encrypting) a message.
func (e *trustEnv) saveAlice(t *testing.T, key *libsignalgo.IdentityKey) {
	t.Helper()

	_, err := e.device.ACIIdentityStore.SaveIdentityKey(t.Context(), serviceID(aliceACI), key)
	if err != nil {
		t.Fatalf("save identity key: %v", err)
	}
}

// trusted asks the wrapper whether key may be used in direction.
func (e *trustEnv) trusted(
	t *testing.T, aci string, key *libsignalgo.IdentityKey, dir libsignalgo.SignalDirection,
) bool {
	t.Helper()

	ok, err := e.device.ACIIdentityStore.IsTrustedIdentity(t.Context(), serviceID(aci), key, dir)
	if err != nil {
		t.Fatalf("is trusted: %v", err)
	}

	return ok
}

// report returns the identity change events the wrapper reports now.
func (e *trustEnv) report(t *testing.T) []*signal.IdentityChanged {
	t.Helper()

	var out []*signal.IdentityChanged

	ok := e.trust.Report(t.Context(), func(evt signal.Event) bool {
		changed, isChange := evt.(*signal.IdentityChanged)
		if !isChange {
			t.Errorf("reported %T", evt)
		}

		out = append(out, changed)

		return true
	})
	if !ok {
		t.Error("report failed")
	}

	return out
}

// alice returns the facade's view of alice's identity.
func (e *trustEnv) alice(t *testing.T) signal.Identity {
	t.Helper()

	ids, err := e.client.Identities(t.Context(), &signal.Recipient{ACI: aliceACI})
	if err != nil || len(ids) != 1 {
		t.Fatalf("identities of alice = %+v, %v", ids, err)
	}

	return ids[0]
}

func serviceID(aci string) libsignalgo.ServiceID {
	return libsignalgo.NewACIServiceID(uuid.MustParse(aci))
}

func newIdentityKey(t *testing.T) *libsignalgo.IdentityKey {
	t.Helper()

	pair, err := libsignalgo.GenerateIdentityKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	return pair.GetIdentityKey()
}

func fingerprintOf(t *testing.T, key *libsignalgo.IdentityKey) string {
	t.Helper()

	raw, err := key.Serialize()
	if err != nil {
		t.Fatal(err)
	}

	return hex.EncodeToString(raw)
}

func TestTrustOnFirstUse(t *testing.T) {
	t.Parallel()

	env := newTrustEnv(t)
	key := newIdentityKey(t)

	// A new user is trusted for sending, before and after libsignal stores the key.
	if !env.trusted(t, aliceACI, key, sending) {
		t.Error("first key not trusted before it is stored")
	}

	env.saveAlice(t, key)

	if !env.trusted(t, aliceACI, key, sending) {
		t.Error("first key not trusted")
	}

	id := env.alice(t)
	if id.Trust != signal.TrustUnverified || id.Fingerprint != fingerprintOf(t, key) ||
		!id.FirstSeen.Equal(env.now) || !id.ChangedAt.IsZero() {
		t.Errorf("identity = %+v", id)
	}

	if events := env.report(t); len(events) != 0 {
		t.Errorf("first key reported as a change: %+v", events)
	}
}

//nolint:cyclop // one scenario, checked step by step
func TestIdentityChangeOnReceive(t *testing.T) {
	t.Parallel()

	env := newTrustEnv(t)
	oldKey, newKey := newIdentityKey(t), newIdentityKey(t)

	env.saveAlice(t, oldKey)

	env.now = env.now.Add(time.Hour)
	env.saveAlice(t, newKey)

	// Receiving keeps working, sending to the new key is refused.
	if !env.trusted(t, aliceACI, newKey, receiving) {
		t.Error("new key refused for receiving")
	}

	if env.trusted(t, aliceACI, newKey, sending) {
		t.Error("new key trusted for sending")
	}

	id := env.alice(t)
	if id.Trust != signal.TrustUntrusted || id.Fingerprint != fingerprintOf(t, newKey) || !id.ChangedAt.Equal(env.now) {
		t.Errorf("identity after change = %+v", id)
	}

	events := env.report(t)
	if len(events) != 1 || events[0].Recipient.ACI != aliceACI ||
		events[0].OldFingerprint != fingerprintOf(t, oldKey) || events[0].NewFingerprint != fingerprintOf(t, newKey) ||
		!events[0].Time.Equal(env.now) {
		t.Fatalf("events = %+v", events)
	}

	if again := env.report(t); len(again) != 0 {
		t.Errorf("change reported twice: %+v", again)
	}

	// Trusting the new key unblocks sending.
	trusted, err := env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: aliceACI}, "")
	if err != nil || trusted.Trust != signal.TrustUnverified {
		t.Fatalf("trust = %+v, %v", trusted, err)
	}

	if !env.trusted(t, aliceACI, newKey, sending) {
		t.Error("trusted key refused for sending")
	}

	// The old key is no exception: a (delayed) message with it is another change.
	env.saveAlice(t, oldKey)

	if id := env.alice(t); id.Trust != signal.TrustUntrusted || id.Fingerprint != fingerprintOf(t, oldKey) {
		t.Errorf("identity after the old key came back = %+v", id)
	}

	events = env.report(t)
	if len(events) != 1 || events[0].OldFingerprint != fingerprintOf(t, newKey) ||
		events[0].NewFingerprint != fingerprintOf(t, oldKey) {
		t.Errorf("events = %+v", events)
	}
}

// TestIdentityOldKeyAfterVerify is the attack the previous key must not get through: after
// the user verified the new key, a prekey bundle signed with the old one (a lost phone, a
// malicious server) is refused and reported, and the sessions with the new key are dropped.
//
//nolint:cyclop // one scenario, checked step by step
func TestIdentityOldKeyAfterVerify(t *testing.T) {
	t.Parallel()

	env := newTrustEnv(t)
	oldKey, newKey := newKeyPair(t), newKeyPair(t)
	alice := signal.Recipient{ACI: aliceACI}

	err := env.process(t, oldKey, 1)
	if err != nil {
		t.Fatalf("first bundle: %v", err)
	}

	// The change removes the session with the old key.
	err = env.process(t, newKey, 1)
	if !errors.Is(err, libsignalgo.ErrorCodeUntrustedIdentity) {
		t.Fatalf("changed bundle: got %v, want UntrustedIdentity", err)
	}

	if n := env.aliceSessions(t); n != 0 {
		t.Errorf("%d sessions with the old key left after the change", n)
	}

	env.report(t)

	number, err := env.client.SafetyNumber(t.Context(), alice)
	if err != nil {
		t.Fatal(err)
	}

	verified, err := env.client.TrustIdentity(t.Context(), alice, number.Number)
	if err != nil || verified.Trust != signal.TrustVerified {
		t.Fatalf("verify = %+v, %v", verified, err)
	}

	err = env.process(t, newKey, 1)
	if err != nil {
		t.Fatalf("verified bundle: %v", err)
	}

	env.now = env.now.Add(time.Hour)

	err = env.process(t, oldKey, 2)
	if !errors.Is(err, libsignalgo.ErrorCodeUntrustedIdentity) {
		t.Fatalf("bundle with the old key: got %v, want UntrustedIdentity", err)
	}

	id := env.alice(t)
	if id.Trust != signal.TrustUntrusted || id.Fingerprint != fingerprintOf(t, oldKey.GetIdentityKey()) ||
		!id.ChangedAt.Equal(env.now) {
		t.Errorf("identity = %+v, want the old key, untrusted", id)
	}

	events := env.report(t)
	if len(events) != 1 || events[0].OldFingerprint != fingerprintOf(t, newKey.GetIdentityKey()) ||
		events[0].NewFingerprint != fingerprintOf(t, oldKey.GetIdentityKey()) {
		t.Errorf("events = %+v", events)
	}

	if n := env.aliceSessions(t); n != 0 {
		t.Errorf("%d sessions with the verified key left after the change", n)
	}
}

// TestTrustIdentityRemovesStaleSessions checks that trusting a key drops the sessions set up
// with another one (as an older go-signal left them after a change) and keeps the others.
func TestTrustIdentityRemovesStaleSessions(t *testing.T) {
	t.Parallel()

	env := newTrustEnv(t)
	oldKey, newKey := newKeyPair(t), newKeyPair(t)
	alice := signal.Recipient{ACI: aliceACI}

	err := env.process(t, oldKey, 1)
	if err != nil {
		t.Fatal(err)
	}

	// A change recorded without removing the session.
	err = env.data.PutIdentity(t.Context(), store.IdentityRecord{
		ServiceID: serviceID(aliceACI).String(), Key: serialize(t, newKey), Trust: signal.TrustUntrusted.String(),
		PreviousKey: serialize(t, oldKey),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = env.client.TrustIdentity(t.Context(), alice, "")
	if err != nil {
		t.Fatal(err)
	}

	if n := env.aliceSessions(t); n != 0 {
		t.Errorf("%d stale sessions left", n)
	}

	err = env.process(t, newKey, 2)
	if err != nil {
		t.Fatal(err)
	}

	_, err = env.client.TrustIdentity(t.Context(), alice, "")
	if err != nil {
		t.Fatal(err)
	}

	if n := env.aliceSessions(t); n != 1 {
		t.Errorf("%d sessions, want the one with the trusted key", n)
	}
}

// TestPNIIdentityLeftToSignalmeow checks that PNI keys, which can't be listed or trusted, are
// neither enforced nor reported.
func TestPNIIdentityLeftToSignalmeow(t *testing.T) {
	t.Parallel()

	env := newTrustEnv(t)
	pni := libsignalgo.NewPNIServiceID(uuid.MustParse(aliceACI))

	for range 2 {
		key := newIdentityKey(t)

		_, err := env.device.ACIIdentityStore.SaveIdentityKey(t.Context(), pni, key)
		if err != nil {
			t.Fatal(err)
		}

		ok, err := env.device.ACIIdentityStore.IsTrustedIdentity(t.Context(), pni, key, sending)
		if err != nil || !ok {
			t.Errorf("PNI key refused: %v", err)
		}
	}

	ok, err := env.device.ACIIdentityStore.IsTrustedIdentity(t.Context(), pni, newIdentityKey(t), sending)
	if err != nil || !ok {
		t.Errorf("new PNI key refused: %v", err)
	}

	if events := env.report(t); len(events) != 0 {
		t.Errorf("PNI change reported: %+v", events)
	}
}

func TestIdentityChangeOnSend(t *testing.T) {
	t.Parallel()

	env := newTrustEnv(t)
	oldKey, newKey := newIdentityKey(t), newIdentityKey(t)

	env.saveAlice(t, oldKey)

	// A prekey bundle with a new key (they re-registered): refused and recorded, so that the
	// user can trust it.
	if env.trusted(t, aliceACI, newKey, sending) {
		t.Fatal("new key from a prekey bundle trusted")
	}

	events := env.report(t)
	if len(events) != 1 || events[0].NewFingerprint != fingerprintOf(t, newKey) {
		t.Fatalf("events = %+v", events)
	}

	_, err := env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: aliceACI}, "")
	if err != nil {
		t.Fatal(err)
	}

	if !env.trusted(t, aliceACI, newKey, sending) {
		t.Error("trusted key refused")
	}

	// libsignal stores it after the session is set up; no change.
	env.saveAlice(t, newKey)

	if id := env.alice(t); id.Trust != signal.TrustUnverified {
		t.Errorf("identity = %+v", id)
	}
}

//nolint:cyclop // one scenario, checked step by step
func TestIdentityKnownBeforeTracking(t *testing.T) {
	t.Parallel()

	env := newTrustEnv(t)
	known, changed := newIdentityKey(t), newIdentityKey(t)

	// Keys signalmeow stored before go-signal tracked trust, including our own.
	stored := map[string]*libsignalgo.IdentityKey{bobACI: known, seededACI: env.device.ACIIdentityKeyPair.GetIdentityKey()}
	for aci, key := range stored {
		_, err := env.device.IdentityKeyStore.SaveIdentityKey(t.Context(), serviceID(aci), key)
		if err != nil {
			t.Fatal(err)
		}
	}

	ids, err := env.client.Identities(t.Context(), nil)
	if err != nil || len(ids) != 1 || ids[0].Recipient.ACI != bobACI || ids[0].Trust != signal.TrustUnverified ||
		!ids[0].FirstSeen.IsZero() {
		t.Fatalf("identities = %+v, %v; want bob only, trusted on first use", ids, err)
	}

	if !env.trusted(t, bobACI, known, sending) || env.trusted(t, bobACI, changed, sending) {
		t.Error("want the known key trusted and a different one refused")
	}

	events := env.report(t)
	if len(events) != 1 || events[0].OldFingerprint != fingerprintOf(t, known) {
		t.Errorf("events = %+v", events)
	}
}

// TestIdentitySaveInTransaction checks that the wrapper writes through signalmeow's decryption
// transaction instead of blocking on it.
func TestIdentitySaveInTransaction(t *testing.T) {
	t.Parallel()

	env := newTrustEnv(t)
	oldKey, newKey := newIdentityKey(t), newIdentityKey(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	for _, key := range []*libsignalgo.IdentityKey{oldKey, newKey} {
		err := env.device.DoDecryptionTxn(ctx, func(ctx context.Context) error {
			_, err := env.device.ACIIdentityStore.SaveIdentityKey(ctx, serviceID(aliceACI), key)
			if err != nil {
				return fmt.Errorf("save: %w", err)
			}

			return nil
		})
		if err != nil {
			t.Fatalf("save in transaction: %v", err)
		}
	}

	if id := env.alice(t); id.Trust != signal.TrustUntrusted {
		t.Errorf("identity = %+v", id)
	}

	// A rolled back change is neither stored nor reported.
	env.report(t)

	err := env.device.DoDecryptionTxn(ctx, func(ctx context.Context) error {
		for range 2 {
			_, err := env.device.ACIIdentityStore.SaveIdentityKey(ctx, serviceID(bobACI), newIdentityKey(t))
			if err != nil {
				return fmt.Errorf("save: %w", err)
			}
		}

		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("got %v", err)
	}

	if events := env.report(t); len(events) != 0 {
		t.Errorf("rolled back change reported: %+v", events)
	}
}

// TestIdentityChangeRemovesSessionsInTransaction checks that a change seen while decrypting
// removes the old sessions through signalmeow's decryption transaction.
func TestIdentityChangeRemovesSessionsInTransaction(t *testing.T) {
	t.Parallel()

	env := newTrustEnv(t)

	err := env.process(t, newKeyPair(t), 1)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	err = env.device.DoDecryptionTxn(ctx, func(ctx context.Context) error {
		_, err := env.device.ACIIdentityStore.SaveIdentityKey(ctx, serviceID(aliceACI), newIdentityKey(t))
		if err != nil {
			return fmt.Errorf("save: %w", err)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("save in transaction: %v", err)
	}

	if n := env.aliceSessions(t); n != 0 {
		t.Errorf("%d sessions with the old key left", n)
	}
}

//nolint:cyclop // one scenario, checked step by step
func TestSafetyNumberAndVerify(t *testing.T) {
	t.Parallel()

	env := newTrustEnv(t)
	key := newIdentityKey(t)
	env.saveAlice(t, key)

	alice := signal.Recipient{ACI: aliceACI}

	number, err := env.client.SafetyNumber(t.Context(), alice)
	if err != nil {
		t.Fatalf("safety number: %v", err)
	}

	if len(number.Number) != signal.SafetyNumberDigits || len(number.Scannable) == 0 ||
		number.Identity.Fingerprint != fingerprintOf(t, key) {
		t.Fatalf("safety number = %+v", number)
	}

	_, err = env.client.TrustIdentity(t.Context(), alice, strings.Repeat("0", signal.SafetyNumberDigits))
	if !errors.Is(err, signal.ErrSafetyNumberMismatch) {
		t.Errorf("wrong number: got %v", err)
	}

	if id := env.alice(t); id.Trust != signal.TrustUnverified {
		t.Errorf("a failed verification changed the trust: %+v", id)
	}

	grouped := strings.Join(signal.GroupSafetyNumber(number.Number), " ")

	id, err := env.client.TrustIdentity(t.Context(), alice, grouped)
	if err != nil || id.Trust != signal.TrustVerified {
		t.Fatalf("verify = %+v, %v", id, err)
	}

	// A verified key that changes becomes untrusted like any other.
	env.saveAlice(t, newIdentityKey(t))

	if id := env.alice(t); id.Trust != signal.TrustUntrusted {
		t.Errorf("identity after change = %+v", id)
	}

	_, err = env.client.SafetyNumber(t.Context(), signal.Recipient{ACI: bobACI})
	if !errors.Is(err, signal.ErrUnknownIdentity) {
		t.Errorf("unknown user: got %v", err)
	}

	_, err = env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: bobACI}, "")
	if !errors.Is(err, signal.ErrUnknownIdentity) {
		t.Errorf("trust unknown user: got %v", err)
	}
}

// TestComputeSafetyNumber checks that both sides get the same number.
//
//nolint:cyclop // one scenario, checked step by step
func TestComputeSafetyNumber(t *testing.T) {
	t.Parallel()

	ours, theirs := newKeyPair(t), newKeyPair(t)
	ourACI, theirACI := uuid.New(), uuid.New()

	ourNumber, ourScan, err := signal.ComputeSafetyNumber(ourACI, ours.GetPublicKey(), theirACI, serialize(t, theirs))
	if err != nil {
		t.Fatal(err)
	}

	theirNumber, theirScan, err := signal.ComputeSafetyNumber(theirACI, theirs.GetPublicKey(), ourACI,
		serialize(t, ours))
	if err != nil {
		t.Fatal(err)
	}

	if ourNumber != theirNumber || len(ourNumber) != signal.SafetyNumberDigits {
		t.Errorf("safety numbers %s and %s, want the same 60 digits", ourNumber, theirNumber)
	}

	if len(ourScan) == 0 || string(ourScan) == string(theirScan) {
		t.Error("want a scannable encoding per side")
	}

	again, _, err := signal.ComputeSafetyNumber(ourACI, ours.GetPublicKey(), theirACI, serialize(t, theirs))
	if err != nil || again != ourNumber {
		t.Errorf("not deterministic: %s, %v", again, err)
	}

	other, _, err := signal.ComputeSafetyNumber(ourACI, ours.GetPublicKey(), theirACI, serialize(t, newKeyPair(t)))
	if err != nil || other == ourNumber {
		t.Errorf("another key gives the same number (%v)", err)
	}
}

func TestSendResultUntrusted(t *testing.T) {
	t.Parallel()

	alice := signal.Recipient{ACI: aliceACI}
	untrusted := fmt.Errorf("error processing prekey bundle: %w",
		&libsignalgo.SignalError{Code: libsignalgo.ErrorCodeUntrustedIdentity, Message: "untrusted identity"})

	res := signal.ConvertRecipientResult(alice, false, signalmeow.SendMessageResult{
		FailedSendResult: signalmeow.FailedSendResult{Error: untrusted},
	})
	if !errors.Is(res.Err, signal.ErrUntrustedIdentity) ||
		!strings.Contains(res.Err.Error(), "identities trust "+aliceACI) {
		t.Errorf("1:1 result error = %v", res.Err)
	}

	group := signal.GroupResults(&signalmeow.GroupMessageSendResult{
		FailedToSendTo: []signalmeow.FailedSendResult{{Recipient: serviceID(aliceACI), Error: untrusted}},
	})
	if len(group) != 1 || !errors.Is(group[0].Err, signal.ErrUntrustedIdentity) {
		t.Errorf("group results = %+v", group)
	}
}

func newKeyPair(t *testing.T) *libsignalgo.IdentityKeyPair {
	t.Helper()

	pair, err := libsignalgo.GenerateIdentityKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	return pair
}

func serialize(t *testing.T, pair *libsignalgo.IdentityKeyPair) []byte {
	t.Helper()

	raw, err := pair.GetPublicKey().Serialize()
	if err != nil {
		t.Fatal(err)
	}

	return raw
}

// TestIdentityChangeBlocksSession runs libsignal's session setup against the wrapped store: a
// prekey bundle with a changed identity key fails with UntrustedIdentity until it is trusted.
func TestIdentityChangeBlocksSession(t *testing.T) {
	t.Parallel()

	env := newTrustEnv(t)
	first, second := newKeyPair(t), newKeyPair(t)

	err := env.process(t, first, 1)
	if err != nil {
		t.Fatalf("first bundle: %v", err)
	}

	err = env.process(t, second, 1)
	if !errors.Is(err, libsignalgo.ErrorCodeUntrustedIdentity) {
		t.Fatalf("changed bundle: got %v, want UntrustedIdentity", err)
	}

	if events := env.report(t); len(events) != 1 {
		t.Errorf("events = %+v", events)
	}

	_, err = env.client.TrustIdentity(t.Context(), signal.Recipient{ACI: aliceACI}, "")
	if err != nil {
		t.Fatal(err)
	}

	err = env.process(t, second, 1)
	if err != nil {
		t.Errorf("trusted bundle: %v", err)
	}
}

// process runs libsignal's session setup with a prekey bundle of alice's device deviceID with
// the identity key pair identity, against the wrapped stores.
func (e *trustEnv) process(t *testing.T, identity *libsignalgo.IdentityKeyPair, deviceID uint) error {
	t.Helper()

	remote, err := serviceID(aliceACI).Address(deviceID)
	if err != nil {
		t.Fatal(err)
	}

	local, err := e.device.ACIServiceID().Address(uint(e.device.DeviceID))
	if err != nil {
		t.Fatal(err)
	}

	//nolint:wrapcheck // tests check libsignal's error
	return libsignalgo.ProcessPreKeyBundle(t.Context(), preKeyBundle(t, identity, uint32(deviceID)), remote, local,
		e.device.ACISessionStore, e.device.ACIIdentityStore)
}

// aliceSessions returns how many sessions the account has with alice.
func (e *trustEnv) aliceSessions(t *testing.T) int {
	t.Helper()

	tuples, err := e.device.ACISessionStore.AllSessionsForServiceID(t.Context(), serviceID(aliceACI))
	if err != nil {
		t.Fatal(err)
	}

	return len(tuples)
}

// preKeyBundle returns a valid prekey bundle of the device deviceID with the identity key pair
// identity.
func preKeyBundle(t *testing.T, identity *libsignalgo.IdentityKeyPair, deviceID uint32) *libsignalgo.PreKeyBundle {
	t.Helper()

	must := func(err error) {
		t.Helper()

		if err != nil {
			t.Fatal(err)
		}
	}

	preKey, err := libsignalgo.GeneratePrivateKey()
	must(err)
	prePublic, err := preKey.GetPublicKey()
	must(err)

	signedKey, err := libsignalgo.GeneratePrivateKey()
	must(err)
	signedPublic, err := signedKey.GetPublicKey()
	must(err)
	signedRaw, err := signedPublic.Serialize()
	must(err)
	signedSig, err := identity.GetPrivateKey().Sign(signedRaw)
	must(err)

	kyber, err := libsignalgo.KyberKeyPairGenerate()
	must(err)
	kyberPublic, err := kyber.GetPublicKey()
	must(err)
	kyberRaw, err := kyberPublic.Serialize()
	must(err)
	kyberSig, err := identity.GetPrivateKey().Sign(kyberRaw)
	must(err)

	bundle, err := libsignalgo.NewPreKeyBundle(4242, deviceID, 1, prePublic, 2, signedPublic, signedSig, 3, kyberPublic,
		kyberSig, identity.GetIdentityKey())
	must(err)

	return bundle
}
