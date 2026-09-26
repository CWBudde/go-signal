//go:build cgo

package signal_test

import (
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestIdentityReportRetriedWhenClosing(t *testing.T) {
	t.Parallel()

	env := newTrustEnv(t)
	oldKey, newKey := newIdentityKey(t), newIdentityKey(t)

	env.saveAlice(t, oldKey)
	env.now = env.now.Add(time.Hour)
	env.saveAlice(t, newKey)

	// The wrapper passes lookups through to signalmeow's store.
	stored, err := env.device.ACIIdentityStore.GetIdentityKey(t.Context(), serviceID(aliceACI))
	if err != nil || stored == nil || fingerprintOf(t, stored) != fingerprintOf(t, newKey) {
		t.Fatalf("stored key = %v, %v; want the new one", stored, err)
	}

	// The client closed before it took the event: the change stays pending.
	offered := 0

	if env.trust.Report(t.Context(), func(signal.Event) bool {
		offered++

		return false
	}) {
		t.Error("report succeeded although the event wasn't taken")
	}

	if offered != 1 {
		t.Errorf("offered %d events, want 1", offered)
	}

	events := env.report(t)
	if len(events) != 1 || events[0].NewFingerprint != fingerprintOf(t, newKey) {
		t.Errorf("events on the next run = %+v, want the change again", events)
	}
}
