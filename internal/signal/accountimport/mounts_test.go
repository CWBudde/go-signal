//go:build linux && (cgo || libsignal_go)

package accountimport_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/accountimport"
)

// Mount records are injected per instance; no privileged mounts are created.
// The real snapshot reader must refuse before writing either fixture directory.
//
//nolint:gocognit // Keep both alias targets and their source-topology assertions in one matrix.
func TestAccountImportPathsMountAliases(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"publication path", "temporary workspace"} {
		for _, child := range []string{snapshotDataDir, "avatars", "nested mount"} {
			t.Run(target+"/"+child, func(t *testing.T) {
				t.Parallel()

				tree := newSnapshotTree(t)
				alias := filepath.Join(filepath.Dir(tree.destination), "alias")
				check(t, os.Mkdir(alias, 0o700))

				opts, config := tree.options(), tree.testOptions()
				sourceChild := filepath.Join(tree.source, child)
				mounts := snapshotMountLine(1, "0:1", "/", "/")

				device := "0:1"
				if child == "nested mount" {
					device = "0:2"
					mounts += snapshotMountLine(2, device, "/separate", sourceChild)
					sourceChild = "/separate"
				}

				mounts += snapshotMountLine(3, device, sourceChild, alias)
				config.MountInfo = func() ([]byte, error) { return []byte(mounts), nil }

				if target == "publication path" {
					opts.DataDir = filepath.Join(alias, "missing", "publication path")
				} else {
					config.TempDir = alias
				}

				before := snapshotFiles(t, tree.source)
				aliasBefore := snapshotFiles(t, alias)

				snapshot, err := accountimport.OpenSnapshotForTest(t.Context(), opts, config)
				if snapshot != nil {
					check(t, snapshot.Close())
				}

				if !errors.Is(err, signal.ErrInvalidAccountImport) {
					t.Fatalf("physical alias error = %v", err)
				}

				if !reflect.DeepEqual(snapshotFiles(t, tree.source), before) {
					t.Fatal("alias refusal changed source")
				}

				if !reflect.DeepEqual(snapshotFiles(t, alias), aliasBefore) {
					t.Fatal("alias refusal changed directory metadata")
				}

				if entries := must(os.ReadDir(alias))(t); len(entries) != 0 {
					t.Fatal("alias refusal created entries")
				}
			})
		}
	}
}

func snapshotMountLine(id int, device, root, point string) string {
	escape := strings.NewReplacer("\\", `\134`, " ", `\040`, "\t", `\011`, "\n", `\012`)
	return fmt.Sprintf("%d 0 %s %s %s rw - ext4 fixture rw\n", id, device, escape.Replace(root), escape.Replace(point))
}

func TestAccountImportSnapshotMountTopology(t *testing.T) {
	t.Parallel()

	t.Run("escaped paths and separate topology", func(t *testing.T) {
		t.Parallel()

		tree := newSnapshotTree(t)
		config := tree.testOptions()
		mounts := snapshotMountLine(1, "0:1", "/", "/") +
			snapshotMountLine(2, "0:2", "/unrelated root", filepath.Join(tree.source, "avatars"))
		mounts += strings.Replace(snapshotMountLine(4, "0:4", "net:[1234]", "/unrelated-nsfs"),
			" - ext4 ", " - nsfs ", 1)
		config.MountInfo = func() ([]byte, error) { return []byte(mounts), nil }

		snapshot := must(accountimport.OpenSnapshotForTest(t.Context(), tree.options(), config))(t)
		defer func() { check(t, snapshot.Close()) }()

		check(t, snapshot.Revalidate(t.Context()))

		mounts += snapshotMountLine(3, "0:3", "/another", filepath.Join(tree.source, "new-mount"))

		revalidateErr := snapshot.Revalidate(t.Context())
		if !errors.Is(revalidateErr, signal.ErrAccountImportSourceChanged) {
			t.Fatalf("source mount topology changed: %v", revalidateErr)
		}
	})

	for _, raw := range []string{
		"", "malformed",
		"1 0 0:1 / relative rw - ext4 fixture rw\n",
		"1 0 0:1 / /bad\\000 rw - ext4 fixture rw\n",
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			tree := newSnapshotTree(t)
			config := tree.testOptions()
			config.MountInfo = func() ([]byte, error) { return []byte(raw), nil }

			snapshot, err := accountimport.OpenSnapshotForTest(t.Context(), tree.options(), config)
			if snapshot != nil {
				check(t, snapshot.Close())
			}

			if !errors.Is(err, signal.ErrUnsupportedAccountImport) {
				t.Fatalf("unprovable mount topology: %v", err)
			}

			assertNoSnapshotWorkspace(t, tree)
		})
	}
}
