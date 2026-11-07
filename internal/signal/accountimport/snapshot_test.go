//go:build linux && (cgo || libsignal_go)

package accountimport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/accountimport"
	"golang.org/x/sys/unix"
)

const (
	snapshotBasename         = "synthetic-linked"
	snapshotACI              = "11111111-1111-4111-8111-111111111111"
	snapshotDuplicateACI     = "duplicate ACI"
	snapshotDuplicateNumber  = "duplicate number"
	snapshotSQLExceeds       = "SQL exceeds total"
	snapshotWAL              = "WAL"
	snapshotHardlinkRegistry = "hard-linked registry"
	snapshotWALDisappears    = "WAL disappears"
	snapshotSQLExact         = "SQL exact total"
	snapshotSHM              = "SHM"
	snapshotHardlinkAccount  = "hard-linked account"
	snapshotSHMReplacement   = "SHM replacement"
)

const (
	snapshotNumberField   = "number"
	snapshotCache         = "cache"
	snapshotAccountFile   = "account"
	snapshotDuplicatePath = "duplicate path"
	snapshotWALExceeds    = "WAL exceeds total"
	snapshotCacheMessage  = "cache gains message"
	snapshotCopyDatabase  = "copy:database"
	snapshotHashDatabase  = "hash:database"
)

const (
	snapshotOtherACI             = "22222222-2222-4222-8222-222222222222"
	snapshotLive                 = "LIVE"
	snapshotSHMSuffix            = "-shm"
	snapshotSidecarAdd           = "add"
	snapshotExplicit             = "explicit"
	snapshotNoFallback           = "no fallback"
	snapshotStagingIgnored       = "staging ignored"
	snapshotUUIDField            = "uuid"
	snapshotAccountExactBound    = "account exact JSON bound"
	snapshotRegistryExceedsBound = "registry exceeds JSON bound"
	snapshotDangling             = "dangling"
	snapshotRootReplacement      = "root replacement"
)

const snapshotEnvironmentField = "environment"

const snapshotPathField = "path"

const snapshotDataDir = "data"

type snapshotTree struct {
	source, destination, workspace, data, account, database string
}

func newSnapshotTree(t *testing.T) snapshotTree {
	t.Helper()

	parent := t.TempDir()
	tree := snapshotTree{
		source: filepath.Join(parent, "source-parent", "source"), destination: filepath.Join(parent, "destination"),
		workspace: filepath.Join(parent, "workspace"),
	}
	tree.data = filepath.Join(tree.source, snapshotDataDir)
	tree.account = filepath.Join(tree.data, snapshotBasename)
	tree.database = filepath.Join(tree.data, snapshotBasename+".d", "account.db")
	check(t, os.MkdirAll(filepath.Dir(tree.database), 0o700))
	check(t, os.Mkdir(tree.workspace, 0o700))

	for _, name := range []string{"accounts.json", "account.json"} {
		raw := must(os.ReadFile(filepath.Join("testdata", "java-0.103.0", name)))(t)

		target := filepath.Join(tree.data, "accounts.json")
		if name == "account.json" {
			target = tree.account
		}

		check(t, os.WriteFile(target, raw, 0o600))
	}

	// Task 2 copies bytes without opening SQL. Schema validation belongs to Task 3.
	check(t, os.WriteFile(tree.database, []byte("stopped database bytes"), 0o600))

	return tree
}

func (tree snapshotTree) options() signal.AccountImportOptions {
	return signal.AccountImportOptions{SourceDir: tree.source, DataDir: tree.destination}
}

func (tree snapshotTree) testOptions() accountimport.SnapshotTestOptions {
	return accountimport.SnapshotTestOptions{TempDir: tree.workspace}
}

func editSnapshotJSON(t *testing.T, path string, edit func(map[string]any)) {
	t.Helper()

	var value map[string]any
	check(t, json.Unmarshal(must(os.ReadFile(path))(t), &value))
	edit(value)
	check(t, os.WriteFile(path, must(json.Marshal(value))(t), 0o600))
}

func assertNoSnapshotWorkspace(t *testing.T, tree snapshotTree) {
	t.Helper()

	if entries := must(os.ReadDir(tree.workspace))(t); len(entries) != 0 {
		t.Fatalf("private workspace leaked: %v", entries)
	}
}

func assertSnapshotError(t *testing.T, tree snapshotTree, want error) {
	t.Helper()

	snapshot, err := accountimport.OpenSnapshotForTest(t.Context(), tree.options(), tree.testOptions())
	if snapshot != nil {
		check(t, snapshot.Close())
		t.Fatal("refused input returned a snapshot")
	}

	if !errors.Is(err, want) {
		t.Fatalf("snapshot error = %v, want %v", err, want)
	}

	_, statErr := os.Lstat(tree.destination)
	if !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("refusal created destination: %v", statErr)
	}

	assertNoSnapshotWorkspace(t, tree)
}

//nolint:cyclop,funlen // Keep the complementary cases and their resource assertions in one test matrix.
func TestAccountImportSnapshot(t *testing.T) {
	t.Parallel()

	t.Run("settings root and private copies", func(t *testing.T) {
		t.Parallel()

		tree := newSnapshotTree(t)
		check(t, os.WriteFile(tree.database+"-wal", []byte("committed WAL"), 0o600))
		check(t, os.WriteFile(tree.database+snapshotSHMSuffix, []byte("shared memory"), 0o600))
		before := snapshotFiles(t, tree.source)

		snapshot := must(accountimport.OpenSnapshotForTest(t.Context(), tree.options(), tree.testOptions()))(t)
		defer func() { check(t, snapshot.Close()) }()

		if snapshot.Number != "+12025550111" || snapshot.ACI != snapshotACI ||
			snapshot.PNI != "33333333-3333-4333-8333-333333333333" {
			t.Fatal("snapshot selected wrong account")
		}

		for _, suffix := range []string{"", "-wal", snapshotSHMSuffix} {
			got := must(os.ReadFile(snapshot.Database + suffix))(t)

			want := must(os.ReadFile(tree.database + suffix))(t)
			if !bytes.Equal(got, want) {
				t.Fatalf("private %s bytes differ", suffix)
			}

			if mode := must(os.Stat(snapshot.Database + suffix))(t).Mode().Perm(); mode != 0o600 {
				t.Fatalf("private file permissions = %o", mode)
			}
		}

		if mode := must(os.Stat(snapshot.Workspace))(t).Mode().Perm(); mode != 0o700 {
			t.Fatalf("private directory permissions = %o", mode)
		}

		check(t, snapshot.Revalidate(t.Context()))
		check(t, snapshot.Close())
		assertNoSnapshotWorkspace(t, tree)

		if got := snapshotFiles(t, tree.source); !reflect.DeepEqual(got, before) {
			t.Fatal("snapshot acquisition changed source files, listings or modes")
		}
	})

	for _, selector := range []string{"", "+12025550111", snapshotACI} {
		t.Run("selector "+selector, func(t *testing.T) {
			t.Parallel()

			tree := newSnapshotTree(t)
			opts := tree.options()
			opts.SourceAccount = selector
			snapshot := must(accountimport.OpenSnapshotForTest(t.Context(), opts, tree.testOptions()))(t)
			check(t, snapshot.Close())
		})
	}

	tests := []struct {
		name string
		edit func(*testing.T, snapshotTree)
		want error
	}{
		{"wrong root", func(t *testing.T, tree snapshotTree) {
			t.Helper()
			check(t, os.Rename(tree.data, tree.data+"-old"))
		}, signal.ErrInvalidAccountImport},
		{"missing account JSON", func(t *testing.T, tree snapshotTree) {
			t.Helper()
			check(t, os.Remove(tree.account))
		}, signal.ErrInvalidAccountImport},
		{"unsupported registry", func(t *testing.T, tree snapshotTree) {
			t.Helper()

			editSnapshotJSON(t, filepath.Join(tree.data, "accounts.json"), func(v map[string]any) { v["version"] = 3 })
		}, signal.ErrUnsupportedAccountImport},
		{"unknown JSON field", func(t *testing.T, tree snapshotTree) {
			t.Helper()

			editSnapshotJSON(t, tree.account, func(v map[string]any) { v["futureField"] = true })
		}, signal.ErrUnsupportedAccountImport},
		{"unsupported account", func(t *testing.T, tree snapshotTree) {
			t.Helper()

			editSnapshotJSON(t, tree.account, func(v map[string]any) { v["version"] = 12 })
		}, signal.ErrUnsupportedAccountImport},
		{"number mismatch", func(t *testing.T, tree snapshotTree) {
			t.Helper()

			editSnapshotJSON(t, tree.account, func(v map[string]any) { v[snapshotNumberField] = "+12025550112" })
		}, signal.ErrInvalidAccountImport},
		{"ACI mismatch", func(t *testing.T, tree snapshotTree) {
			t.Helper()

			editSnapshotJSON(t, tree.account, func(v map[string]any) {
				snapshotJSONObject(t, v["aciAccountData"])["serviceId"] = snapshotOtherACI
			})
		}, signal.ErrInvalidAccountImport},
		{"not LIVE", func(t *testing.T, tree snapshotTree) {
			t.Helper()

			editSnapshotJSON(t, tree.account, func(v map[string]any) { v["serviceEnvironment"] = "STAGING" })
		}, signal.ErrUnsupportedAccountImport},
		{"primary device", func(t *testing.T, tree snapshotTree) {
			t.Helper()

			editSnapshotJSON(t, tree.account, func(v map[string]any) { v["deviceId"] = 1 })
		}, signal.ErrUnsupportedAccountImport},
		{"not registered", func(t *testing.T, tree snapshotTree) {
			t.Helper()

			editSnapshotJSON(t, tree.account, func(v map[string]any) { v["registered"] = false })
		}, signal.ErrUnsupportedAccountImport},
		{"duplicate nested key", func(t *testing.T, tree snapshotTree) {
			t.Helper()

			raw := must(os.ReadFile(tree.account))(t)
			raw = bytes.Replace(raw, []byte(`"registrationId" : 123`), []byte(`"registrationId":123,"registrationId":123`), 1)
			check(t, os.WriteFile(tree.account, raw, 0o600))
		}, signal.ErrInvalidAccountImport},
		{"duplicate escaped key", func(t *testing.T, tree snapshotTree) {
			t.Helper()

			raw := must(os.ReadFile(tree.account))(t)
			raw = bytes.Replace(raw, []byte(`"version" : 11`), []byte(`"version":11,"\u0076ersion":11`), 1)
			check(t, os.WriteFile(tree.account, raw, 0o600))
		}, signal.ErrInvalidAccountImport},
		{"trailing JSON", func(t *testing.T, tree snapshotTree) {
			t.Helper()

			raw := append(must(os.ReadFile(tree.account))(t), []byte(` {}`)...)
			check(t, os.WriteFile(tree.account, raw, 0o600))
		}, signal.ErrInvalidAccountImport},
		{"registry trailing JSON", func(t *testing.T, tree snapshotTree) {
			t.Helper()

			path := filepath.Join(tree.data, "accounts.json")
			check(t, os.WriteFile(path, append(must(os.ReadFile(path))(t), []byte(` null`)...), 0o600))
		}, signal.ErrInvalidAccountImport},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			tree := newSnapshotTree(t)
			test.edit(t, tree)
			before := snapshotFiles(t, tree.source)
			assertSnapshotError(t, tree, test.want)

			if !reflect.DeepEqual(snapshotFiles(t, tree.source), before) {
				t.Fatal("refusal changed source")
			}
		})
	}

	t.Run("required fields", testSnapshotRequiredFields)
	t.Run("selection", testSnapshotSelection)
	t.Run("limits", testSnapshotLimits)
}

func testSnapshotRequiredFields(t *testing.T) {
	t.Helper()
	t.Parallel()

	fields := make([]string, 0, 25)
	fields = append(fields,
		"version", "timestamp", "serviceEnvironment", "registered", snapshotNumberField, "deviceId",
		"isMultiDevice", "password", "profileKey", "aciAccountData", "pniAccountData",
	)

	for _, namespace := range []string{"aciAccountData", "pniAccountData"} {
		for _, field := range []string{
			"serviceId", "registrationId", "identityPrivateKey", "identityPublicKey",
			"nextPreKeyId", "nextSignedPreKeyId", "nextKyberPreKeyId",
		} {
			fields = append(fields, namespace+"."+field)
		}
	}

	for _, field := range fields {
		for _, bad := range []string{"missing", "null", "wrong type"} {
			t.Run(field+"/"+bad, func(t *testing.T) {
				t.Parallel()

				tree := newSnapshotTree(t)
				editSnapshotJSON(t, tree.account, func(value map[string]any) {
					key := field
					if parent, child, nested := strings.Cut(field, "."); nested {
						value = snapshotJSONObject(t, value[parent])
						key = child
					}

					switch bad {
					case "missing":
						delete(value, key)
					case "null":
						value[key] = nil
					case "wrong type":
						value[key] = []any{}
					}
				})
				assertSnapshotError(t, tree, signal.ErrInvalidAccountImport)
			})
		}
	}
}

//nolint:cyclop,funlen // Keep the complementary cases and their resource assertions in one test matrix.
func testSnapshotSelection(t *testing.T) {
	t.Helper()
	t.Parallel()

	for _, kind := range []string{
		"ambiguous",
		snapshotDuplicateACI,
		snapshotDuplicateNumber,
		snapshotDuplicatePath,
		snapshotExplicit,
		snapshotNoFallback,
		snapshotStagingIgnored,
		"unknown selector",
		"bad selector",
	} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			tree := newSnapshotTree(t)
			editSnapshotJSON(t, filepath.Join(tree.data, "accounts.json"), func(value map[string]any) {
				other := map[string]any{
					snapshotNumberField: "+12025550112", snapshotUUIDField: snapshotOtherACI,
					snapshotEnvironmentField: snapshotLive, snapshotPathField: "other",
				}

				switch kind {
				case snapshotDuplicateACI:
					other[snapshotUUIDField] = snapshotACI
				case snapshotDuplicateNumber:
					other[snapshotNumberField] = "+12025550111"
				case snapshotDuplicatePath:
					other[snapshotPathField] = snapshotBasename
				case snapshotStagingIgnored:
					other[snapshotEnvironmentField] = "STAGING"
				}

				value["accounts"] = append(snapshotJSONArray(t, value["accounts"]), other)
			})
			opts := tree.options()
			want := signal.ErrAmbiguousAccountImport

			switch kind {
			case snapshotExplicit, snapshotNoFallback, snapshotDuplicateACI, snapshotDuplicateNumber, snapshotDuplicatePath:
				opts.SourceAccount = "+12025550111"
			case "unknown selector":
				opts.SourceAccount = "+12025550119"
				want = signal.ErrInvalidAccountImport
			case "bad selector":
				opts.SourceAccount = "not-an-account"
				want = signal.ErrInvalidAccountImport
			}

			if kind == snapshotNoFallback {
				check(t, os.Remove(tree.account))

				want = signal.ErrInvalidAccountImport
			}

			snapshot, err := accountimport.OpenSnapshotForTest(t.Context(), opts, tree.testOptions())
			if kind == snapshotExplicit || kind == snapshotStagingIgnored {
				check(t, err)
				check(t, snapshot.Close())
			} else {
				if snapshot != nil {
					check(t, snapshot.Close())
				}

				if !errors.Is(err, want) {
					t.Fatalf("selection error = %v, want %v", err, want)
				}
			}

			assertNoSnapshotWorkspace(t, tree)
		})
	}
}

//nolint:cyclop,funlen // Keep the complementary cases and their resource assertions in one test matrix.
func testSnapshotLimits(t *testing.T) {
	t.Helper()
	t.Parallel()

	for _, kind := range []string{
		snapshotAccountExactBound,
		"account exceeds JSON bound",
		snapshotRegistryExceedsBound,
		"lower JSON limit",
		snapshotSQLExact,
		snapshotSQLExceeds,
		snapshotWALExceeds,
	} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			tree := newSnapshotTree(t)
			config := tree.testOptions()
			success := false

			switch kind {
			case snapshotAccountExactBound, "account exceeds JSON bound", snapshotRegistryExceedsBound:
				path := tree.account
				if kind == snapshotRegistryExceedsBound {
					path = filepath.Join(tree.data, "accounts.json")
				}

				raw := must(os.ReadFile(path))(t)
				size := 4 * 1024 * 1024

				success = kind == snapshotAccountExactBound
				if !success {
					size++
				}

				check(t, os.WriteFile(path, append(raw, bytes.Repeat([]byte(" "), size-len(raw))...), 0o600))
			case "lower JSON limit":
				config.JSONLimit = 32
			case snapshotSQLExact, snapshotSQLExceeds, snapshotWALExceeds:
				config.SQLLimit = 32

				check(t, os.WriteFile(tree.database, bytes.Repeat([]byte("D"), 16), 0o600))
				check(t, os.WriteFile(tree.database+"-wal", bytes.Repeat([]byte("W"), 8), 0o600))
				check(t, os.WriteFile(tree.database+snapshotSHMSuffix, bytes.Repeat([]byte("S"), 8), 0o600))

				success = kind == snapshotSQLExact
				switch kind {
				case snapshotSQLExceeds:
					check(t, os.WriteFile(tree.database, bytes.Repeat([]byte("D"), 33), 0o600))
				case snapshotWALExceeds:
					check(t, os.WriteFile(tree.database+"-wal", bytes.Repeat([]byte("W"), 9), 0o600))
				}
			}

			snapshot, err := accountimport.OpenSnapshotForTest(t.Context(), tree.options(), config)
			if snapshot != nil {
				check(t, snapshot.Close())
			}

			if success {
				check(t, err)
			} else if !errors.Is(err, signal.ErrInvalidAccountImport) {
				t.Fatalf("limit error = %v", err)
			}

			assertNoSnapshotWorkspace(t, tree)
		})
	}
}

func TestAccountImportPaths(t *testing.T) {
	t.Parallel()
	t.Run("basenames", testSnapshotBasenames)
	t.Run("symlinks", testSnapshotSymlinks)
	t.Run("special files", testSnapshotSpecialFiles)
	t.Run("overlap", testSnapshotOverlap)
}

func testSnapshotBasenames(t *testing.T) {
	t.Helper()
	t.Parallel()

	for _, path := range []string{"../escape", "/absolute", ".", "..", "nested/name", `nested\name`, "", "accounts.json"} {
		t.Run("registry basename "+path, func(t *testing.T) {
			t.Parallel()

			tree := newSnapshotTree(t)
			editSnapshotJSON(t, filepath.Join(tree.data, "accounts.json"), func(value map[string]any) {
				snapshotJSONObject(t, snapshotJSONArray(t, value["accounts"])[0])[snapshotPathField] = path
			})
			assertSnapshotError(t, tree, signal.ErrInvalidAccountImport)
		})
	}
}

func testSnapshotSymlinks(t *testing.T) {
	t.Helper()
	t.Parallel()

	for _, name := range []string{
		"root",
		"ancestor",
		snapshotDataDir,
		snapshotAccountFile,
		"database directory",
		"database",
		snapshotWAL,
		snapshotSHM,
		snapshotCache,
		snapshotDangling,
	} {
		t.Run("symlink "+name, func(t *testing.T) {
			t.Parallel()

			tree := newSnapshotTree(t)

			path := snapshotLinkPath(tree, name)
			switch name {
			case snapshotWAL, snapshotSHM:
				check(t, os.WriteFile(path, []byte("sidecar"), 0o600))
			case snapshotCache:
				check(t, os.Mkdir(path, 0o700))
			}

			if name == snapshotDangling {
				check(t, os.Remove(path))
				check(t, os.Symlink(path+"-absent", path))
			} else {
				check(t, os.Rename(path, path+"-real"))
				check(t, os.Symlink(path+"-real", path))
			}

			assertSnapshotError(t, tree, signal.ErrInvalidAccountImport)
		})
	}
}

func testSnapshotSpecialFiles(t *testing.T) {
	t.Helper()
	t.Parallel()

	for _, name := range []string{"registry", snapshotAccountFile, "database", snapshotWAL, snapshotSHM} {
		for _, special := range []string{"FIFO", "device"} {
			t.Run(name+" "+special, func(t *testing.T) {
				t.Parallel()

				tree := newSnapshotTree(t)

				path := snapshotLinkPath(tree, name)
				if name != snapshotWAL && name != snapshotSHM {
					check(t, os.Remove(path))
				}

				if special == "FIFO" {
					check(t, unix.Mkfifo(path, 0o600))
				} else {
					// /dev/null is a real character device; source paths themselves
					// must also reject symlinks before opening it.
					check(t, os.Symlink("/dev/null", path))
				}

				assertSnapshotError(t, tree, signal.ErrInvalidAccountImport)
			})
		}
	}
}

//nolint:cyclop,funlen // Each overlap topology needs its own filesystem setup.
func testSnapshotOverlap(t *testing.T) {
	t.Helper()
	t.Parallel()

	for _, kind := range []string{
		"equal",
		"destination below",
		"destination above",
		"nonexistent ancestors",
		"source alias",
		"destination alias",
		"hard-linked database",
		snapshotHardlinkAccount,
		snapshotHardlinkRegistry,
		"temp inside source",
	} {
		t.Run("overlap "+kind, func(t *testing.T) {
			t.Parallel()

			tree := newSnapshotTree(t)
			opts, config := tree.options(), tree.testOptions()

			switch kind {
			case "equal":
				opts.DataDir = tree.source
			case "destination below", "nonexistent ancestors":
				opts.DataDir = filepath.Join(tree.source, "missing", "nested", "destination")
			case "destination above":
				opts.DataDir = filepath.Dir(tree.source)
			case "source alias":
				alias := tree.source + "-alias"
				check(t, os.Symlink(tree.source, alias))
				opts.SourceDir = alias
			case "destination alias":
				alias := tree.destination + "-alias"
				check(t, os.Symlink(tree.source, alias))
				opts.DataDir = filepath.Join(alias, "missing", "nested")
			case "hard-linked database", snapshotHardlinkAccount, snapshotHardlinkRegistry:
				check(t, os.Mkdir(tree.destination, 0o700))

				path := tree.database

				switch kind {
				case snapshotHardlinkAccount:
					path = tree.account
				case snapshotHardlinkRegistry:
					path = filepath.Join(tree.data, "accounts.json")
				}

				check(t, os.Link(path, filepath.Join(tree.destination, "aliased-file")))
			case "temp inside source":
				config.TempDir = tree.source
			}

			before := snapshotFiles(t, tree.source)

			snapshot, err := accountimport.OpenSnapshotForTest(t.Context(), opts, config)
			if snapshot != nil {
				check(t, snapshot.Close())
			}

			if !errors.Is(err, signal.ErrInvalidAccountImport) {
				t.Fatalf("overlap error = %v", err)
			}

			if !reflect.DeepEqual(snapshotFiles(t, tree.source), before) {
				t.Fatal("overlap refusal changed source")
			}

			assertNoSnapshotWorkspace(t, tree)
		})
	}
}

func snapshotLinkPath(tree snapshotTree, name string) string {
	paths := map[string]string{
		"root": tree.source, "ancestor": filepath.Dir(tree.source), snapshotDataDir: tree.data,
		"registry":          filepath.Join(tree.data, "accounts.json"),
		snapshotAccountFile: tree.account, snapshotDangling: tree.account,
		"database directory": filepath.Dir(tree.database),
		snapshotWAL:          tree.database + "-wal",
		snapshotSHM:          tree.database + snapshotSHMSuffix,
		snapshotCache:        filepath.Join(filepath.Dir(tree.database), "msg-cache"),
	}
	if path, ok := paths[name]; ok {
		return path
	}

	return tree.database
}

func snapshotJSONObject(t *testing.T, value any) map[string]any {
	t.Helper()

	object, ok := value.(map[string]any)
	if !ok {
		t.Fatal("invalid test JSON object")
	}

	return object
}

func snapshotJSONArray(t *testing.T, value any) []any {
	t.Helper()

	array, ok := value.([]any)
	if !ok {
		t.Fatal("invalid test JSON array")
	}

	return array
}

// Contents, directory entries, permissions and modification times are observed
// independently of the snapshot implementation. Access times are deliberately ignored.
func snapshotFiles(t *testing.T, root string) map[string]string {
	t.Helper()

	files := make(map[string]string)

	check(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		info, infoErr := entry.Info()
		if infoErr != nil {
			return fmt.Errorf("source fixture info: %w", infoErr)
		}

		value := info.Mode().String() + ":" + info.ModTime().String()

		if entry.Type().IsRegular() {
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return fmt.Errorf("source fixture read: %w", readErr)
			}

			value += ":" + string(raw)
		}

		files[strings.TrimPrefix(path, root)] = value

		return nil
	}))

	return files
}

// These tests are added before change detection is implemented; cancellation
// must also remove a workspace created earlier in the streamed operation.
//
//nolint:cyclop,funlen // Keep the complementary cases and their resource assertions in one test matrix.
func TestAccountImportSourceChanged(t *testing.T) {
	t.Parallel()

	for _, change := range []string{
		"truncate",
		"same length",
		"file replacement",
		"parent replacement",
		snapshotRootReplacement,
		"WAL appears",
		snapshotWALDisappears,
		snapshotSHMReplacement,
		"cache appears",
		snapshotCacheMessage,
		"destination rebind",
	} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()

			tree := newSnapshotTree(t)

			cache := filepath.Join(filepath.Dir(tree.database), "msg-cache")

			switch change {
			case snapshotWALDisappears:
				check(t, os.WriteFile(tree.database+"-wal", []byte("wal"), 0o600))
			case snapshotSHMReplacement:
				check(t, os.WriteFile(tree.database+snapshotSHMSuffix, []byte("shm"), 0o600))
			case snapshotCacheMessage:
				check(t, os.MkdirAll(filepath.Join(cache, "123"), 0o700))
			}

			snapshot := must(accountimport.OpenSnapshotForTest(t.Context(), tree.options(), tree.testOptions()))(t)
			defer func() { check(t, snapshot.Close()) }()

			switch change {
			case "truncate":
				check(t, os.Truncate(tree.database, 1))
			case "same length":
				info := must(os.Stat(tree.database))(t)
				check(t, os.WriteFile(tree.database, bytes.Repeat([]byte("X"), int(info.Size())), 0o600))
				check(t, os.Chtimes(tree.database, info.ModTime(), info.ModTime()))
			case "file replacement", snapshotSHMReplacement:
				path := tree.database
				if change == snapshotSHMReplacement {
					path += snapshotSHMSuffix
				}

				raw := must(os.ReadFile(path))(t)
				check(t, os.Rename(path, path+"-old"))
				check(t, os.WriteFile(path, raw, 0o600))
			case "parent replacement", snapshotRootReplacement:
				path := filepath.Dir(tree.database)
				if change == snapshotRootReplacement {
					path = tree.source
				}

				check(t, os.Rename(path, path+"-old"))
				check(t, os.Mkdir(path, 0o700))
			case "WAL appears":
				check(t, os.WriteFile(tree.database+"-wal", []byte("new WAL"), 0o600))
			case snapshotWALDisappears:
				check(t, os.Remove(tree.database+"-wal"))
			case "cache appears":
				check(t, os.Mkdir(cache, 0o700))
			case snapshotCacheMessage:
				check(t, os.WriteFile(filepath.Join(cache, "123", "message"), []byte("cached"), 0o600))
			case "destination rebind":
				check(t, os.Symlink(tree.source, tree.destination))
			}

			err := snapshot.Revalidate(t.Context())
			if !errors.Is(err, signal.ErrAccountImportSourceChanged) {
				t.Fatalf("change detection error = %v", err)
			}

			check(t, snapshot.Close())
			assertNoSnapshotWorkspace(t, tree)
		})
	}

	for _, operation := range []string{snapshotCopyDatabase, snapshotHashDatabase} {
		t.Run("cancel during "+operation, func(t *testing.T) {
			t.Parallel()

			tree := newSnapshotTree(t)
			check(t, os.WriteFile(tree.database, bytes.Repeat([]byte("D"), 256*1024), 0o600))

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			config := tree.testOptions()
			seen := false
			config.Step = func(step string) {
				if step == operation {
					seen = true

					cancel()
				}
			}

			snapshot, err := accountimport.OpenSnapshotForTest(ctx, tree.options(), config)
			if snapshot != nil {
				check(t, snapshot.Close())
			}

			if !seen || !errors.Is(err, context.Canceled) {
				t.Fatalf("stream cancellation: seen=%v, error=%v", seen, err)
			}

			assertNoSnapshotWorkspace(t, tree)
		})
	}
}
