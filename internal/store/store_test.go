//go:build cgo

package store_test

import (
	"errors"
	"io"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	mstore "go.mau.fi/mautrix-signal/pkg/signalmeow/store"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/types"
)

func TestOpenAccount(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)
	data := openAccount(t, dir)

	if got := perm(t, filepath.Join(dir.AccountDir(testACI), "account.db")); got != 0o600 {
		t.Errorf("account.db perm = %o, want 600", got)
	}

	devices, err := data.Devices.GetAllDevices(t.Context())
	if err != nil || len(devices) != 0 {
		t.Fatalf("fresh store: %d devices, %v", len(devices), err)
	}
}

func TestMetaSurvivesReopen(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)
	data := openAccount(t, dir)

	for _, value := range []string{"v1", "v2"} {
		err := data.SetMeta(t.Context(), "k", value)
		if err != nil {
			t.Fatalf("set meta: %v", err)
		}
	}

	err := data.Close()
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopening an already migrated database must be a no-op and keep our tables.
	data = openAccount(t, dir)

	value, ok, err := data.Meta(t.Context(), "k")
	if err != nil || !ok || value != "v2" {
		t.Errorf("meta = %q, %v, %v", value, ok, err)
	}

	_, ok, err = data.Meta(t.Context(), "missing")
	if err != nil || ok {
		t.Errorf("missing meta = %v, %v", ok, err)
	}
}

// openAccount opens the testACI database and closes it at the end of the test.
func openAccount(t *testing.T, dir *store.Dir) *store.Store {
	t.Helper()

	data, err := dir.OpenAccount(t.Context(), testACI, zerolog.Nop())
	if err != nil {
		t.Fatalf("open account: %v", err)
	}

	t.Cleanup(func() { _ = data.Close() })

	return data
}

func TestLinkStore(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)
	links := dir.NewLinkStore(zerolog.Nop())

	aci := uuid.MustParse(testACI)

	_, err := links.DeviceByACI(t.Context(), aci)
	if err == nil {
		t.Fatal("lookup before PutDevice should fail")
	}

	err = links.PutDevice(t.Context(), newDevice(t, aci))
	if err != nil {
		t.Fatalf("put device: %v", err)
	}

	device, err := links.DeviceByACI(t.Context(), aci)
	if err != nil || !device.IsDeviceLoggedIn() {
		t.Fatalf("device = %+v, %v", device, err)
	}

	_, err = dir.Lock(testACI)
	if !errors.Is(err, store.ErrAccountInUse) {
		t.Errorf("linking must hold the account lock, got %v", err)
	}

	data, lock := links.Take()
	if data == nil || lock == nil {
		t.Fatal("Take returned nothing")
	}

	err = links.Close()
	if err != nil {
		t.Fatalf("close after Take: %v", err)
	}

	_ = data.Close()
	_ = lock.Unlock()
}

// newDevice returns logged-in device data for aci.
func newDevice(t *testing.T, aci uuid.UUID) *mstore.DeviceData {
	t.Helper()

	aciKeys, err := libsignalgo.GenerateIdentityKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	pniKeys, err := libsignalgo.GenerateIdentityKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	return &mstore.DeviceData{
		ACIIdentityKeyPair: aciKeys,
		PNIIdentityKeyPair: pniKeys,
		ACI:                aci,
		PNI:                uuid.New(),
		DeviceID:           2,
		Number:             "+15550100",
		Password:           "secret",
	}
}

func TestGroupIdentifiers(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := openAccount(t, openDir(t, io.Discard))

	// One database can hold several accounts; each only sees its own groups.
	own, other := uuid.MustParse(testACI), uuid.New()
	groups := map[uuid.UUID][]types.GroupIdentifier{
		own:   {"group-b", "group-a"},
		other: {"group-c"},
	}

	for aci, ids := range groups {
		err := data.Devices.PutDevice(ctx, newDevice(t, aci))
		if err != nil {
			t.Fatalf("put device: %v", err)
		}

		device, err := data.Devices.DeviceByACI(ctx, aci)
		if err != nil {
			t.Fatalf("load device: %v", err)
		}

		for _, id := range ids {
			err = device.GroupStore.StoreMasterKey(ctx, id, types.SerializedGroupMasterKey("key-"+id))
			if err != nil {
				t.Fatalf("store master key: %v", err)
			}
		}
	}

	got, err := data.GroupIdentifiers(ctx, testACI)
	if err != nil || !slices.Equal(got, []string{"group-a", "group-b"}) {
		t.Errorf("GroupIdentifiers = %v, %v; want the own groups, sorted", got, err)
	}

	got, err = data.GroupIdentifiers(ctx, uuid.NewString())
	if err != nil || len(got) != 0 {
		t.Errorf("GroupIdentifiers of an unknown account = %v, %v", got, err)
	}
}
