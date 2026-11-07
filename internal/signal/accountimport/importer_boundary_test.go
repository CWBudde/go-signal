package accountimport_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/accountimport"
)

var _ app.AccountImporter = accountimport.Importer{}

// A missing backend/platform (or unfinished implementation) must refuse before
// opening paths: even paths beneath a regular file must return the boundary error.
func assertAccountImportRefusal(t *testing.T, wantErr error) {
	t.Helper()

	tests := []struct {
		name        string
		source      string
		destination string
	}{
		{"missing paths", "missing/source", "missing/destination"},
		{"inaccessible source", "source/child", "destination"},
		{"inaccessible destination", ".", "source/child"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			marker := filepath.Join(root, "source")

			const original = "source must remain unchanged"

			err := os.WriteFile(marker, []byte(original), 0o600)
			if err != nil {
				t.Fatal(err)
			}

			opts := signal.AccountImportOptions{
				SourceDir: filepath.Join(root, test.source),
				DataDir:   filepath.Join(root, test.destination),
			}

			got, err := (accountimport.Importer{}).Import(t.Context(), opts)
			if !errors.Is(err, wantErr) {
				t.Fatalf("Import error = %v, want %v", err, wantErr)
			}

			if !reflect.DeepEqual(got, signal.AccountImportResult{}) {
				t.Errorf("Import = %+v, want zero result", got)
			}

			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}

			if len(entries) != 1 || entries[0].Name() != "source" {
				t.Fatalf("Import changed the source/destination directory: %v", entries)
			}

			content, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}

			if string(content) != original {
				t.Errorf("Import changed source content to %q", content)
			}
		})
	}
}
