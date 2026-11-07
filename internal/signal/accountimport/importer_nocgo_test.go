//go:build !cgo && !libsignal_go

package accountimport_test

import (
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestAccountImportNoBackend(t *testing.T) {
	t.Parallel()
	assertAccountImportRefusal(t, signal.ErrCGORequired)
}
