//go:build cgo || purego

package store_test

import (
	"bytes"
	"testing"

	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
)

// Rejected ciphertext must neither consume a skipped key nor commit a new
// session/identity. A subsequent valid decrypt in protocolStateStep verifies
// that the same stored state is still usable, including after backend changes.
func rejectPendingSessionTampering(t *testing.T, sender, receiver protocolAccount, ciphertext []byte, preKey bool) {
	t.Helper()

	before := sessionSnapshot(t, receiver, sender)
	identityBefore := identitySnapshot(t, receiver, sender)
	bad := bytes.Clone(ciphertext)
	bad[len(bad)-1] ^= 1

	var (
		plain []byte
		err   error
	)

	if preKey {
		keys := receiver.device.ACIPreKeyStore
		pre := stateValue(stateValue(keys.LoadPreKey(t.Context(), 1))(t).Serialize())(t)
		signed := stateValue(stateValue(keys.LoadSignedPreKey(t.Context(), 2))(t).Serialize())(t)
		kyber := stateValue(stateValue(keys.LoadKyberPreKey(t.Context(), 3))(t).Serialize())(t)
		message := stateValue(libsignalgo.DeserializePreKeyMessage(bad))(t)
		plain, err = libsignalgo.DecryptPreKey(t.Context(), message, sender.addr, receiver.addr,
			receiver.device.ACISessionStore, receiver.device.ACIIdentityStore, keys, keys, keys)
		// These must still exist and retain exactly the same key material.
		preAfter := stateValue(keys.LoadPreKey(t.Context(), 1))(t)
		signedAfter := stateValue(keys.LoadSignedPreKey(t.Context(), 2))(t)

		kyberAfter := stateValue(keys.LoadKyberPreKey(t.Context(), 3))(t)
		if preAfter == nil || signedAfter == nil || kyberAfter == nil {
			t.Fatal("failed prekey decrypt consumed a key")
		}

		stateEqual(t, stateValue(preAfter.Serialize())(t), pre)
		stateEqual(t, stateValue(signedAfter.Serialize())(t), signed)
		stateEqual(t, stateValue(kyberAfter.Serialize())(t), kyber)
	} else {
		message := stateValue(libsignalgo.DeserializeMessage(bad))(t)
		plain, err = libsignalgo.Decrypt(t.Context(), message, sender.addr, receiver.addr,
			receiver.device.ACISessionStore, receiver.device.ACIIdentityStore)
	}

	if err == nil || len(plain) != 0 {
		t.Fatalf("tampered session message: got %x, error %v", plain, err)
	}

	stateEqual(t, sessionSnapshot(t, receiver, sender), before)
	stateEqual(t, identitySnapshot(t, receiver, sender), identityBefore)
}

func sessionSnapshot(t *testing.T, account, peer protocolAccount) []byte {
	t.Helper()

	record := stateValue(account.device.ACISessionStore.LoadSession(t.Context(), peer.addr))(t)
	if record == nil {
		return nil
	}

	return stateValue(record.Serialize())(t)
}

func identitySnapshot(t *testing.T, account, peer protocolAccount) []byte {
	t.Helper()

	key := stateValue(account.device.ACIIdentityStore.GetIdentityKey(t.Context(), peer.device.ACIServiceID()))(t)
	if key == nil {
		return nil
	}

	return stateValue(key.Serialize())(t)
}

func rejectPendingGroupTampering(t *testing.T, sender, receiver protocolAccount, distribution uuid.UUID, ct []byte) {
	t.Helper()

	keys := receiver.device.SenderKeyStore
	before := stateValue(stateValue(keys.LoadSenderKey(t.Context(), sender.addr, distribution))(t).Serialize())(t)
	bad := bytes.Clone(ct)
	bad[len(bad)-1] ^= 1

	plain, err := libsignalgo.GroupDecrypt(t.Context(), bad, sender.addr, keys)
	if err == nil || len(plain) != 0 {
		t.Fatalf("tampered group message: got %x, error %v", plain, err)
	}

	after := stateValue(stateValue(keys.LoadSenderKey(t.Context(), sender.addr, distribution))(t).Serialize())(t)
	stateEqual(t, after, before)
}
