package cmd_test

import (
	"os"
	"path/filepath"
	"testing"
)

// golden compares got with testdata/<name>.golden. Run the tests with UPDATE_GOLDEN=1 to
// rewrite the files.
func golden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name+".golden")

	if os.Getenv("UPDATE_GOLDEN") != "" {
		err := os.MkdirAll("testdata", 0o750)
		if err == nil {
			err = os.WriteFile(path, []byte(got), 0o600)
		}

		if err != nil {
			t.Fatal(err)
		}

		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with UPDATE_GOLDEN=1 to create it)", err)
	}

	if got != string(want) {
		t.Errorf("output differs from %s:\ngot:\n%s\nwant:\n%s", path, got, want)
	}
}
