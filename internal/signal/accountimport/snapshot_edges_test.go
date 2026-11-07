//go:build linux && (cgo || libsignal_go)

package accountimport_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/accountimport"
)

func TestAccountImportSnapshotStrictMembers(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, old, replacement string
		want                   error
	}{
		{"case variant", `"version" : 11`, `"Version" : 11`, signal.ErrUnsupportedAccountImport},
		{"case alias", `"version" : 11`, `"version":11,"Version":12`, signal.ErrUnsupportedAccountImport},
		{"active null", `"activeSignedPreKeyId" : 2`, `"activeSignedPreKeyId":null`, signal.ErrInvalidAccountImport},
		{
			"last resort null", `"activeLastResortKyberPreKeyId" : 4`, `"activeLastResortKyberPreKeyId":null`,
			signal.ErrInvalidAccountImport,
		},
		{"fractional integer", `"registrationId" : 123`, `"registrationId":123.5`, signal.ErrInvalidAccountImport},
		{
			"empty password", `"password" : "synthetic-offline-password-never-authenticate"`, `"password":""`,
			signal.ErrInvalidAccountImport,
		},
		{"zero ACI", snapshotACI, `00000000-0000-0000-0000-000000000000`, signal.ErrInvalidAccountImport},
		{
			"wrong namespace", `PNI:33333333-3333-4333-8333-333333333333`, `33333333-3333-4333-8333-333333333333`,
			signal.ErrInvalidAccountImport,
		},
		{
			"noncanonical key", `"profileKey" : "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="`,
			`"profileKey":"AAAAAAAAAAAAAAAAAAAA\nAAAAAAAAAAAAAAAAAAAAAAA="`, signal.ErrInvalidAccountImport,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			tree := newSnapshotTree(t)
			raw := must(os.ReadFile(tree.account))(t)

			changed := bytes.Replace(raw, []byte(test.old), []byte(test.replacement), 1)
			if bytes.Equal(raw, changed) {
				t.Fatal("test failed to alter fixture")
			}

			check(t, os.WriteFile(tree.account, changed, 0o600))
			assertSnapshotError(t, tree, test.want)
		})
	}
}

//nolint:cyclop // Exercise the same mutation matrix at both streaming boundaries.
func TestAccountImportSourceChangedDuringStream(t *testing.T) {
	t.Parallel()

	for _, stage := range []string{snapshotCopyDatabase, snapshotHashDatabase} {
		for _, kind := range []string{"replace file", "replace parent", "grow file", "optional appears"} {
			t.Run(stage+"/"+kind, func(t *testing.T) {
				t.Parallel()

				tree := newSnapshotTree(t)
				config := tree.testOptions()
				fired := false
				config.Step = func(step string) {
					if step != stage || fired {
						return
					}

					fired = true

					switch kind {
					case "replace file":
						raw := must(os.ReadFile(tree.database))(t)
						check(t, os.Rename(tree.database, tree.database+"-old"))
						check(t, os.WriteFile(tree.database, raw, 0o600))
					case "replace parent":
						path := filepath.Dir(tree.database)
						check(t, os.Rename(path, path+"-old"))
						check(t, os.Mkdir(path, 0o700))
					case "grow file":
						check(t, os.WriteFile(tree.database, bytes.Repeat([]byte("D"), 128<<10), 0o600))
					case "optional appears":
						check(t, os.WriteFile(tree.database+"-wal", []byte("WAL"), 0o600))
					}
				}

				snapshot, err := accountimport.OpenSnapshotForTest(t.Context(), tree.options(), config)
				if snapshot != nil {
					check(t, snapshot.Close())
				}

				if !fired || !errors.Is(err, signal.ErrAccountImportSourceChanged) {
					t.Fatalf("stream mutation: fired=%v, err=%v", fired, err)
				}

				assertNoSnapshotWorkspace(t, tree)
			})
		}
	}
}

func TestAccountImportPathsDestinationResolution(t *testing.T) {
	// Environment-dependent resolution cannot run in parallel with Setenv.
	for _, kind := range []string{"default", "tilde", "separate symlink", "new destination directories"} {
		t.Run(kind, func(t *testing.T) {
			tree := newSnapshotTree(t)
			opts := tree.options()
			want := tree.destination

			switch kind {
			case "default":
				t.Setenv("XDG_DATA_HOME", tree.destination)

				opts.DataDir = ""
				want = filepath.Join(tree.destination, "go-signal")
			case "tilde":
				t.Setenv("HOME", filepath.Dir(tree.destination))

				opts.DataDir = "~/destination"
			case "separate symlink":
				check(t, os.Mkdir(tree.destination, 0o700))
				check(t, os.Symlink(tree.destination, tree.destination+"-alias"))
				opts.DataDir = tree.destination + "-alias"
			}

			snapshot := must(accountimport.OpenSnapshotForTest(t.Context(), opts, tree.testOptions()))(t)
			defer func() { check(t, snapshot.Close()) }()

			if snapshot.Destination != want {
				t.Fatalf("resolved destination = %q, want %q", snapshot.Destination, want)
			}

			if kind == "new destination directories" {
				// Task 6 creates the destination after acquisition. That ordinary
				// creation must remain compatible with final revalidation.
				check(t, os.Mkdir(tree.destination, 0o700))
			}

			check(t, snapshot.Revalidate(t.Context()))
		})
	}
}

func TestAccountImportSnapshotResourceCleanup(t *testing.T) {
	t.Parallel()

	tree := newSnapshotTree(t)
	snapshot := must(accountimport.OpenSnapshotForTest(t.Context(), tree.options(), tree.testOptions()))(t)
	check(t, snapshot.Close())
	assertNoSnapshotWorkspace(t, tree)

	for _, entry := range must(os.ReadDir("/proc/self/fd"))(t) {
		path, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err != nil {
			continue // Another parallel test may have closed its own descriptor.
		}

		if strings.HasPrefix(path, tree.source) || strings.HasPrefix(path, tree.workspace) {
			t.Fatalf("snapshot descriptor leaked: %s", path)
		}
	}
}

func TestAccountImportSnapshotCanceledBeforeAcquisition(t *testing.T) {
	t.Parallel()

	tree := newSnapshotTree(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := accountimport.OpenSnapshotForTest(ctx, tree.options(), tree.testOptions())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquisition: %v", err)
	}

	assertNoSnapshotWorkspace(t, tree)
}

func TestAccountImportSnapshotRegistryEligibility(t *testing.T) {
	t.Parallel()

	tree := newSnapshotTree(t)
	editSnapshotJSON(t, filepath.Join(tree.data, "accounts.json"), func(value map[string]any) {
		entries := snapshotJSONArray(t, value["accounts"])
		value["accounts"] = append(entries, map[string]any{
			snapshotPathField: "unregistered", snapshotEnvironmentField: snapshotLive,
			snapshotNumberField: nil, snapshotUUIDField: nil,
		})
	})
	snapshot := must(accountimport.OpenSnapshotForTest(t.Context(), tree.options(), tree.testOptions()))(t)
	check(t, snapshot.Close())
}

func TestAccountImportPathsDevice(t *testing.T) {
	t.Parallel()

	err := accountimport.CheckRequiredFileForTest(t.Context(), "/dev", "null")
	if !errors.Is(err, signal.ErrInvalidAccountImport) {
		t.Fatalf("character device error = %v", err)
	}
}

func TestAccountImportPathsBoundedListings(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{snapshotCache, "destination"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			tree := newSnapshotTree(t)

			path := tree.destination
			if kind == snapshotCache {
				path = filepath.Join(filepath.Dir(tree.database), "msg-cache")
			}

			check(t, os.Mkdir(path, 0o700))
			check(t, os.Mkdir(filepath.Join(path, "one"), 0o700))
			check(t, os.Mkdir(filepath.Join(path, "two"), 0o700))

			config := tree.testOptions()
			config.EntryLimit = 1

			snapshot, err := accountimport.OpenSnapshotForTest(t.Context(), tree.options(), config)
			if snapshot != nil {
				check(t, snapshot.Close())
			}

			if !errors.Is(err, signal.ErrInvalidAccountImport) {
				t.Fatalf("listing limit: %v", err)
			}

			assertNoSnapshotWorkspace(t, tree)
		})
	}
}

func TestAccountImportSourceChangedWorkspaceParent(t *testing.T) {
	t.Parallel()

	tree := newSnapshotTree(t)
	before := snapshotFiles(t, tree.source)
	config := tree.testOptions()
	fired := false
	config.Step = func(step string) {
		if step != snapshotCopyDatabase || fired {
			return
		}

		fired = true

		check(t, os.Rename(tree.workspace, tree.workspace+"-old"))
		check(t, os.Symlink(tree.source, tree.workspace))
	}

	snapshot, err := accountimport.OpenSnapshotForTest(t.Context(), tree.options(), config)
	if snapshot != nil {
		check(t, snapshot.Close())
	}

	if !fired || !errors.Is(err, signal.ErrAccountImportSourceChanged) {
		t.Fatalf("workspace rebinding: fired=%v, err=%v", fired, err)
	}

	if got := snapshotFiles(t, tree.source); !reflect.DeepEqual(got, before) {
		t.Fatal("workspace rebinding caused a source write")
	}

	check(t, os.Remove(tree.workspace))
	check(t, os.Rename(tree.workspace+"-old", tree.workspace))
	assertNoSnapshotWorkspace(t, tree)
}

func TestAccountImportSnapshotCaseSensitivePaths(t *testing.T) {
	t.Parallel()

	tree := newSnapshotTree(t)
	editSnapshotJSON(t, filepath.Join(tree.data, "accounts.json"), func(value map[string]any) {
		entries := snapshotJSONArray(t, value["accounts"])
		value["accounts"] = append(entries, map[string]any{
			snapshotPathField: strings.ToUpper(snapshotBasename), snapshotEnvironmentField: snapshotLive,
			snapshotNumberField: "+12025550112", snapshotUUIDField: snapshotOtherACI,
		})
	})
	opts := tree.options()
	opts.SourceAccount = "+12025550111"
	snapshot := must(accountimport.OpenSnapshotForTest(t.Context(), opts, tree.testOptions()))(t)
	check(t, snapshot.Close())
}

func TestAccountImportSourceChangedSidecars(t *testing.T) {
	t.Parallel()

	for _, suffix := range []string{"-wal", snapshotSHMSuffix} {
		for _, operation := range []string{snapshotSidecarAdd, "remove", "replace"} {
			t.Run(suffix+"/"+operation, func(t *testing.T) {
				t.Parallel()

				tree := newSnapshotTree(t)

				path := tree.database + suffix
				if operation != snapshotSidecarAdd {
					check(t, os.WriteFile(path, []byte("sidecar"), 0o600))
				}

				snapshot := must(accountimport.OpenSnapshotForTest(t.Context(), tree.options(), tree.testOptions()))(t)
				defer func() { check(t, snapshot.Close()) }()

				switch operation {
				case snapshotSidecarAdd:
					check(t, os.WriteFile(path, []byte("sidecar"), 0o600))
				case "remove":
					check(t, os.Remove(path))
				case "replace":
					check(t, os.Rename(path, path+"-old"))
					check(t, os.WriteFile(path, []byte("sidecar"), 0o600))
				}

				err := snapshot.Revalidate(t.Context())
				if !errors.Is(err, signal.ErrAccountImportSourceChanged) {
					t.Fatalf("sidecar change: %v", err)
				}
			})
		}
	}
}
