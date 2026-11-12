//go:build linux && (cgo || libsignal_go)

package accountimport_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/accountimport"
)

// The SQL projection must retain the original type/length when its bounded value
// is NULL. Otherwise nullable overflow could silently drop imported state.
func TestAccountImportSourceNullableBounds(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"text", "blob"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			tree := newSQLSnapshotTree(t)
			database := openSQLFixture(t, tree.database)

			var err error
			if kind == "text" {
				_, err = database.ExecContext(t.Context(), "INSERT INTO recipient(given_name) VALUES(?)",
					strings.Repeat("x", (4<<20)+1))
			} else {
				_, err = database.ExecContext(t.Context(), "INSERT INTO recipient(storage_record) VALUES(?)",
					bytes.Repeat([]byte{1}, (4<<20)+1))
			}

			check(t, err)
			check(t, database.Close())
			validateSQLFixture(t, tree, tree.testOptions(), signal.ErrInvalidAccountImport)
		})
	}
}

// account_id_type chooses the local store; address independently identifies the
// remote party. Source validation must preserve their full Cartesian product.
func TestAccountImportSourceSessionNamespaces(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		local  int64
		remote string
	}{
		{"ACI to ACI", 0, snapshotOtherACI},
		{"ACI to PNI", 0, "PNI:" + snapshotOtherACI},
		{"PNI to ACI", 1, snapshotOtherACI},
		{"PNI to PNI", 1, "PNI:" + snapshotOtherACI},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			tree := newSQLSnapshotTree(t)
			database := openSQLFixture(t, tree.database)
			_, err := database.ExecContext(t.Context(), "UPDATE session SET account_id_type=?,address=?",
				testCase.local, testCase.remote)
			check(t, err)
			check(t, database.Close())

			snap := must(accountimport.OpenSnapshotForTest(t.Context(), tree.options(), tree.testOptions()))(t)
			defer func() { check(t, snap.Close()) }()

			rows := must(snap.SourceRowsForTest(t.Context(), "session"))(t)
			if len(rows) != 1 {
				t.Fatalf("session rows=%d", len(rows))
			}

			if rows[0].Values["account_id_type"] != testCase.local ||
				rows[0].ServiceIDs["address"] != testCase.remote {
				t.Fatal("local scope or typed remote address changed")
			}
		})
	}
}

func TestAccountImportSourceCacheRefresh(t *testing.T) {
	t.Parallel()

	for _, when := range []string{"before SQL open", "during configuration"} {
		t.Run(when, func(t *testing.T) { t.Parallel(); checkSourceCacheRefresh(t, when) })
	}
}

func checkSourceCacheRefresh(t *testing.T, when string) {
	t.Helper()
	tree := newSQLSnapshotTree(t)
	cache := filepath.Join(filepath.Dir(tree.database), "msg-cache", "peer")
	check(t, os.MkdirAll(cache, 0o700))
	message := filepath.Join(cache, "message")
	before := snapshotFiles(t, tree.source)
	addMessage := func() {
		check(t, os.WriteFile(message, []byte("synthetic pending message"), 0o600))
		before = snapshotFiles(t, tree.source)
	}
	opts := tree.testOptions()
	added := false

	if when == "during configuration" {
		opts.Step = func(step string) {
			if step == "sql:row:key_value" && !added {
				addMessage()

				added = true
			}
		}
	}

	snap := must(accountimport.OpenSnapshotForTest(t.Context(), tree.options(), opts))(t)

	if when == "before SQL open" {
		addMessage()
	}

	err := snap.ValidateSourceForTest(t.Context())
	if !errors.Is(err, signal.ErrAccountImportSourceChanged) {
		t.Errorf("cache changed: %v", err)
	}

	check(t, snap.Close())

	if !reflect.DeepEqual(before, snapshotFiles(t, tree.source)) {
		t.Error("validation changed source")
	}

	equal(t, must(os.ReadFile(message))(t), []byte("synthetic pending message"))
	assertNoSnapshotWorkspace(t, tree)
}
