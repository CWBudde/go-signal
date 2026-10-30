//go:build cgo || libsignal_go

package accountimport

import (
	"context"
	"runtime"

	"github.com/cwbudde/go-signal/internal/signal"
)

// Importer is the offline account converter. Its zero value requires no setup or I/O.
// Conversion and publication are not yet implemented.
type Importer struct{}

// Import currently refuses without accessing the source or destination. Linux
// conversion will be integrated after snapshot validation and transactional conversion.
// Other platforms lack the required publication primitives.
func (Importer) Import(context.Context, signal.AccountImportOptions) (signal.AccountImportResult, error) {
	if runtime.GOOS != "linux" {
		return signal.AccountImportResult{}, signal.ErrUnsupportedAccountImport
	}

	return signal.AccountImportResult{}, signal.ErrNotImplemented
}
