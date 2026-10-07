//go:build cgo || libsignal_go

package signal_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/google/uuid"
)

func TestPNIIdentityTrustLifecycle(t *testing.T) {
	t.Parallel()

	for _, localPNI := range []bool{false, true} {
		name := "aci-store"
		if localPNI {
			name = "pni-store"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			checkPNITrustLifecycle(t, localPNI)
		})
	}
}

//nolint:cyclop,funlen // one trust lifecycle, checked step by step
func checkPNITrustLifecycle(t *testing.T, localPNI bool) {
	t.Helper()
	env := newTrustEnv(t)

	identities := env.device.ACIIdentityStore
	if localPNI {
		identities = env.device.PNIIdentityStore
	}

	peer := libsignalgo.NewPNIServiceID(uuid.MustParse(aliceACI))
	rcpt := signal.Recipient{PNI: aliceACI}
	oldKey, changed := newIdentityKey(t), newIdentityKey(t)
	env.saveAlice(t, oldKey) // ACI with the same UUID must remain independent.

	for i, key := range []*libsignalgo.IdentityKey{oldKey, changed} {
		_, err := identities.SaveIdentityKey(t.Context(), peer, key)
		if err != nil {
			t.Fatal(err)
		}

		trusted, err := identities.IsTrustedIdentity(t.Context(), peer, key, sending)
		if err != nil || trusted != (i == 0) {
			t.Errorf("key %d sending trust = %v, %v", i, trusted, err)
		}
	}

	ids, err := env.client.Identities(t.Context(), &rcpt)
	if err != nil || len(ids) != 1 || ids[0].Trust != signal.TrustUntrusted {
		t.Fatalf("list PNI = %+v, %v", ids, err)
	}

	// A mismatch must not grant trust or clear the pending change.
	_, err = env.client.TrustIdentity(t.Context(), rcpt, strings.Repeat("0", signal.SafetyNumberDigits))
	if !errors.Is(err, signal.ErrSafetyNumberMismatch) {
		t.Fatalf("wrong safety number: %v", err)
	}

	events := env.report(t)
	if len(events) != 1 || events[0].Recipient != rcpt || events[0].NewFingerprint != fingerprintOf(t, changed) {
		t.Errorf("PNI changes = %+v", events)
	}

	number := checkPNISafetyNumber(t, env, peer, changed)

	identity, err := env.client.TrustIdentity(t.Context(), rcpt, number)
	if err != nil || identity.Trust != signal.TrustVerified {
		t.Fatalf("verify = %+v, %v", identity, err)
	}

	trusted, err := identities.IsTrustedIdentity(t.Context(), peer, changed, sending)
	if err != nil || !trusted {
		t.Errorf("verified PNI refused: %v", err)
	}

	if env.alice(t).Trust != signal.TrustUnverified {
		t.Error("PNI verification changed ACI trust")
	}

	receivingOK, err := identities.IsTrustedIdentity(t.Context(), peer, oldKey, receiving)
	if err != nil || !receivingOK {
		t.Errorf("receiving refused: %v", err)
	}

	trusted, err = identities.IsTrustedIdentity(t.Context(), peer, oldKey, sending)
	if err != nil || trusted {
		t.Errorf("old PNI key trusted: %v", err)
	}
}

func checkPNISafetyNumber(
	t *testing.T, env *trustEnv, peer libsignalgo.ServiceID, key *libsignalgo.IdentityKey,
) string {
	t.Helper()

	number, err := env.client.SafetyNumber(t.Context(), signal.Recipient{PNI: peer.UUID.String()})
	if err != nil {
		t.Fatal(err)
	}

	remote, err := libsignalgo.DeserializePublicKey(key.TrySerialize())
	if err != nil {
		t.Fatal(err)
	}

	generated, err := libsignalgo.NewFingerprint(libsignalgo.FingerprintVersion(5200), libsignalgo.FingerprintVersionV2,
		libsignalgo.NewACIServiceID(env.device.ACI).Bytes(), env.device.ACIIdentityKeyPair.GetPublicKey(),
		peer.Bytes(), remote)
	if err != nil {
		t.Fatal(err)
	}

	wantNumber, err := generated.DisplayString()
	if err != nil {
		t.Fatal(err)
	}

	wantScan, err := generated.ScannableEncoding()
	if err != nil {
		t.Fatal(err)
	}

	if number.Number != wantNumber || !bytes.Equal(number.Scannable, wantScan) {
		t.Error("PNI safety number lost typed ID or used local PNI key")
	}

	return wantNumber
}

func TestPNIIdentityLookup(t *testing.T) {
	t.Parallel()

	env := newTrustEnv(t)
	key := newIdentityKey(t)
	peer := libsignalgo.NewPNIServiceID(uuid.MustParse(aliceACI))

	_, err := env.device.ACIIdentityStore.SaveIdentityKey(t.Context(), peer, key)
	if err != nil {
		t.Fatal(err)
	}

	ownIDs := []libsignalgo.ServiceID{env.device.ACIServiceID(), libsignalgo.NewPNIServiceID(env.device.PNI)}
	for _, own := range ownIDs {
		_, err = env.device.ACIIdentityStore.SaveIdentityKey(t.Context(), own, key)
		if err != nil {
			t.Fatal(err)
		}
	}

	ids, err := env.client.Identities(t.Context(), nil)
	if err != nil || len(ids) != 1 || ids[0].Recipient != (signal.Recipient{PNI: aliceACI}) {
		t.Fatalf("all identities = %+v, %v", ids, err)
	}

	for _, rcpt := range []signal.Recipient{{PNI: "invalid-pni"}, {PNI: uuid.Nil.String()}, {}} {
		_, err = env.client.Identities(t.Context(), &rcpt)
		if !errors.Is(err, signal.ErrUnresolvable) {
			t.Errorf("invalid %+v: %v", rcpt, err)
		}
	}

	unknown := signal.Recipient{PNI: bobACI}

	_, err = env.client.TrustIdentity(t.Context(), unknown, "")
	if !errors.Is(err, signal.ErrUnknownIdentity) {
		t.Errorf("unknown PNI: %v", err)
	}
}

//nolint:cyclop,funlen // one real session replacement and recovery scenario
func TestPNIIdentitySessionChange(t *testing.T) {
	t.Parallel()

	env := newTrustEnv(t)
	oldKey, changed := newKeyPair(t), newKeyPair(t)
	peer := libsignalgo.NewPNIServiceID(uuid.MustParse(aliceACI))

	remote, err := peer.Address(1)
	if err != nil {
		t.Fatal(err)
	}

	local, err := env.device.ACIServiceID().Address(uint(env.device.DeviceID))
	if err != nil {
		t.Fatal(err)
	}

	err = libsignalgo.ProcessPreKeyBundle(t.Context(), preKeyBundle(t, oldKey, 1), remote, local,
		env.device.ACISessionStore, env.device.ACIIdentityStore)
	if err != nil {
		t.Fatal(err)
	}

	stale, err := env.device.ACISessionStore.LoadSession(t.Context(), remote)
	if err != nil || stale == nil {
		t.Fatalf("load initial session: %v", err)
	}

	_, err = env.device.ACIIdentityStore.SaveIdentityKey(t.Context(), peer, changed.GetIdentityKey())
	if err != nil {
		t.Fatal(err)
	}

	sessions, err := env.device.ACISessionStore.AllSessionsForServiceID(t.Context(), peer)
	if err != nil || len(sessions) != 0 {
		t.Fatalf("stale PNI sessions = %d, %v", len(sessions), err)
	}

	bundle := preKeyBundle(t, changed, 1)
	err = libsignalgo.ProcessPreKeyBundle(t.Context(), bundle, remote, local,
		env.device.ACISessionStore, env.device.ACIIdentityStore)

	var cryptoErr *libsignalgo.SignalError
	if !errors.As(err, &cryptoErr) || cryptoErr.Code != libsignalgo.ErrorCodeUntrustedIdentity {
		t.Fatalf("changed PNI prekey accepted: %v", err)
	}

	// Model an older account retaining a session after its identity changed.
	err = env.device.ACISessionStore.StoreSession(t.Context(), remote, stale)
	if err != nil {
		t.Fatal(err)
	}

	_, err = env.client.TrustIdentity(t.Context(), signal.Recipient{PNI: aliceACI}, "")
	if err != nil {
		t.Fatal(err)
	}

	sessions, err = env.device.ACISessionStore.AllSessionsForServiceID(t.Context(), peer)
	if err != nil || len(sessions) != 0 {
		t.Fatalf("stale sessions after PNI trust = %d, %v", len(sessions), err)
	}

	err = libsignalgo.ProcessPreKeyBundle(t.Context(), bundle, remote, local,
		env.device.ACISessionStore, env.device.ACIIdentityStore)
	if err != nil {
		t.Fatalf("trusted PNI prekey refused: %v", err)
	}
}
