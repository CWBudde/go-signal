//go:build cgo || libsignal_go

package store_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/google/uuid"
)

//nolint:cyclop // upgrade an existing account, preserve identity and exercise the new queue
func TestIdentitySyncUpgrade(t *testing.T) {
	t.Parallel()
	dir := openDir(t, io.Discard)
	data := openAccount(t, dir)

	err := data.Devices.PutDevice(t.Context(), newDevice(t, uuid.MustParse(testACI)))
	if err != nil {
		t.Fatal(err)
	}

	peer := uuid.NewString()
	key := []byte{5, 1}
	protocolIdentity(t, data, testACI, peer, key)

	err = data.PutIdentity(t.Context(), store.IdentityRecord{ServiceID: peer, Key: key, Trust: verificationVerified})
	if err != nil {
		t.Fatal(err)
	}

	err = data.IdentityTestSQL(t.Context(), `DROP TABLE gosignal_storage_identities`)
	if err != nil {
		t.Fatal(err)
	}

	err = data.IdentityTestSQL(t.Context(), `DROP TABLE gosignal_identity_sync`)
	if err != nil {
		t.Fatal(err)
	}

	err = data.IdentityTestSQL(t.Context(), `UPDATE gosignal_version SET version=10`)
	if err != nil {
		t.Fatal(err)
	}

	err = data.Close()
	if err != nil {
		t.Fatal(err)
	}

	upgraded := openAccount(t, dir)

	rec, err := upgraded.Identity(t.Context(), peer)
	if err != nil || rec == nil || rec.Trust != verificationVerified || !bytes.Equal(rec.Key, key) {
		t.Fatalf("migration damaged identity: %+v,%v", rec, err)
	}

	err = upgraded.QueueIdentitySync(t.Context(), store.IdentitySync{
		ServiceID: peer, Key: key, Trust: verificationVerified, Token: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}

	pending, err := upgraded.PendingIdentitySync(t.Context(), testACI)
	if err != nil || len(pending) != 1 || pending[0].ServiceID != peer {
		t.Fatalf("migrated queue=%+v,%v", pending, err)
	}
}

func TestIdentitySyncAccountScope(t *testing.T) {
	t.Parallel()
	data := verificationStore(t)
	other := newDevice(t, uuid.New())

	err := data.Devices.PutDevice(t.Context(), other)
	if err != nil {
		t.Fatal(err)
	}

	peer, key := uuid.NewString(), []byte{5, 1}
	protocolIdentity(t, data, other.ACI.String(), peer, key)

	err = data.PutIdentity(t.Context(), store.IdentityRecord{ServiceID: peer, Key: key, Trust: verificationVerified})
	if err != nil {
		t.Fatal(err)
	}

	err = data.QueueIdentitySync(t.Context(), store.IdentitySync{
		ServiceID: peer, Key: key, Trust: verificationVerified, Token: uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}

	pending, err := data.PendingIdentitySync(t.Context(), testACI)
	if err != nil || len(pending) != 0 {
		t.Fatalf("foreign account authorized outgoing decision: %+v,%v", pending, err)
	}
}
