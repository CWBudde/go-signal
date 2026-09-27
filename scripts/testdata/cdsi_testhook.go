//go:build purego

// Package shimtest is copied into attest/ only by test-cdsi-integration.sh.
// It is absent from both published forks and from every production build.
package shimtest

import (
	"testing"

	"github.com/cwbudde/libsignal-go/attest/internal/testhook"
)

// EnableExpiredFixture temporarily enables upstream's evaluation-number-12
// exception. Other attestation checks remain active. Call only in serial tests.
func EnableExpiredFixture(t *testing.T) {
	t.Helper()

	was := testhook.SetAcceptVeryExpiredEvalNumber(true)
	t.Cleanup(func() { testhook.SetAcceptVeryExpiredEvalNumber(was) })
	if was {
		t.Fatal("expired-fixture exception was already enabled")
	}
}
