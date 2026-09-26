//go:build cgo && !purego

package signal_test

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
)

func TestHPKERoundTrip(t *testing.T) {
	t.Parallel()

	keys := identityKeys(t)
	public, private := serializeKeys(t, keys)

	sealed, err := signal.HPKESeal(public, []byte("hello"), []byte("info"), []byte("aad"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	plain, err := signal.HPKEOpen(private, sealed, []byte("info"), []byte("aad"))
	if err != nil || string(plain) != "hello" {
		t.Fatalf("open: %q, %v", plain, err)
	}

	_, err = signal.HPKEOpen(private, sealed, []byte("info"), []byte("other"))
	if err == nil {
		t.Error("open with the wrong associated data succeeded")
	}
}

func TestDevicesFromResponse(t *testing.T) {
	t.Parallel()

	const (
		regID  = "registrationId"
		laptop = "laptop"
	)

	keys := identityKeys(t)
	public, _ := serializeKeys(t, keys)
	created := time.Date(2026, 9, 20, 12, 30, 0, 0, time.UTC)

	name, err := signalmeow.EncryptDeviceName(laptop, keys.GetPublicKey())
	if err != nil {
		t.Fatal(err)
	}

	// Associated data: device ID as one byte, then the registration ID (big-endian).
	aad := binary.BigEndian.AppendUint32([]byte{2}, 4242)

	sealedCreated, err := signal.HPKESeal(public,
		binary.BigEndian.AppendUint64(nil, uint64(created.UnixMilli())), []byte("deviceCreatedAt"), aad)
	if err != nil {
		t.Fatal(err)
	}

	lastSeen := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)

	body, err := json.Marshal(map[string]any{"devices": []map[string]any{
		{"id": 1, "lastSeen": lastSeen.UnixMilli(), regID: 1},
		{
			"id": 2, "name": base64.StdEncoding.EncodeToString(name), "lastSeen": lastSeen.UnixMilli(),
			regID: 4242, "createdAtCiphertext": sealedCreated,
		},
		// A plain-text name and a creation time sealed for another device ID.
		{"id": 3, "name": "old-client", regID: 7, "createdAtCiphertext": sealedCreated},
	}})
	if err != nil {
		t.Fatal(err)
	}

	devices, err := signal.DevicesFromResponse(body, keys, 2)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	want := []signal.Device{
		{ID: 1, LastSeen: lastSeen},
		{ID: 2, Name: laptop, Created: created, LastSeen: lastSeen, Current: true},
		{ID: 3, Name: "old-client"},
	}

	if len(devices) != len(want) {
		t.Fatalf("got %+v", devices)
	}

	for i := range want {
		if devices[i] != want[i] {
			t.Errorf("device %d: got %+v, want %+v", i, devices[i], want[i])
		}
	}
}

func identityKeys(t *testing.T) *libsignalgo.IdentityKeyPair {
	t.Helper()

	keys, err := libsignalgo.GenerateIdentityKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	return keys
}

func serializeKeys(t *testing.T, keys *libsignalgo.IdentityKeyPair) ([]byte, []byte) {
	t.Helper()

	public, err := keys.GetPublicKey().Serialize()
	if err != nil {
		t.Fatal(err)
	}

	private, err := keys.GetPrivateKey().Serialize()
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(public, private) {
		t.Fatal("keys are equal")
	}

	return public, private
}
