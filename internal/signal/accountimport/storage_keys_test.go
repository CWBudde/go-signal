//go:build cgo || libsignal_go

package accountimport_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
)

const storageKeyVectorsDir = "testdata/java-storage-keys"

type storageKeyCase struct {
	Name               string `json:"name"`
	AccountEntropyPool string `json:"accountEntropyPool"`
	MasterKey          []byte `json:"masterKey"`
	StorageKey         []byte `json:"storageKey"`
}

// loadStorageKeyVectors reads the Java vectors after checking them against their provenance.
func loadStorageKeyVectors(t *testing.T) []storageKeyCase {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(storageKeyVectorsDir, "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}

	var provenance struct {
		//nolint:tagliatelle // The spelling is fixed by the Java generator's provenance JSON.
		FixtureSHA256 map[string]string `json:"fixtureSHA256"`
	}

	rawProvenance, err := os.ReadFile(filepath.Join(storageKeyVectorsDir, "provenance.json"))
	if err != nil {
		t.Fatal(err)
	}

	err = json.Unmarshal(rawProvenance, &provenance)
	if err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != provenance.FixtureSHA256["vectors.json"] {
		t.Fatalf("vectors.json SHA-256 = %s, provenance has %s", got, provenance.FixtureSHA256["vectors.json"])
	}

	var vectors struct {
		Version int              `json:"version"`
		Cases   []storageKeyCase `json:"cases"`
	}

	err = json.Unmarshal(raw, &vectors)
	if err != nil {
		t.Fatal(err)
	}

	if vectors.Version != 1 || len(vectors.Cases) != 3 {
		t.Fatalf("vectors version %d with %d cases, want 1 with 3", vectors.Version, len(vectors.Cases))
	}

	return vectors.Cases
}

// TestJavaStorageKeyDerivation resolves each signal-cli key source (SignalAccount's stored
// derived storageKey, pinMasterKey and accountEntropyPool) to the storage key Java derives.
func TestJavaStorageKeyDerivation(t *testing.T) {
	t.Parallel()

	for _, vector := range loadStorageKeyVectors(t) {
		var device mstore.DeviceData

		switch vector.Name {
		case "accountEntropyPool":
			master, err := libsignalgo.AccountEntropyPool(vector.AccountEntropyPool).DeriveSVRKey()
			if err != nil || !bytes.Equal(master, vector.MasterKey) {
				t.Fatalf("pool master key = %x, %v; want %x", master, err, vector.MasterKey)
			}

			device.AccountEntropyPool = libsignalgo.AccountEntropyPool(vector.AccountEntropyPool)
		case "pinMasterKey":
			device.MasterKey = vector.MasterKey
		case "derivedStorageKey":
			// Imported as the derived form; it must never be treated as a master key.
			device.DerivedStorageKey = vector.StorageKey
		default:
			t.Fatalf("unknown vector case %q", vector.Name)
		}

		key, err := device.ResolveStorageServiceKey()
		if err != nil || !bytes.Equal(key[:], vector.StorageKey) {
			t.Errorf("%s: storage key = %x, %v; want %x", vector.Name, key, err, vector.StorageKey)
		}
	}
}

// TestJavaStorageKeyImportRefusals checks that a stored derived key next to a master key or
// pool it doesn't belong to is refused rather than guessed.
func TestJavaStorageKeyImportRefusals(t *testing.T) {
	t.Parallel()

	cases := loadStorageKeyVectors(t)
	pool, pin, derived := cases[0], cases[1], cases[2]

	for name, device := range map[string]mstore.DeviceData{
		"derived and pin master": {DerivedStorageKey: derived.StorageKey, MasterKey: pin.MasterKey},
		"derived and pool": {
			DerivedStorageKey: derived.StorageKey, AccountEntropyPool: libsignalgo.AccountEntropyPool(pool.AccountEntropyPool),
		},
		"derived as master": {DerivedStorageKey: pin.MasterKey, MasterKey: pin.MasterKey},
	} {
		_, err := device.ResolveStorageServiceKey()
		if !errors.Is(err, mstore.ErrStorageServiceKeyConflict) {
			t.Errorf("%s: error = %v, want ErrStorageServiceKeyConflict", name, err)
		}
	}

	agreeing := mstore.DeviceData{DerivedStorageKey: pin.StorageKey, MasterKey: pin.MasterKey}

	key, err := agreeing.ResolveStorageServiceKey()
	if err != nil || !bytes.Equal(key[:], pin.StorageKey) {
		t.Errorf("agreeing derived and master key = %x, %v", key, err)
	}
}
