//go:build cgo || libsignal_go

package store_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// allocatorAbortExit is the exit status of the abort step, which ends its process right after
// a committed reservation, before any key is generated.
const allocatorAbortExit = 17

// allocatorBatch is the number of EC and Kyber IDs every reserve step takes per scope.
const allocatorBatch = 5

// Steps of the allocator sequence.
const (
	actionInit    = "init"
	actionReserve = "reserve"
	actionAbort   = "abort"
	actionCheck   = "check"
)

var errAllocatorRollback = errors.New("rollback")

// allocatorSeed is the sparse initial state of a scope: ACI and PNI use different counters and
// active references, and the PNI EC counter starts at zero.
func allocatorSeed(scope libsignalgo.ServiceIDType) mstore.PreKeyAllocatorState {
	if scope == libsignalgo.ServiceIDTypePNI {
		return mstore.PreKeyAllocatorState{
			NextEC: 0, NextSigned: 4, NextKyber: 6, ActiveSigned: new(uint32(3)), ActiveLastResort: new(uint32(5)),
		}
	}

	return mstore.PreKeyAllocatorState{
		NextEC: 10, NextSigned: 8, NextKyber: 31, ActiveSigned: new(uint32(7)), ActiveLastResort: new(uint32(30)),
	}
}

// TestPreKeyAllocatorAccountStore runs the backend-step sequence (without the process abort)
// in one process against go-signal's account database.
func TestPreKeyAllocatorAccountStore(t *testing.T) {
	t.Parallel()

	path := t.TempDir()
	for _, action := range []string{actionInit, actionReserve, actionReserve, actionCheck} {
		allocatorStep(t, path, action)
	}

	// Seeding joins the caller's transaction: a rollback persists neither records nor state.
	dir := stateValue(store.OpenDir(path, slog.New(slog.DiscardHandler)))(t)
	data := stateValue(dir.OpenAccount(t.Context(), secondACI, zerolog.Nop()))(t)

	t.Cleanup(func() { stateCheck(t, data.Close()) })

	stateCheck(t, data.Devices.PutDevice(t.Context(), newDevice(t, uuid.MustParse(secondACI))))
	device := stateValue(data.Devices.DeviceByACI(t.Context(), uuid.MustParse(secondACI)))(t)

	err := device.DoDecryptionTxn(t.Context(), func(ctx context.Context) error {
		stateCheck(t, device.ACIPreKeyStore.StorePreKey(ctx, 1, signalmeow.GeneratePreKeys(1, 1)[0]))
		stateCheck(t, data.Devices.SeedPreKeyAllocator(ctx, device.ACI, device.ACIServiceID(),
			mstore.PreKeyAllocatorState{NextEC: 2, NextSigned: 1, NextKyber: 1}))

		return errAllocatorRollback
	})
	if !errors.Is(err, errAllocatorRollback) {
		t.Fatalf("seed transaction = %v", err)
	}

	_, err = device.ACIPreKeyStore.PreKeyAllocatorState(t.Context())
	if !errors.Is(err, mstore.ErrPreKeyAllocatorUninitialized) {
		t.Fatalf("state after rollback = %v, want uninitialized", err)
	}

	if rec := stateValue(device.ACIPreKeyStore.LoadPreKey(t.Context(), 1))(t); rec != nil {
		t.Fatal("record survived rollback")
	}
}

// TestPreKeyAllocatorBackendStep is one step of scripts/test-prekey-allocators.sh, which runs
// the steps in separate native and pure-Go binaries against the same database.
func TestPreKeyAllocatorBackendStep(t *testing.T) {
	t.Parallel()

	path := os.Getenv("GOSIGNAL_TEST_ALLOCATOR_DIR")
	if path == "" {
		t.Skip("run through scripts/test-prekey-allocators.sh")
	}

	action := os.Getenv("GOSIGNAL_TEST_ALLOCATOR_ACTION")
	if !slices.Contains([]string{actionInit, actionReserve, actionAbort, actionCheck}, action) {
		t.Fatalf("unknown allocator test action %q", action)
	}

	allocatorStep(t, path, action)
}

func allocatorBackend() string {
	if store.SQLiteDriverForTest == "sqlite3" {
		return "cgo"
	}

	return "purego"
}

// allocatorStep runs one action on the testACI account below path and appends it to the
// ledger, which the check action verifies.
func allocatorStep(t *testing.T, path, action string) {
	t.Helper()

	dir := stateValue(store.OpenDir(path, slog.New(slog.DiscardHandler)))(t)
	data := stateValue(dir.OpenAccount(t.Context(), testACI, zerolog.Nop()))(t)

	if action == actionInit {
		initAllocatorAccount(t, data)
		stateCheck(t, data.Close())
		appendLedger(t, path, action)

		return
	}

	device := stateValue(data.Devices.DeviceByACI(t.Context(), uuid.MustParse(testACI)))(t)

	switch action {
	case actionReserve:
		entry := reserveAndConsume(t, device)
		stateCheck(t, data.Close())
		appendLedger(t, path, action+entry)
	case actionAbort:
		// Commit an ACI reservation, record it, then end the process without generating keys
		// or closing the database.
		var entry strings.Builder

		for _, kind := range []mstore.PreKeyKind{mstore.PreKeyEC, mstore.PreKeyKyber} {
			start := stateValue(device.ACIPreKeyStore.ReservePreKeyIDs(t.Context(), kind, allocatorBatch))(t)
			fmt.Fprintf(&entry, " aci-%s=%d", kind, start)
		}

		appendLedger(t, path, action+entry.String())
		os.Exit(allocatorAbortExit)
	case actionCheck:
		checkAllocatorAccount(t, path, device)
		stateCheck(t, data.Close())
		appendLedger(t, path, action)
	}
}

func initAllocatorAccount(t *testing.T, data *store.Store) {
	t.Helper()

	stateCheck(t, data.Devices.PutDevice(t.Context(), newDevice(t, uuid.MustParse(testACI))))
	device := stateValue(data.Devices.DeviceByACI(t.Context(), uuid.MustParse(testACI)))(t)

	err := device.DoDecryptionTxn(t.Context(), func(ctx context.Context) error {
		for _, pks := range []mstore.PreKeyStore{device.ACIPreKeyStore, device.PNIPreKeyStore} {
			seed := allocatorSeed(pks.GetServiceID().Type)
			ikp := identityFor(device, pks)
			signed := signalmeow.GenerateSignedPreKey(*seed.ActiveSigned, ikp)

			stateCheck(t, pks.StoreSignedPreKey(ctx, *seed.ActiveSigned, signed))
			lastResort := signalmeow.GenerateKyberPreKeys(*seed.ActiveLastResort, 1, ikp)[0]
			stateCheck(t, pks.StoreLastResortKyberPreKey(ctx, *seed.ActiveLastResort, lastResort))

			if seed.NextEC > 1 {
				id := uint32(seed.NextEC) - 1
				stateCheck(t, pks.StorePreKey(ctx, id, signalmeow.GeneratePreKeys(id, 1)[0]))
			}

			err := data.Devices.SeedPreKeyAllocator(ctx, device.ACI, pks.GetServiceID(), seed)
			if err != nil {
				return fmt.Errorf("seed %s: %w", pks.GetServiceID(), err)
			}
		}

		return nil
	})
	stateCheck(t, err)
}

func identityFor(device *mstore.Device, pks mstore.PreKeyStore) *libsignalgo.IdentityKeyPair {
	if pks.GetServiceID().Type == libsignalgo.ServiceIDTypePNI {
		return device.PNIIdentityKeyPair
	}

	return device.ACIIdentityKeyPair
}

// reserveAndConsume reserves, stores and then consumes one EC and one Kyber batch per scope,
// and uses the last-resort key, which must be retained. It returns the ledger fields.
func reserveAndConsume(t *testing.T, device *mstore.Device) string {
	t.Helper()

	var entry strings.Builder

	for _, pks := range []mstore.PreKeyStore{device.ACIPreKeyStore, device.PNIPreKeyStore} {
		scope := strings.ToLower(pks.GetServiceID().Type.String())
		ctx := t.Context()

		ecStart := stateValue(pks.ReservePreKeyIDs(ctx, mstore.PreKeyEC, allocatorBatch))(t)
		for _, key := range signalmeow.GeneratePreKeys(ecStart, allocatorBatch) {
			stateCheck(t, pks.StorePreKey(ctx, 0, key))
			stateCheck(t, pks.RemovePreKey(ctx, stateValue(key.GetID())(t)))
		}

		kyberStart := stateValue(pks.ReservePreKeyIDs(ctx, mstore.PreKeyKyber, allocatorBatch))(t)
		for _, key := range signalmeow.GenerateKyberPreKeys(kyberStart, allocatorBatch, identityFor(device, pks)) {
			stateCheck(t, pks.StoreKyberPreKey(ctx, 0, key))
			stateCheck(t, pks.MarkKyberPreKeyUsed(ctx, stateValue(key.GetID())(t)))
		}

		stateCheck(t, pks.MarkKyberPreKeyUsed(ctx, *allocatorSeed(pks.GetServiceID().Type).ActiveLastResort))
		fmt.Fprintf(&entry, " %s-ec=%d %s-kyber=%d", scope, ecStart, scope, kyberStart)
	}

	return entry.String()
}

func appendLedger(t *testing.T, path, entry string) {
	t.Helper()

	f := stateValue(os.OpenFile(filepath.Join(path, "allocator-ledger"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600))(t)
	_, err := fmt.Fprintf(f, "%s %s\n", allocatorBackend(), entry)
	stateCheck(t, errors.Join(err, f.Close()))
}

// checkAllocatorAccount compares both scopes with the positions the ledger replay expects.
// Active references and last-resort keys stay put, and all ordinary keys were consumed.
func checkAllocatorAccount(t *testing.T, path string, device *mstore.Device) {
	t.Helper()

	next := replayAllocatorLedger(t, path)

	for _, pks := range []mstore.PreKeyStore{device.ACIPreKeyStore, device.PNIPreKeyStore} {
		scope := strings.ToLower(pks.GetServiceID().Type.String())
		want := allocatorSeed(pks.GetServiceID().Type)
		want.NextEC, want.NextKyber = next[scope+"-ec"], next[scope+"-kyber"]
		checkAllocatorScope(t, pks, want)
	}
}

// replayAllocatorLedger requires every reservation to start exactly where the previous one of
// its scope and kind ended, abandoned ones included, and returns the positions after the last.
func replayAllocatorLedger(t *testing.T, path string) map[string]uint64 {
	t.Helper()

	aci, pni := allocatorSeed(libsignalgo.ServiceIDTypeACI), allocatorSeed(libsignalgo.ServiceIDTypePNI)
	next := map[string]uint64{
		"aci-ec": aci.NextEC, "aci-kyber": aci.NextKyber, "pni-ec": pni.NextEC, "pni-kyber": pni.NextKyber,
	}

	ledger := stateValue(os.ReadFile(filepath.Join(path, "allocator-ledger")))(t)
	for line := range strings.Lines(string(ledger)) {
		for _, field := range strings.Fields(line)[2:] {
			key, value, _ := strings.Cut(field, "=")

			start := stateValue(strconv.ParseUint(value, 10, 32))(t)
			if start != next[key] {
				t.Fatalf("ledger %q: %s started at %d, want %d (reuse or skipped position)", line, key, start, next[key])
			}

			next[key] = start + allocatorBatch
		}
	}

	return next
}

func checkAllocatorScope(t *testing.T, pks mstore.PreKeyStore, want mstore.PreKeyAllocatorState) {
	t.Helper()

	got := stateValue(pks.PreKeyAllocatorState(t.Context()))(t)
	if got.NextEC != want.NextEC || got.NextSigned != want.NextSigned || got.NextKyber != want.NextKyber ||
		*got.ActiveSigned != *want.ActiveSigned || *got.ActiveLastResort != *want.ActiveLastResort {
		t.Fatalf("%s state = %+v, want %+v", pks.GetServiceID(), got, want)
	}

	if lr := stateValue(pks.IsKyberPreKeyLastResort(t.Context(), *want.ActiveLastResort))(t); !lr {
		t.Fatalf("%s last-resort key was not retained", pks.GetServiceID())
	}

	// Only the EC key stored next to the seed (ID NextEC-1 of the initial state) remains.
	wantEC := uint32(0)
	if allocatorSeed(pks.GetServiceID().Type).NextEC > 1 {
		wantEC = 1
	}

	ec, kyber := stateValue2(pks.PreKeyCounts(t.Context()))(t)
	if ec != wantEC || kyber != 0 {
		t.Fatalf("%s ordinary counts = %d/%d, want %d/0", pks.GetServiceID(), ec, kyber, wantEC)
	}
}

func stateValue2[A, B any](a A, b B, err error) func(*testing.T) (A, B) {
	return func(t *testing.T) (A, B) {
		t.Helper()
		stateCheck(t, err)

		return a, b
	}
}

func TestLinkStoreStoreProvisionedDevice(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	logs := slog.New(slog.DiscardHandler)
	path := t.TempDir()
	dir := stateValue(store.OpenDir(path, logs))(t)
	aci := uuid.MustParse(testACI)

	first := linkInput(t, aci)
	links := dir.NewLinkStore(zerolog.Nop())
	device := stateValue(links.StoreProvisionedDevice(ctx, first.data, first.keys))(t)
	assertProvisionedScopes(t, device)
	stateCheck(t, device.ACIPreKeyStore.StorePreKey(ctx, 0, signalmeow.GeneratePreKeys(41, 1)[0]))
	stateCheck(t, links.Close())

	// A failing relink (changed PNI) keeps the old state and releases the lock and database.
	data := stateValue(dir.OpenAccount(ctx, testACI, zerolog.Nop()))(t)
	stateCheck(t, data.IdentityTestSQL(ctx, `CREATE TRIGGER fail_link BEFORE INSERT ON signalmeow_prekey_allocators
		BEGIN SELECT RAISE(ABORT, 'injected link failure'); END`))
	stateCheck(t, data.Close())

	failing, retry := dir.NewLinkStore(zerolog.Nop()), linkInput(t, aci)

	dev, err := failing.StoreProvisionedDevice(ctx, retry.data, retry.keys)
	if err == nil || dev != nil {
		t.Fatalf("failing relink = %v, %v", dev, err)
	}

	if opened, lock := failing.Take(); opened != nil || lock != nil {
		t.Fatal("failed link kept the database or lock")
	}

	lock := stateValue(dir.Lock(testACI))(t)
	stateCheck(t, lock.Unlock())

	data = stateValue(dir.OpenAccount(ctx, testACI, zerolog.Nop()))(t)
	stateCheck(t, data.IdentityTestSQL(ctx, "DROP TRIGGER fail_link"))
	old := stateValue(data.Devices.DeviceByACI(ctx, aci))(t)

	if old.PNI != first.data.PNI || stateValue(old.ACIPreKeyStore.LoadPreKey(ctx, 41))(t) == nil {
		t.Fatal("failed relink changed the old account")
	}

	stateCheck(t, data.Close())

	// A successful relink replaces credentials, keys and allocator scopes.
	relink := linkInput(t, aci)
	links = dir.NewLinkStore(zerolog.Nop())
	device = stateValue(links.StoreProvisionedDevice(ctx, relink.data, relink.keys))(t)

	if device.PNI != relink.data.PNI || stateValue(device.ACIPreKeyStore.LoadPreKey(ctx, 41))(t) != nil {
		t.Fatal("relink kept the old PNI or keys")
	}

	assertProvisionedScopes(t, device)

	opened, held := links.Take()
	stateCheck(t, errors.Join(opened.Close(), held.Unlock()))
}

type provisioningInput struct {
	data *mstore.DeviceData
	keys mstore.ProvisioningPreKeys
}

func linkInput(t *testing.T, aci uuid.UUID) provisioningInput {
	t.Helper()

	data := newDevice(t, aci)

	return provisioningInput{data: data, keys: mstore.ProvisioningPreKeys{
		ACISigned:     signalmeow.GenerateSignedPreKey(1, data.ACIIdentityKeyPair),
		PNISigned:     signalmeow.GenerateSignedPreKey(1, data.PNIIdentityKeyPair),
		ACILastResort: signalmeow.GenerateKyberPreKeys(1, 1, data.ACIIdentityKeyPair)[0],
		PNILastResort: signalmeow.GenerateKyberPreKeys(1, 1, data.PNIIdentityKeyPair)[0],
	}}
}

func assertProvisionedScopes(t *testing.T, device *mstore.Device) {
	t.Helper()

	for _, pks := range []mstore.PreKeyStore{device.ACIPreKeyStore, device.PNIPreKeyStore} {
		got := stateValue(pks.PreKeyAllocatorState(t.Context()))(t)
		if got.NextEC != 1 || got.NextSigned != 2 || got.NextKyber != 2 ||
			got.ActiveSigned == nil || *got.ActiveSigned != 1 || got.ActiveLastResort == nil || *got.ActiveLastResort != 1 {
			t.Fatalf("%s provisioned state = %+v", pks.GetServiceID(), got)
		}
	}
}
