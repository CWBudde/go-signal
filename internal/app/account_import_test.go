package app_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

type accountImporterFunc func(context.Context, signal.AccountImportOptions) (signal.AccountImportResult, error)

func (importer accountImporterFunc) Import(ctx context.Context, opts signal.AccountImportOptions,
) (signal.AccountImportResult, error) {
	return importer(ctx, opts)
}

func accountImportResult() signal.AccountImportResult {
	return signal.AccountImportResult{
		Account: testAccount(),
		Counts: signal.AccountImportCounts{
			Identities: 1, Sessions: 2, PreKeys: 3, SignedPreKeys: 4, KyberPreKeys: 5,
			LastResortKyberPreKeys: 1, SenderKeys: 6, Recipients: 7, Groups: 8, ChatTimers: 9,
		},
		Excluded:     []string{"attachments", "storage-cache"},
		SourceFormat: signal.AccountImportFormat{Registry: 2, AccountJSON: 11, SQLite: 31},
	}
}

func TestImportAccount(t *testing.T) {
	t.Parallel()

	opts := signal.AccountImportOptions{
		SourceDir: "snapshot with spaces", SourceAccount: "+4915112345678", DataDir: "~/destination",
	}
	want := accountImportResult()

	tests := []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "invalid source", err: signal.ErrInvalidAccountImport},
		{name: "unsupported", err: signal.ErrUnsupportedAccountImport},
		{name: "ambiguous", err: signal.ErrAmbiguousAccountImport},
		{name: "source changed", err: signal.ErrAccountImportSourceChanged},
		{name: "no backend", err: signal.ErrCGORequired},
		{name: "cancelled", err: context.Canceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			calls := 0
			importer := accountImporterFunc(func(gotCtx context.Context, gotOpts signal.AccountImportOptions,
			) (signal.AccountImportResult, error) {
				calls++

				if gotCtx != ctx || gotOpts != opts {
					t.Errorf("import arguments = (%v, %+v), want original context and %+v", gotCtx, gotOpts, opts)
				}

				return want, test.err
			})

			got, err := app.ImportAccount(ctx, importer, opts)
			if !errors.Is(err, test.err) {
				t.Fatalf("ImportAccount error = %v, want %v", err, test.err)
			}

			if err != nil && !strings.HasPrefix(err.Error(), "account import: ") {
				t.Errorf("error %q lacks operation context", err)
			}

			if !reflect.DeepEqual(got, want) {
				t.Errorf("ImportAccount = %+v, want %+v", got, want)
			}

			if calls != 1 {
				t.Errorf("importer called %d times, want once", calls)
			}
		})
	}
}

func TestImportAccountEmptySource(t *testing.T) {
	t.Parallel()

	importer := accountImporterFunc(func(context.Context, signal.AccountImportOptions,
	) (signal.AccountImportResult, error) {
		t.Fatal("empty source must be rejected before calling the importer")

		return signal.AccountImportResult{}, nil
	})

	got, err := app.ImportAccount(t.Context(), importer, signal.AccountImportOptions{DataDir: "destination"})
	if !errors.Is(err, signal.ErrInvalidAccountImport) {
		t.Fatalf("ImportAccount error = %v, want ErrInvalidAccountImport", err)
	}

	if !reflect.DeepEqual(got, signal.AccountImportResult{}) {
		t.Errorf("ImportAccount = %+v, want zero result", got)
	}
}
