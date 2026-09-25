//go:build cgo

package signal_test

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

const (
	cachedNumber = "+15550111"
	cachedACI    = "22222222-2222-4222-8222-222222222222"
	cachedPNI    = "33333333-3333-4333-8333-333333333333"
)

func TestUsernameHash(t *testing.T) {
	t.Parallel()

	hash, err := signal.UsernameHash("He110.01")
	if err != nil || len(hash) != 32 {
		t.Fatalf("UsernameHash = %x, %v; want 32 bytes", hash, err)
	}

	// The nickname is case-insensitive.
	lower, err := signal.UsernameHash("he110.01")
	if err != nil || !bytes.Equal(hash, lower) {
		t.Errorf("hash of he110.01 = %x, want %x (%v)", lower, hash, err)
	}

	other, err := signal.UsernameHash("He110.02")
	if err != nil || bytes.Equal(hash, other) {
		t.Errorf("different discriminators hash the same (%v)", err)
	}

	// Invalid usernames from libsignal's tests.
	for _, name := range []string{"0zerostart.42", "no_discriminator", "🦀.42", "zero.00", "short.1"} {
		_, err := signal.UsernameHash(name)
		if !errors.Is(err, signal.ErrInvalidUsername) {
			t.Errorf("UsernameHash(%q) error = %v, want ErrInvalidUsername", name, err)
		}
	}
}

func TestACIFromUsernameResponse(t *testing.T) {
	t.Parallel()

	aci, err := signal.ACIFromUsernameResponse(http.StatusOK, []byte(`{"uuid":"`+cachedACI+`"}`))
	if err != nil || aci.String() != cachedACI {
		t.Errorf("ok: got %v, %v", aci, err)
	}

	_, err = signal.ACIFromUsernameResponse(http.StatusNotFound, nil)
	if !errors.Is(err, signal.ErrNotOnSignal) {
		t.Errorf("404: got %v, want ErrNotOnSignal", err)
	}

	_, err = signal.ACIFromUsernameResponse(http.StatusTooManyRequests, nil)
	if err == nil || errors.Is(err, signal.ErrNotOnSignal) {
		t.Errorf("429: got %v", err)
	}

	_, err = signal.ACIFromUsernameResponse(http.StatusOK, []byte(`{}`))
	if !errors.Is(err, signal.ErrNotOnSignal) {
		t.Errorf("no uuid: got %v, want ErrNotOnSignal", err)
	}
}

func TestResolveOffline(t *testing.T) {
	t.Parallel()

	dataDir := seedAccount(t)
	cacheRecipient(t, dataDir)

	client, err := signal.Open(t.Context(), signal.Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = client.Close() }()

	// A cached number and an ACI resolve without a connection.
	got, err := client.Resolve(t.Context(), []signal.Recipient{{Number: cachedNumber}, {ACI: seededACI}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	want := []signal.Recipient{{ACI: cachedACI, PNI: cachedPNI, Number: cachedNumber}, {ACI: seededACI}}
	if got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Resolve = %+v, want %+v", got, want)
	}

	// An unknown number needs contact discovery; an invalid username fails before the network.
	_, err = client.Resolve(t.Context(), []signal.Recipient{{Number: "+15550199"}, {Username: "nodiscriminator"}, {}})
	for _, want := range []error{signal.ErrNotConnected, signal.ErrInvalidUsername, signal.ErrUnresolvable} {
		if !errors.Is(err, want) {
			t.Errorf("Resolve error = %v, want %v", err, want)
		}
	}
}

// cacheRecipient stores cachedNumber's ACI and PNI in the seeded account's database, as a
// contact discovery lookup would.
func cacheRecipient(t *testing.T, dataDir string) {
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

	_, err = device.RecipientStore.UpdateRecipientE164(t.Context(),
		uuid.MustParse(cachedACI), uuid.MustParse(cachedPNI), cachedNumber)
	if err != nil {
		t.Fatal(err)
	}
}
