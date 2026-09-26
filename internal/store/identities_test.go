//go:build cgo

package store_test

import (
	"io"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
)

//nolint:cyclop // one scenario, checked step by step
func TestIdentityRecords(t *testing.T) {
	t.Parallel()

	data := openAccount(t, openDir(t, io.Discard))
	ctx := t.Context()

	rec, err := data.Identity(ctx, "missing")
	if err != nil || rec != nil {
		t.Fatalf("missing record = %+v, %v", rec, err)
	}

	seen := time.Date(2026, 9, 20, 12, 30, 0, 0, time.UTC)
	alice := store.IdentityRecord{ServiceID: "a", Key: []byte{5, 1}, Trust: "trusted-unverified", FirstSeen: seen}
	bob := store.IdentityRecord{
		ServiceID: "b", Key: []byte{5, 3}, Trust: "untrusted", FirstSeen: seen, ChangedAt: seen.Add(time.Hour),
		PreviousKey: []byte{5, 2}, PendingEvent: true,
	}

	for _, r := range []store.IdentityRecord{bob, alice} {
		err = data.PutIdentity(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
	}

	all, err := data.Identities(ctx)
	if err != nil || len(all) != 2 || all[0].ServiceID != "a" || !all[0].FirstSeen.Equal(seen) ||
		!all[0].ChangedAt.IsZero() || all[0].PreviousKey != nil || all[1].Trust != "untrusted" {
		t.Fatalf("identities = %+v, %v", all, err)
	}

	pending, err := data.PendingIdentityChanges(ctx)
	if err != nil || len(pending) != 1 || pending[0].ServiceID != "b" || string(pending[0].PreviousKey) != "\x05\x02" {
		t.Fatalf("pending = %+v, %v", pending, err)
	}

	// A report for a key that changed again in the meantime doesn't clear the newer change.
	err = data.MarkIdentityReported(ctx, "b", []byte{5, 2})
	if err != nil {
		t.Fatal(err)
	}

	if pending, _ = data.PendingIdentityChanges(ctx); len(pending) != 1 {
		t.Errorf("stale report cleared the change")
	}

	err = data.MarkIdentityReported(ctx, "b", []byte{5, 3})
	if err != nil {
		t.Fatal(err)
	}

	if pending, _ = data.PendingIdentityChanges(ctx); len(pending) != 0 {
		t.Errorf("pending after report = %+v", pending)
	}

	keys, err := data.StoredIdentityKeys(ctx, testACI)
	if err != nil || len(keys) != 0 {
		t.Errorf("signalmeow keys of an empty store = %v, %v", keys, err)
	}
}
