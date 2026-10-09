//go:build cgo || libsignal_go

package store_test

import (
	"io"
	"testing"

	"github.com/cwbudde/go-signal/internal/store"
)

func TestStorageIdentityUpgradeProtectsPendingDecision(t *testing.T) {
	t.Parallel()
	dir := openDir(t, io.Discard)

	data := openAccount(t, dir)

	err := data.QueueIdentitySync(t.Context(), store.IdentitySync{
		ServiceID: "peer", Key: []byte{5, 1}, Trust: verificationVerified, Token: "pending",
	})
	if err != nil {
		t.Fatal(err)
	}

	err = data.IdentityTestSQL(t.Context(), `DROP TABLE gosignal_storage_identities`)
	if err != nil {
		t.Fatal(err)
	}

	err = data.IdentityTestSQL(t.Context(), `UPDATE gosignal_version SET version=11`)
	if err != nil {
		t.Fatal(err)
	}

	err = data.Close()
	if err != nil {
		t.Fatal(err)
	}

	upgraded := openAccount(t, dir)

	observation, err := upgraded.StorageIdentity(t.Context(), "peer")
	if err != nil || observation == nil || !observation.Dirty {
		t.Fatalf("migrated protection=%+v,%v", observation, err)
	}
}
