//go:build (cgo || libsignal_go) && linux

package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

const (
	importedNumber = "+15550177"
	importedPNI    = "77777777-7777-4777-8777-777777777777"
	// publishCrashExit is the exit status of a helper process stopped at a publication step.
	publishCrashExit = 17

	stepIntent     = "intent"
	stepRegistered = "registered"
	actionUnlink   = "unlink"
	actionReimport = "reimport"
	actionCrash    = "crash"
)

var errStop = errors.New("stopped by the test")

// importedEntry is the account the tests import, as secondACI.
func importedEntry() store.AccountEntry {
	return store.AccountEntry{Number: importedNumber, ACI: secondACI, PNI: importedPNI, DeviceID: 3}
}

// buildDevice stores a logged-in device for entry, as a converter would.
func buildDevice(t *testing.T, entry store.AccountEntry) func(context.Context, *store.Store) error {
	t.Helper()

	data := newDevice(t, uuid.MustParse(entry.ACI))
	data.Number, data.PNI, data.DeviceID = entry.Number, uuid.MustParse(entry.PNI), entry.DeviceID

	return func(ctx context.Context, s *store.Store) error {
		return s.Devices.PutDevice(ctx, data)
	}
}

// stageImport builds entry's account in a new import attempt.
func stageImport(t *testing.T, dir *store.Dir, entry store.AccountEntry) *store.Import {
	t.Helper()

	im := stateValue(dir.BeginImport())(t)
	stateCheck(t, im.Build(t.Context(), entry.ACI, buildDevice(t, entry), zerolog.Nop()))

	return im
}

// seedRegistered creates a registered account with a logged-in device, as linking would.
func seedRegistered(t *testing.T, dir *store.Dir, entry store.AccountEntry) {
	t.Helper()

	data := stateValue(dir.OpenAccount(t.Context(), entry.ACI, zerolog.Nop()))(t)
	stateCheck(t, buildDevice(t, entry)(t.Context(), data))
	stateCheck(t, data.Close())
	putAccount(t, dir, entry)
}

// byACI selects the entry of aci, like the client reselecting its account.
func byACI(aci string) func([]store.AccountEntry) (store.AccountEntry, error) {
	return func(entries []store.AccountEntry) (store.AccountEntry, error) {
		for _, entry := range entries {
			if entry.ACI == aci {
				return entry, nil
			}
		}

		return store.AccountEntry{}, store.ErrAccountChanged
	}
}

func openRegistered(dir *store.Dir, aci string, mode store.OpenMode, expect store.Generation) (*store.Opened, error) {
	opened, err := dir.OpenRegistered(context.Background(), byACI(aci), mode, expect, zerolog.Nop())
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", aci, err)
	}

	return opened, nil
}

func closeOpened(t *testing.T, opened *store.Opened) {
	t.Helper()

	stateCheck(t, opened.Store.Close())

	if opened.Lock != nil {
		stateCheck(t, opened.Lock.Unlock())
	}
}

// wantPublished checks that entry is registered once, opens with its device, and that nothing of
// the import attempt is left.
func wantPublished(t *testing.T, dir *store.Dir, entry store.AccountEntry) {
	t.Helper()

	accounts := stateValue(dir.Accounts())(t)
	if !slices.Contains(accounts, entry) || len(accounts) != 1 {
		t.Fatalf("accounts = %+v, want only %+v", accounts, entry)
	}

	opened := stateValue(openRegistered(dir, entry.ACI, store.OpenConnected, store.Generation{}))(t)
	device := stateValue(opened.Store.DeviceByACI(t.Context(), uuid.MustParse(entry.ACI)))(t)

	if device == nil || !device.IsDeviceLoggedIn() || device.Number != entry.Number {
		t.Errorf("published device = %+v", device)
	}

	closeOpened(t, opened)

	if got := perm(t, dir.AccountDir(entry.ACI)); got != 0o700 {
		t.Errorf("account dir perm = %o", got)
	}

	if got := perm(t, filepath.Join(dir.AccountDir(entry.ACI), "account.db")); got != 0o600 {
		t.Errorf("account.db perm = %o", got)
	}

	wantMissing(t, filepath.Join(dir.AccountDir(entry.ACI), ".import-owner"))
	wantNoAttempts(t, dir)
}

func wantNoAttempts(t *testing.T, dir *store.Dir) {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(dir.Path(), ".imports"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) || len(entries) != 0 {
		t.Errorf("import attempts left: %v, %v", entries, err)
	}
}

func wantMissing(t *testing.T, path string) {
	t.Helper()

	_, err := os.Lstat(path)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s exists: %v", path, err)
	}
}

func wantExists(t *testing.T, path string) {
	t.Helper()

	_, err := os.Lstat(path)
	if err != nil {
		t.Errorf("%s: %v", path, err)
	}
}

func TestPublishImport(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)
	entry := importedEntry()

	im := stageImport(t, dir, entry)
	stateCheck(t, im.Publish(entry))
	stateCheck(t, im.Close())

	wantPublished(t, dir, entry)

	// A second import of the published account is a collision, not an overwrite.
	again := stageImport(t, dir, entry)

	err := again.Publish(entry)
	if !errors.Is(err, store.ErrImportCollision) {
		t.Errorf("republish: %v, want ErrImportCollision", err)
	}

	stateCheck(t, again.Close())
	wantPublished(t, dir, entry)
}

// TestPublishCollision refuses every existing entry or file the account would clash with, and
// leaves the attempt intact.
func TestPublishCollision(t *testing.T) {
	t.Parallel()

	entry := importedEntry()

	for name, setup := range map[string]func(*testing.T, *store.Dir){
		"entry": func(t *testing.T, dir *store.Dir) {
			t.Helper()
			seedRegistered(t, dir, store.AccountEntry{Number: "+15550101", ACI: secondACI, PNI: uuid.NewString()})
		},
		"unlinked": func(t *testing.T, dir *store.Dir) {
			t.Helper()
			putAccount(t, dir, store.AccountEntry{Number: importedNumber, ACI: secondACI, UnlinkedAt: time.Unix(5, 0).UTC()})
		},
		"orphan": func(t *testing.T, dir *store.Dir) {
			t.Helper()
			stateCheck(t, os.Mkdir(dir.AccountDir(secondACI), 0o700))
		},
		"symlink": func(t *testing.T, dir *store.Dir) {
			t.Helper()
			stateCheck(t, os.Symlink(t.TempDir(), dir.AccountDir(secondACI)))
		},
		"number": func(t *testing.T, dir *store.Dir) {
			t.Helper()
			putAccount(t, dir, store.AccountEntry{Number: importedNumber, ACI: testACI})
		},
		"pni": func(t *testing.T, dir *store.Dir) {
			t.Helper()
			putAccount(t, dir, store.AccountEntry{Number: "+15550101", ACI: testACI, PNI: importedPNI})
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := openDir(t, io.Discard)
			setup(t, dir)

			before := stateValue(dir.Accounts())(t)
			im := stageImport(t, dir, entry)

			err := im.Publish(entry)
			if !errors.Is(err, store.ErrImportCollision) {
				t.Fatalf("publish: %v, want ErrImportCollision", err)
			}

			if after := stateValue(dir.Accounts())(t); !slices.Equal(after, before) {
				t.Errorf("accounts = %+v, want %+v", after, before)
			}

			wantExists(t, filepath.Join(im.Stage(), secondACI, "account.db"))
			wantMissing(t, filepath.Join(im.Stage(), "intent.json"))

			stateCheck(t, im.Close())
			wantNoAttempts(t, dir)
		})
	}
}

// TestPublishInterrupted stops publication after each step, then lets the process "crash" by
// releasing the owner lock: the account is invisible until it is registered, and the next
// registry operation rolls a committed attempt forward and removes an uncommitted one.
func TestPublishInterrupted(t *testing.T) {
	t.Parallel()

	for i, step := range store.PublishSteps() {
		t.Run(step, func(t *testing.T) {
			t.Parallel()

			dir := openDir(t, io.Discard)
			entry := importedEntry()
			committed := i >= slices.Index(store.PublishSteps(), stepIntent)
			registered := i >= slices.Index(store.PublishSteps(), stepRegistered)

			dir.SetPublishHook(func(name string) error {
				if name == step {
					return errStop
				}

				return nil
			})

			im, err := interruptedImport(t, dir, entry)
			if !errors.Is(err, errStop) || committed != errors.Is(err, store.ErrRecoveryRequired) {
				t.Fatalf("publish stopped after %s: %v", step, err)
			}

			dir.SetPublishHook(nil)

			// While the attempt is alive, other processes leave it alone and can't see it
			// before it is registered.
			other := stateValue(store.OpenDir(dir.Path(), slog.New(slog.DiscardHandler)))(t)
			if got := stateValue(other.Accounts())(t); slices.Contains(got, entry) != registered {
				t.Errorf("accounts while running = %+v", got)
			}

			if im != nil {
				wantExists(t, im.Stage())
				im.Abandon()
			}

			if committed {
				wantPublished(t, dir, entry)

				return
			}

			if got := stateValue(dir.Accounts())(t); len(got) != 0 {
				t.Errorf("accounts after cleanup = %+v", got)
			}

			wantMissing(t, dir.AccountDir(secondACI))
			wantNoAttempts(t, dir)
		})
	}
}

// interruptedImport runs an import of entry until the publish hook stops it.
func interruptedImport(t *testing.T, dir *store.Dir, entry store.AccountEntry) (*store.Import, error) {
	t.Helper()

	im, err := dir.BeginImport()
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}

	err = im.Build(t.Context(), entry.ACI, buildDevice(t, entry), zerolog.Nop())
	if err == nil {
		err = im.Publish(entry)
	}

	if err != nil {
		return im, fmt.Errorf("import: %w", err)
	}

	return im, nil
}

// TestRecoveryEvidence: recovery refuses to finish an attempt whose files disagree with its
// intent, and changes nothing.
func TestRecoveryEvidence(t *testing.T) {
	t.Parallel()

	for name, tc := range recoveryCases() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			wantRecoveryRefused(t, tc)
		})
	}
}

// recoveryCase interrupts publication after step, then tampers with the attempt.
type recoveryCase struct {
	step   string
	tamper func(t *testing.T, dir *store.Dir, stage string)
}

func recoveryCases() map[string]recoveryCase {
	appendByte := func(t *testing.T, path string) {
		t.Helper()

		file := stateValue(os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0))(t)
		_, err := file.Write([]byte{0})
		stateCheck(t, errors.Join(err, file.Close()))
	}

	return map[string]recoveryCase{
		"staged digest": {stepIntent, func(t *testing.T, _ *store.Dir, stage string) {
			t.Helper()
			appendByte(t, filepath.Join(stage, secondACI, "account.db"))
		}},
		"renamed digest": {"renamed", func(t *testing.T, dir *store.Dir, _ string) {
			t.Helper()
			appendByte(t, filepath.Join(dir.AccountDir(secondACI), "account.db"))
		}},
		"marker token": {stepIntent, func(t *testing.T, _ *store.Dir, stage string) {
			t.Helper()
			writeFile(t, filepath.Join(stage, secondACI, ".import-owner"), "someone else")
		}},
		"missing marker": {"renamed", func(t *testing.T, dir *store.Dir, _ string) {
			t.Helper()
			stateCheck(t, os.Remove(filepath.Join(dir.AccountDir(secondACI), ".import-owner")))
		}},
		"both locations": {stepIntent, func(t *testing.T, dir *store.Dir, _ string) {
			t.Helper()
			stateCheck(t, os.Mkdir(dir.AccountDir(secondACI), 0o700))
		}},
		"conflicting entry": {stepIntent, func(t *testing.T, dir *store.Dir, _ string) {
			t.Helper()
			writeFile(t, filepath.Join(dir.Path(), "accounts.json"),
				`{"version":1,"accounts":[{"number":"+15550101","aci":"`+secondACI+`","deviceId":2}]}`)
		}},
	}
}

// wantRecoveryRefused runs testCase: recovery must fail with ErrRecoveryRequired, every time,
// without changing any file.
func wantRecoveryRefused(t *testing.T, testCase recoveryCase) {
	t.Helper()

	dir := openDir(t, io.Discard)

	dir.SetPublishHook(func(name string) error {
		if name == testCase.step {
			return errStop
		}

		return nil
	})

	im, err := interruptedImport(t, dir, importedEntry())
	if !errors.Is(err, store.ErrRecoveryRequired) {
		t.Fatalf("publish: %v", err)
	}

	dir.SetPublishHook(nil)
	im.Abandon()
	testCase.tamper(t, dir, im.Stage())

	before := snapshotTree(t, dir.Path())

	for range 2 {
		_, err = dir.Accounts()
		if !errors.Is(err, store.ErrRecoveryRequired) {
			t.Fatalf("Accounts: %v, want ErrRecoveryRequired", err)
		}
	}

	if after := snapshotTree(t, dir.Path()); !slices.Equal(after, before) {
		t.Errorf("recovery changed files:\n%v\nwant\n%v", after, before)
	}
}

// snapshotTree lists the paths, sizes and modes below root, except the registry lock.
func snapshotTree(t *testing.T, root string) []string {
	t.Helper()

	var out []string

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("info: %w", err)
		}

		rel := stateValue(filepath.Rel(root, path))(t)
		if rel != "accounts.lock" {
			out = append(out, rel+" "+info.Mode().String()+" "+strconv.FormatInt(info.Size(), 10))
		}

		return nil
	})
	stateCheck(t, err)

	return out
}

func TestRecoverySkipsActiveStage(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)
	im := stageImport(t, dir, importedEntry())

	other := stateValue(store.OpenDir(dir.Path(), slog.New(slog.DiscardHandler)))(t)
	_ = stateValue(other.Accounts())(t)

	wantExists(t, filepath.Join(im.Stage(), secondACI, "account.db"))

	stateCheck(t, im.Close())
	wantNoAttempts(t, dir)
}

func TestOpenRegisteredMissing(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)
	putAccount(t, dir, store.AccountEntry{Number: "+15550100", ACI: testACI})

	for _, mode := range []store.OpenMode{store.OpenSnapshot, store.OpenConnected} {
		_, err := openRegistered(dir, testACI, mode, store.Generation{})
		if !errors.Is(err, store.ErrAccountMissing) {
			t.Errorf("mode %d without a directory: %v", mode, err)
		}

		wantMissing(t, dir.AccountDir(testACI))
	}

	stateCheck(t, os.Mkdir(dir.AccountDir(testACI), 0o700))

	for _, mode := range []store.OpenMode{store.OpenSnapshot, store.OpenConnected} {
		_, err := openRegistered(dir, testACI, mode, store.Generation{})
		if !errors.Is(err, store.ErrAccountMissing) {
			t.Errorf("mode %d without a database: %v", mode, err)
		}

		wantMissing(t, filepath.Join(dir.AccountDir(testACI), "account.db"))
	}
}

// TestOpenRegisteredModes: a connected open holds the account lock, a snapshot neither needs
// nor keeps it, and a failed open doesn't keep the registry lock.
func TestOpenRegisteredModes(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)
	seedRegistered(t, dir, store.AccountEntry{Number: "+15550100", ACI: testACI, PNI: uuid.NewString()})

	connected := stateValue(openRegistered(dir, testACI, store.OpenConnected, store.Generation{}))(t)
	if connected.Lock == nil {
		t.Fatal("connected open holds no lock")
	}

	start := time.Now()

	_, err := openRegistered(dir, testACI, store.OpenConnected, store.Generation{})
	if !errors.Is(err, store.ErrAccountInUse) || time.Since(start) > time.Second {
		t.Fatalf("second connected open: %v after %v", err, time.Since(start))
	}

	snapshot := stateValue(openRegistered(dir, testACI, store.OpenSnapshot, store.Generation{}))(t)
	if snapshot.Lock != nil {
		t.Error("snapshot holds a lock")
	}

	device := stateValue(snapshot.Store.DeviceByACI(t.Context(), uuid.MustParse(testACI)))(t)
	if device == nil {
		t.Error("snapshot has no device")
	}

	closeOpened(t, snapshot)
	closeOpened(t, connected)

	closeOpened(t, stateValue(openRegistered(dir, testACI, store.OpenSnapshot, store.Generation{}))(t))
	closeOpened(t, stateValue(openRegistered(dir, testACI, store.OpenConnected, store.Generation{}))(t))
}

// TestRegistryLockOrder: a process holding the account lock may take the registry lock, and one
// holding the registry lock never waits for the account lock.
func TestRegistryLockOrder(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)
	seedRegistered(t, dir, store.AccountEntry{Number: "+15550100", ACI: testACI, PNI: uuid.NewString()})

	lock := stateValue(dir.Lock(testACI))(t)
	defer func() { _ = lock.Unlock() }()

	start := time.Now()

	_, err := openRegistered(dir, testACI, store.OpenConnected, store.Generation{})
	if !errors.Is(err, store.ErrAccountInUse) || time.Since(start) > time.Second {
		t.Fatalf("connected open of a held account: %v after %v", err, time.Since(start))
	}

	stateCheck(t, dir.MarkUnlinked(testACI, time.Unix(9, 0).UTC()))

	if got := stateValue(dir.Accounts())(t); len(got) != 1 || got[0].UnlinkedAt.IsZero() {
		t.Errorf("accounts = %+v", got)
	}
}

func TestRegistryBusy(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)
	release := stateValue(dir.HoldRegistry())(t)

	_, err := dir.Accounts()
	if !errors.Is(err, store.ErrRegistryBusy) {
		t.Errorf("Accounts with a held registry: %v", err)
	}

	release()

	_ = stateValue(dir.Accounts())(t)
}

// TestSnapshotNoMigrate: a snapshot never migrates a database another process holds.
func TestSnapshotNoMigrate(t *testing.T) {
	t.Parallel()

	dir := openDir(t, io.Discard)
	seedRegistered(t, dir, store.AccountEntry{Number: "+15550100", ACI: testACI, PNI: uuid.NewString()})

	path := filepath.Join(dir.AccountDir(testACI), "account.db")
	version := func(update string) int {
		sqlDB := stateValue(sql.Open(store.SQLiteDriverForTest, path))(t)
		defer func() { _ = sqlDB.Close() }()

		if update != "" {
			_ = stateValue(sqlDB.ExecContext(t.Context(), update))(t)
		}

		var v int

		stateCheck(t, sqlDB.QueryRowContext(t.Context(), "SELECT version FROM gosignal_version").Scan(&v))

		return v
	}

	lowered := version("UPDATE gosignal_version SET version = version - 1")

	lock := stateValue(dir.Lock(testACI))(t)
	defer func() { _ = lock.Unlock() }()

	_, err := openRegistered(dir, testACI, store.OpenSnapshot, store.Generation{})
	if !errors.Is(err, store.ErrAccountInUse) {
		t.Errorf("snapshot of an outdated held database: %v", err)
	}

	if got := version(""); got != lowered {
		t.Errorf("version = %d, want it untouched at %d", got, lowered)
	}
}

// TestStaleSelection: an account opened before another process unlinks or reimports it is
// never reconnected, recreated or locked through its old files.
//
//nolint:paralleltest // forks helpers; a fork briefly shares the flocks of parallel tests
func TestStaleSelection(t *testing.T) {
	for _, action := range []string{actionUnlink, actionReimport} {
		t.Run(action, func(t *testing.T) {
			dir := openDir(t, io.Discard)
			seedRegistered(t, dir, store.AccountEntry{Number: "+15550100", ACI: secondACI, PNI: uuid.NewString()})

			snapshot := stateValue(openRegistered(dir, secondACI, store.OpenSnapshot, store.Generation{}))(t)
			closeOpened(t, snapshot)

			runHelper(t, dir.Path(), action, nil)

			_, err := openRegistered(dir, secondACI, store.OpenConnected, snapshot.Generation)
			if !errors.Is(err, store.ErrAccountChanged) {
				t.Fatalf("connect after %s: %v, want ErrAccountChanged", action, err)
			}

			if action == actionUnlink {
				wantMissing(t, dir.AccountDir(secondACI))

				return
			}

			// Selecting again finds the reimported account.
			wantPublished(t, dir, importedEntry())
		})
	}
}

// TestRegistryConcurrentUpdates runs registry writers in parallel processes: every entry, mark,
// removal and publication survives.
//
//nolint:paralleltest // forks helpers; a fork briefly shares the flocks of parallel tests
func TestRegistryConcurrentUpdates(t *testing.T) {
	const workers = 4

	dir := openDir(t, io.Discard)
	seedRegistered(t, dir, store.AccountEntry{Number: "+15550100", ACI: testACI, PNI: uuid.NewString()})

	cmds := make([]*exec.Cmd, 0, workers+2)
	cmds = append(cmds,
		helperCmd(t, dir.Path(), actionUnlink, []string{"GOSIGNAL_TEST_REGISTRY_ACI=" + testACI}),
		helperCmd(t, dir.Path(), "publish", nil))

	for worker := range workers {
		env := []string{"GOSIGNAL_TEST_REGISTRY_WORKER=" + strconv.Itoa(worker)}
		cmds = append(cmds, helperCmd(t, dir.Path(), "updates", env))
	}

	for _, cmd := range cmds {
		stateCheck(t, cmd.Start())
	}

	for _, cmd := range cmds {
		err := cmd.Wait()
		if err != nil {
			t.Errorf("%v: %v\n%s", cmd.Args, err, cmd.Stdout)
		}
	}

	wantConcurrentResult(t, dir, workers)
}

// wantConcurrentResult checks the registry after TestRegistryConcurrentUpdates.
func wantConcurrentResult(t *testing.T, dir *store.Dir, workers int) {
	t.Helper()

	accounts := stateValue(dir.Accounts())(t)
	if len(accounts) != workers*updatesPerWorker+1 || !slices.Contains(accounts, importedEntry()) {
		t.Fatalf("%d accounts: %+v", len(accounts), accounts)
	}

	for _, entry := range accounts {
		if entry.ACI == testACI {
			t.Errorf("removed account is back: %+v", entry)
		}

		if worker, i, ok := workerEntry(entry.ACI); ok && (i == 0) == entry.UnlinkedAt.IsZero() {
			t.Errorf("worker %d entry %d unlinked at %v", worker, i, entry.UnlinkedAt)
		}
	}

	wantMissing(t, dir.AccountDir(testACI))
}

const updatesPerWorker = 10

// workerACI is the ACI of the i-th entry of a registry update worker.
func workerACI(worker, i int) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(strconv.Itoa(worker)+"/"+strconv.Itoa(i))).String()
}

func workerEntry(aci string) (int, int, bool) {
	for worker := range 8 {
		for i := range updatesPerWorker {
			if workerACI(worker, i) == aci {
				return worker, i, true
			}
		}
	}

	return 0, 0, false
}

// TestPublicationCrash ends a publishing process after each durable step; the next process
// recovers exactly one published account or a clean data dir.
//
//nolint:paralleltest // forks helpers; a fork briefly shares the flocks of parallel tests
func TestPublicationCrash(t *testing.T) {
	for _, step := range store.PublishSteps() {
		t.Run(step, func(t *testing.T) {
			dir := openDir(t, io.Discard)
			env := []string{"GOSIGNAL_TEST_PUBLISH_STEP=" + step}
			runHelper(t, dir.Path(), actionCrash, env)
			runHelper(t, dir.Path(), "check", env)
		})
	}
}

func helperCmd(t *testing.T, dataDir, action string, env []string) *exec.Cmd {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run", "^TestRegistryHelperProcess$", "-test.v")

	cmd.Env = append(os.Environ(), append(env,
		"GOSIGNAL_TEST_REGISTRY_DIR="+dataDir, "GOSIGNAL_TEST_REGISTRY_ACTION="+action)...)

	var out limitedBuffer

	cmd.Stdout, cmd.Stderr = &out, &out

	return cmd
}

// runHelper runs one helper process and requires its expected exit status.
func runHelper(t *testing.T, dataDir, action string, env []string) {
	t.Helper()

	cmd := helperCmd(t, dataDir, action, env)
	err := cmd.Run()

	want := 0
	if action == actionCrash {
		want = publishCrashExit
	}

	var exit *exec.ExitError
	if (want == 0 && err != nil) || (want != 0 && (!errors.As(err, &exit) || exit.ExitCode() != want)) {
		t.Fatalf("helper %s: %v, want exit %d\n%s", action, err, want, cmd.Stdout)
	}
}

// limitedBuffer collects a helper's output.
type limitedBuffer struct {
	data []byte
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)

	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return string(b.data)
}

// TestRegistryHelperProcess is one step of the multi-process tests above and of
// scripts/test-account-publication.sh.
//
//nolint:paralleltest // one step per process
func TestRegistryHelperProcess(t *testing.T) {
	path, action := os.Getenv("GOSIGNAL_TEST_REGISTRY_DIR"), os.Getenv("GOSIGNAL_TEST_REGISTRY_ACTION")
	if path == "" {
		t.Skip("helper process of the registry tests")
	}

	dir := stateValue(store.OpenDir(path, slog.New(slog.DiscardHandler)))(t)
	step := os.Getenv("GOSIGNAL_TEST_PUBLISH_STEP")

	switch action {
	case actionUnlink, actionReimport:
		helperUnlink(t, dir, action == actionReimport)
	case "publish":
		publish(t, dir, importedEntry())
	case "updates":
		helperUpdates(t, dir)
	case actionCrash:
		dir.SetPublishHook(func(name string) error {
			if name == step {
				os.Exit(publishCrashExit)
			}

			return nil
		})
		publish(t, dir, importedEntry())
		t.Fatalf("publication didn't reach step %q", step)
	case "check":
		helperCheckCrash(t, dir, step)
	default:
		t.Fatalf("unknown helper action %q", action)
	}
}

// helperUnlink removes an account like `account unlink`, then optionally imports it again.
func helperUnlink(t *testing.T, dir *store.Dir, reimport bool) {
	t.Helper()

	aci := os.Getenv("GOSIGNAL_TEST_REGISTRY_ACI")
	if aci == "" {
		aci = secondACI
	}

	lock := stateValue(dir.Lock(aci))(t)
	stateCheck(t, dir.RemoveAccount(aci))
	stateCheck(t, lock.Unlock())

	if reimport {
		publish(t, dir, importedEntry())
	}
}

// helperUpdates adds a worker's entries and marks its first one unlinked.
func helperUpdates(t *testing.T, dir *store.Dir) {
	t.Helper()

	worker := stateValue(strconv.Atoi(os.Getenv("GOSIGNAL_TEST_REGISTRY_WORKER")))(t)
	for i := range updatesPerWorker {
		number := "+1555" + strconv.Itoa(1000+worker*100+i)
		putAccount(t, dir, store.AccountEntry{Number: number, ACI: workerACI(worker, i)})
	}

	stateCheck(t, dir.MarkUnlinked(workerACI(worker, 0), time.Unix(7, 0).UTC()))
}

// helperCheckCrash checks the data dir after a crash at step: published from the intent on,
// clean before.
func helperCheckCrash(t *testing.T, dir *store.Dir, step string) {
	t.Helper()

	if slices.Index(store.PublishSteps(), step) >= slices.Index(store.PublishSteps(), stepIntent) {
		wantPublished(t, dir, importedEntry())

		return
	}

	if got := stateValue(dir.Accounts())(t); len(got) != 0 {
		t.Errorf("accounts after a crash before the intent: %+v", got)
	}

	wantMissing(t, dir.AccountDir(secondACI))
	wantNoAttempts(t, dir)
}

func publish(t *testing.T, dir *store.Dir, entry store.AccountEntry) {
	t.Helper()

	im := stageImport(t, dir, entry)
	stateCheck(t, im.Publish(entry))
	stateCheck(t, im.Close())
}
