package app

import (
	"context"
	"fmt"

	"github.com/cwbudde/go-signal/internal/signal"
)

// AccountImporter converts and publishes a stopped source snapshot without a live client.
// Implementations validate source state and keep errors free of source secrets.
type AccountImporter interface {
	Import(ctx context.Context, opts signal.AccountImportOptions) (signal.AccountImportResult, error)
}

// ImportAccount validates the source argument and delegates to the offline importer.
// It does not construct an App or open a signal.Client. Results survive errors so
// callers can distinguish a published account from a failed prepublication attempt.
func ImportAccount(ctx context.Context, importer AccountImporter, opts signal.AccountImportOptions,
) (signal.AccountImportResult, error) {
	if opts.SourceDir == "" {
		return signal.AccountImportResult{}, fmt.Errorf("account import: source directory is required: %w",
			signal.ErrInvalidAccountImport)
	}

	result, err := importer.Import(ctx, opts)
	if err != nil {
		return result, fmt.Errorf("account import: %w", err)
	}

	return result, nil
}
