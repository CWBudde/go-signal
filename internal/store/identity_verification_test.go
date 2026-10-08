//go:build cgo || libsignal_go

package store_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

const (
	verificationUntrusted  = "untrusted"
	verificationVerified   = "trusted-verified"
	verificationUnverified = "trusted-unverified"
)

var errVerificationRollback = errors.New("rollback verification")

func protocolIdentity(t *testing.T, data *store.Store, account, peerID string, key []byte) {
	t.Helper()

	err := data.IdentityTestSQL(t.Context(), `INSERT INTO signalmeow_identity_keys
 (account_id,their_service_id,key,trust_level) VALUES ($1,$2,$3,'UNVERIFIED')
 ON CONFLICT(account_id,their_service_id) DO UPDATE SET key=excluded.key`, account, peerID, key)
	if err != nil {
		t.Fatal(err)
	}
}

//nolint:cyclop,funlen // exact-key transitions and preserved history scenario
func TestApplyIdentityTrust(t *testing.T) {
	t.Parallel()
	data := verificationStore(t)
	peerID := uuid.NewString()
	key := []byte{5, 1}
	newKey := []byte{5, 2}
	seen := time.Unix(100, 0).UTC()

	rec := store.IdentityRecord{
		ServiceID: peerID, Key: key, Trust: verificationUntrusted, FirstSeen: seen,
		ChangedAt: seen, PreviousKey: []byte{5, 0}, PendingEvent: true,
	}

	err := data.PutIdentity(t.Context(), rec)
	if err != nil {
		t.Fatal(err)
	}
	// A matching key stored for another protocol account must not authorize this account.
	other := newDevice(t, uuid.New())

	err = data.Devices.PutDevice(t.Context(), other)
	if err != nil {
		t.Fatal(err)
	}

	protocolIdentity(t, data, other.ACI.String(), peerID, key)
	applyTrust(t, data, peerID, key, verificationVerified, false)
	protocolIdentity(t, data, testACI, peerID, key)
	applyTrust(t, data, peerID, newKey, verificationVerified, false)

	for _, state := range []string{verificationVerified, verificationUnverified, verificationUntrusted} {
		applyTrust(t, data, peerID, key, state, true)
		applyTrust(t, data, peerID, key, state, true)

		got, err := data.Identity(t.Context(), peerID)
		if err != nil || got == nil || got.Trust != state ||
			!got.FirstSeen.Equal(seen) || !got.ChangedAt.Equal(seen) ||
			!bytes.Equal(got.PreviousKey, rec.PreviousKey) || got.PendingEvent {
			t.Fatalf("updated record=%+v,%v", got, err)
		}
	}
	// Protocol replacement arrives before the facade observes it.
	protocolIdentity(t, data, testACI, peerID, newKey)
	applyTrust(t, data, peerID, key, verificationVerified, false)
	// Facade replacement must also block a stale update even if protocol still has old key.
	rec.Key = newKey

	err = data.PutIdentity(t.Context(), rec)
	if err != nil {
		t.Fatal(err)
	}

	protocolIdentity(t, data, testACI, peerID, key)
	applyTrust(t, data, peerID, key, verificationVerified, false)

	got, err := data.Identity(t.Context(), peerID)
	if err != nil || got == nil || got.Trust != verificationUntrusted ||
		!bytes.Equal(got.Key, newKey) || !got.PendingEvent {
		t.Fatalf("stale update replaced current identity=%+v,%v", got, err)
	}
	// Legacy protocol-only identities are supported; unknown keys are never imported.
	legacy := uuid.NewString()
	applyTrust(t, data, legacy, key, verificationVerified, false)

	unknown, err := data.Identity(t.Context(), legacy)
	if err != nil || unknown != nil {
		t.Fatalf("unknown imported=%+v,%v", unknown, err)
	}

	protocolIdentity(t, data, testACI, legacy, key)
	applyTrust(t, data, legacy, key, verificationVerified, true)

	got, err = data.Identity(t.Context(), legacy)
	if err != nil || got == nil || got.Trust != verificationVerified {
		t.Fatalf("legacy record=%+v,%v", got, err)
	}
}

func applyTrust(t *testing.T, data *store.Store, peerID string, key []byte, trust string, want bool) {
	t.Helper()

	got, err := data.ApplyIdentityTrust(t.Context(), testACI, peerID, key, trust)
	if err != nil || got != want {
		t.Fatalf("apply trust=%v,%v;want %v", got, err, want)
	}
}

//nolint:cyclop // transaction rollback and failure/retry scenario
func TestApplyIdentityTrustTransaction(t *testing.T) {
	t.Parallel()
	data := verificationStore(t)

	device := newDevice(t, uuid.MustParse(testACI))

	err := data.Devices.PutDevice(t.Context(), device)
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := data.Devices.DeviceByACI(t.Context(), device.ACI)
	if err != nil {
		t.Fatal(err)
	}

	peerID := uuid.NewString()
	key := []byte{5, 1}
	protocolIdentity(t, data, testACI, peerID, key)

	sentinel := errVerificationRollback

	err = loaded.DoDecryptionTxn(t.Context(), func(ctx context.Context) error {
		applied, writeErr := data.ApplyIdentityTrust(ctx, testACI, peerID, key, verificationVerified)
		if writeErr != nil || !applied {
			t.Fatalf("apply=%v,%v", applied, writeErr)
		}

		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}

	got, err := data.Identity(t.Context(), peerID)
	if err != nil || got != nil {
		t.Fatalf("rollback identity=%+v,%v", got, err)
	}
	// Trigger failure must return an error without creating trust; retry succeeds.
	err = data.IdentityTestSQL(t.Context(), `CREATE TRIGGER fail_verification BEFORE INSERT ON gosignal_identities
 BEGIN SELECT RAISE(ABORT,'verification failure'); END`)
	if err != nil {
		t.Fatal(err)
	}

	applied, err := data.ApplyIdentityTrust(t.Context(), testACI, peerID, key, verificationVerified)
	if err == nil || applied {
		t.Fatalf("write failure=%v,%v", applied, err)
	}

	err = data.IdentityTestSQL(t.Context(), `DROP TRIGGER fail_verification`)
	if err != nil {
		t.Fatal(err)
	}

	applyTrust(t, data, peerID, key, verificationVerified, true)
}

func TestApplyIdentityTrustAccountIsolation(t *testing.T) {
	t.Parallel()
	dir := openDir(t, io.Discard)

	a := openAccount(t, dir)

	err := a.Devices.PutDevice(t.Context(), newDevice(t, uuid.MustParse(testACI)))
	if err != nil {
		t.Fatal(err)
	}

	b, err := dir.OpenAccount(t.Context(), uuid.NewString(), zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = b.Close() })

	peerID := uuid.NewString()
	key := []byte{5, 1}
	protocolIdentity(t, a, testACI, peerID, key)
	applyTrust(t, a, peerID, key, verificationVerified, true)
	applyTrust(t, b, peerID, key, verificationVerified, false)
}

func verificationStore(t *testing.T) *store.Store {
	t.Helper()

	data := openAccount(t, openDir(t, io.Discard))

	err := data.Devices.PutDevice(t.Context(), newDevice(t, uuid.MustParse(testACI)))
	if err != nil {
		t.Fatal(err)
	}

	return data
}
