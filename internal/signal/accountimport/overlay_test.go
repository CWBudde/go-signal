//go:build cgo || libsignal_go

package accountimport_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal/accountimport"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"google.golang.org/protobuf/proto"
)

const accountOverlayDir = "testdata/java-account-overlay"

type overlayCase struct {
	Name           string                      `json:"name"`
	Config         accountimport.AccountConfig `json:"config"`
	SelfProfileKey []byte                      `json:"selfProfileKey"`
	AccountRecord  []byte                      `json:"accountRecord"`
}

type overlayVectors struct {
	Version      int           `json:"version"`
	CachedRecord []byte        `json:"cachedRecord"`
	Cases        []overlayCase `json:"cases"`
}

// loadOverlayVectors reads the Java vectors after checking them against their provenance.
func loadOverlayVectors(t *testing.T) overlayVectors {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(accountOverlayDir, "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}

	var provenance struct {
		//nolint:tagliatelle // The spelling is fixed by the Java generator's provenance JSON.
		FixtureSHA256 map[string]string `json:"fixtureSHA256"`
		Overlay       string            `json:"overlay"`
	}

	rawProvenance, err := os.ReadFile(filepath.Join(accountOverlayDir, "provenance.json"))
	if err != nil {
		t.Fatal(err)
	}

	err = json.Unmarshal(rawProvenance, &provenance)
	if err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != provenance.FixtureSHA256["vectors.json"] || provenance.Overlay == "" {
		t.Fatalf("vectors.json SHA-256 = %s, provenance has %s (overlay %q)", got,
			provenance.FixtureSHA256["vectors.json"], provenance.Overlay)
	}

	var vectors overlayVectors

	err = json.Unmarshal(raw, &vectors)
	if err != nil {
		t.Fatal(err)
	}

	if vectors.Version != 1 || len(vectors.Cases) != 6 {
		t.Fatalf("vectors version %d with %d cases, want 1 with 6", vectors.Version, len(vectors.Cases))
	}

	return vectors
}

// TestJavaAccountOverlay builds each AccountRecord the way signal-cli's overlay does and
// compares it with the Java record; CONTACTS sharing, which Java collapses into NOBODY, is
// refused.
func TestJavaAccountOverlay(t *testing.T) {
	t.Parallel()

	vectors := loadOverlayVectors(t)

	for _, vector := range vectors.Cases {
		java := &signalpb.AccountRecord{}

		err := proto.Unmarshal(vector.AccountRecord, java)
		if err != nil {
			t.Fatalf("%s: %v", vector.Name, err)
		}

		got, err := accountimport.AccountOverlay(vectors.CachedRecord, vector.Config, vector.SelfProfileKey)

		if vector.Config.PhoneNumberSharingMode == "CONTACTS" {
			if !errors.Is(err, accountimport.ErrUnsupportedSharingMode) ||
				java.GetPhoneNumberSharingMode() != signalpb.AccountRecord_NOBODY {
				t.Errorf("%s: error = %v, Java mode %v", vector.Name, err, java.GetPhoneNumberSharingMode())
			}

			continue
		}

		if err != nil || !proto.Equal(got, java) {
			t.Errorf("%s: overlay = %v, %v; Java has %v", vector.Name, got, err, java)
		}
	}
}

// TestAccountGuards protects the fields an import changes, or all of them without proof that
// the cached record is fresh.
func TestAccountGuards(t *testing.T) {
	t.Parallel()

	vectors := loadOverlayVectors(t)

	cached := &signalpb.AccountRecord{}

	err := proto.Unmarshal(vectors.CachedRecord, cached)
	if err != nil {
		t.Fatal(err)
	}

	cfg := vectors.Cases[1].Config // allFalse

	effective, err := accountimport.AccountOverlay(vectors.CachedRecord, cfg, vectors.Cases[1].SelfProfileKey)
	if err != nil {
		t.Fatal(err)
	}

	changed := guardFields(t, accountimport.AccountGuards(cached, effective, cfg, 7, true))
	want := []mstore.PrivacyField{
		mstore.PrivacyFieldReadReceipts, mstore.PrivacyFieldUnlistedPhoneNumber,
		mstore.PrivacyFieldPhoneNumberSharingMode, mstore.PrivacyFieldOwnProfileKey,
	}

	if !slices.Equal(changed, want) {
		t.Errorf("fresh guards = %v, want %v", changed, want)
	}

	all := guardFields(t, accountimport.AccountGuards(cached, effective, cfg, 7, false))
	if !slices.Equal(all, mstore.AccountPrivacyFields) {
		t.Errorf("unproven guards = %v", all)
	}
}

// guardFields returns the fields of guards, which must be protected account guards armed at
// version 7 with an explicit local value.
func guardFields(t *testing.T, guards []mstore.PrivacyGuard) []mstore.PrivacyField {
	t.Helper()

	out := make([]mstore.PrivacyField, 0, len(guards))

	for _, guard := range guards {
		if !guard.Protected || guard.ArmedVersion != 7 || guard.Kind != mstore.PrivacyEntityAccount || !guard.LocalPresent {
			t.Errorf("guard %+v", guard)
		}

		out = append(out, guard.Field)
	}

	return out
}
