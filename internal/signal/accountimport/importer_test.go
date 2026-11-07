//go:build cgo || libsignal_go

package accountimport_test

import (
	"runtime"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestAccountImportBackendBoundary(t *testing.T) {
	t.Parallel()

	want := signal.ErrNotImplemented
	if runtime.GOOS != "linux" {
		want = signal.ErrUnsupportedAccountImport
	}

	assertAccountImportRefusal(t, want)
}
