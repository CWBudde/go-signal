//go:build !cgo && !libsignal_go

package accountimport

import (
	"context"

	"github.com/cwbudde/go-signal/internal/signal"
)

// Importer is unavailable in builds without a Signal protocol backend.
// Its zero value requires no setup or I/O.
type Importer struct{}

// Import returns the backend-required error without accessing either path.
func (Importer) Import(context.Context, signal.AccountImportOptions) (signal.AccountImportResult, error) {
	return signal.AccountImportResult{}, signal.ErrCGORequired
}
