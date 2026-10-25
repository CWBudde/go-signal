//go:build cgo || libsignal_go

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/store/upgrades"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
)

// OpenMode says how OpenRegistered opens an account.
type OpenMode int

const (
	// OpenSnapshot opens the account for reading and for go-signal's own tables, without its
	// lock: another process may be connected as the account. It never creates or migrates;
	// a database that needs migrating is migrated only if the account lock is free.
	OpenSnapshot OpenMode = iota
	// OpenConnected takes the account lock and keeps it, then migrates the database.
	OpenConnected
)

// Generation identifies the files of an opened account, so that a later open can tell whether
// the account was removed or replaced in between. The zero value matches any account.
type Generation struct {
	dir, db fs.FileInfo
}

// matches reports whether other has the files of g, or g is zero.
func (g Generation) matches(other Generation) bool {
	return g.dir == nil || (os.SameFile(g.dir, other.dir) && os.SameFile(g.db, other.db))
}

// Opened is an account opened by OpenRegistered.
type Opened struct {
	Entry AccountEntry
	Store *Store
	// Lock is the held account lock (OpenConnected only).
	Lock       *Lock
	Generation Generation
}

// OpenRegistered selects a registered account with choose and opens it, in one step under the
// registry lock: the entry, the account directory, its lock file and database are checked
// together, so that a concurrent unlink, relink or import can't make it open, recreate or lock
// anything else. A missing directory or database is ErrAccountMissing; nothing is created
// except a missing lock file. If expect isn't zero, an account with other files is
// ErrAccountChanged.
func (d *Dir) OpenRegistered(ctx context.Context, choose func([]AccountEntry) (AccountEntry, error),
	mode OpenMode, expect Generation, log zerolog.Logger,
) (*Opened, error) {
	var (
		opened *Opened
		lock   *os.File
	)

	err := d.withRegistry(func(reg *registry) error {
		var err error

		opened, lock, err = d.selectRegistered(reg, choose, mode, expect)
		if err != nil || lock != nil {
			return err
		}

		// Another process holds the account: open it now, while it can't be removed.
		opened.Store, err = openCurrent(ctx, d.dbPath(opened.Entry.ACI), log)

		return err
	})
	if err != nil {
		return nil, err
	}

	if lock == nil {
		return opened, nil
	}

	// The account lock is held, so nothing can remove the account meanwhile.
	opened.Store, err = openDB(ctx, d.dbPath(opened.Entry.ACI), log)
	if err != nil {
		_ = lock.Close()

		return nil, fmt.Errorf("open account: %w", err)
	}

	if mode == OpenConnected {
		opened.Lock = &Lock{file: lock}
	} else {
		_ = lock.Close()
	}

	return opened, nil
}

// selectRegistered chooses the entry, checks its files against expect and tries its lock (see
// lockRegistered), all under the registry lock.
func (d *Dir) selectRegistered(reg *registry, choose func([]AccountEntry) (AccountEntry, error),
	mode OpenMode, expect Generation,
) (*Opened, *os.File, error) {
	accounts, err := reg.accounts()
	if err != nil {
		return nil, nil, err
	}

	entry, err := choose(accounts)
	if err != nil {
		return nil, nil, err
	}

	generation, err := d.generation(entry.ACI)
	if err != nil {
		return nil, nil, err
	}

	if !expect.matches(generation) {
		return nil, nil, fmt.Errorf("%w: %s", ErrAccountChanged, entry.Number)
	}

	lock, err := d.lockRegistered(entry.ACI, mode)
	if err != nil && !errors.Is(err, errWouldBlock) {
		return nil, nil, err
	}

	return &Opened{Entry: entry, Generation: generation}, lock, nil
}

// dbPath is the database of the account aci.
func (d *Dir) dbPath(aci string) string {
	return filepath.Join(d.AccountDir(aci), dbFile)
}

// generation returns the files of the registered account aci; a missing directory or database
// is ErrAccountMissing.
func (d *Dir) generation(aci string) (Generation, error) {
	dir := d.AccountDir(aci)

	dirInfo, err := os.Lstat(dir)
	if err == nil && !dirInfo.IsDir() {
		err = fs.ErrInvalid
	}

	if err != nil {
		return Generation{}, fmt.Errorf("%w: %s: %w", ErrAccountMissing, dir, err)
	}

	path := d.dbPath(aci)

	dbInfo, err := os.Lstat(path)
	if err == nil && !dbInfo.Mode().IsRegular() {
		err = fs.ErrInvalid
	}

	if err != nil {
		return Generation{}, fmt.Errorf("%w: %s: %w", ErrAccountMissing, path, err)
	}

	return Generation{dir: dirInfo, db: dbInfo}, nil
}

// lockRegistered tries the lock of the registered account aci without waiting and returns the
// locked file. If another process holds it, OpenConnected fails with ErrAccountInUse and
// OpenSnapshot with errWouldBlock.
func (d *Dir) lockRegistered(aci string, mode OpenMode) (*os.File, error) {
	path := filepath.Join(d.AccountDir(aci), lockFile)

	// Accounts seeded without ever connecting may have no lock file yet; the directory exists.
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, filePerm)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	err = tryLock(file)
	if errors.Is(err, errWouldBlock) {
		_ = file.Close()

		if mode == OpenConnected {
			return nil, fmt.Errorf("%w%s", ErrAccountInUse, holder(path))
		}

		return nil, errWouldBlock
	}

	if err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("lock %s: %w", path, err)
	}

	// The locked file must be the one at path, not one removed meanwhile.
	held, err := file.Stat()
	if err == nil {
		var current fs.FileInfo

		current, err = os.Stat(path)
		if err == nil && !os.SameFile(held, current) {
			err = ErrAccountChanged
		}
	}

	if err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("lock %s: %w", path, err)
	}

	if file.Truncate(0) == nil {
		_, _ = file.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}

	return file, nil
}

// openCurrent opens the database path without migrating it: one that needs migrating is
// ErrAccountInUse, since the process holding the account must do that.
func openCurrent(ctx context.Context, path string, log zerolog.Logger) (*Store, error) {
	sqlDB, err := dbutil.NewWithDialect(sqliteDSN(path), sqliteDriver)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	err = checkCurrent(ctx, sqlDB)
	if err != nil {
		_ = sqlDB.Close()

		return nil, err
	}

	return newStore(sqlDB, log), nil
}

// checkCurrent fails unless both version tables of sqlDB are at this build's latest versions.
func checkCurrent(ctx context.Context, sqlDB *dbutil.Database) error {
	for _, table := range []struct {
		name   string
		latest int
	}{
		{"signalmeow_version", len(upgrades.Table)},
		{ourVersionTable, len(upgradeTable())},
	} {
		var version int

		err := sqlDB.QueryRow(ctx, "SELECT version FROM "+table.name+" LIMIT 1").Scan(&version)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: read %s: %w", ErrAccountInUse, table.name, err)
		}

		// A newer, still compatible version is checked when the store is used.
		if version < table.latest {
			return fmt.Errorf("%w: its database (%s v%d) must first be upgraded to v%d by the process using it, "+
				"or once that has stopped", ErrAccountInUse, table.name, version, table.latest)
		}
	}

	return nil
}
