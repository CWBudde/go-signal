//go:build cgo || libsignal_go

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

var (
	errImportState = errors.New("import attempt")
	errNotLoggedIn = errors.New("no logged-in device")
)

// Import is a private import attempt: an account built under .imports/<token>/ and then
// published into the data dir by Publish. The attempt holds its owner lock until Close, so that
// recovery in other processes leaves it alone.
type Import struct {
	d     *Dir
	token string
	stage string
	owner *os.File

	aci    string
	digest string
	// committed is set once the publication intent may be durable: from then on, Close leaves
	// the attempt to recovery instead of removing it.
	committed bool
}

// BeginImport allocates a private import attempt under the registry lock. It fails with
// ErrImportUnsupported on platforms that can't publish atomically.
func (d *Dir) BeginImport() (*Import, error) {
	if !importSupported {
		return nil, ErrImportUnsupported
	}

	var im *Import

	err := d.withRegistry(func(*registry) error {
		var err error

		im, err = d.allocateAttempt()

		return err
	})
	if err != nil {
		return nil, err
	}

	err = d.step(stepAllocated)
	if err != nil {
		return nil, errors.Join(err, im.Close())
	}

	return im, nil
}

// allocateAttempt creates .imports/<token>/ with its owner lock held.
func (d *Dir) allocateAttempt() (*Import, error) {
	parent := filepath.Join(d.path, importsDir)

	err := d.mkdir(parent)
	if err != nil {
		return nil, fmt.Errorf("create imports dir: %w", err)
	}

	token, err := newToken()
	if err != nil {
		return nil, err
	}

	stage := filepath.Join(parent, token)

	err = os.Mkdir(stage, dirPerm)
	if err != nil {
		return nil, fmt.Errorf("create import attempt: %w", err)
	}

	owner, err := os.OpenFile(filepath.Join(stage, ownerLockFile), os.O_RDWR|os.O_CREATE|os.O_EXCL, filePerm)
	if err == nil {
		err = tryLock(owner)
		if err == nil {
			err = errors.Join(syncDir(stage), syncDir(parent))
		}

		if err != nil {
			_ = owner.Close()
		}
	}

	if err != nil {
		_ = os.RemoveAll(stage)

		return nil, fmt.Errorf("create import attempt: %w", err)
	}

	return &Import{d: d, token: token, stage: stage, owner: owner}, nil
}

// Build creates the account aci in the attempt and fills its database with build. Then it
// checkpoints and closes the database, checks that it reopens without migrating and holds the
// logged-in device of aci, and makes it durable. No connection stays open.
func (im *Import) Build(ctx context.Context, aci string, build func(context.Context, *Store) error,
	log zerolog.Logger,
) error {
	if im.aci != "" {
		return fmt.Errorf("%w: account already built", errImportState)
	}

	parsed, err := uuid.Parse(aci)
	if err != nil || parsed.String() != aci {
		return fmt.Errorf("%w: invalid ACI %q", errImportState, aci)
	}

	dir := filepath.Join(im.stage, aci)

	err = os.Mkdir(dir, dirPerm)
	if err != nil {
		return fmt.Errorf("create staged account: %w", err)
	}

	path := filepath.Join(dir, dbFile)

	err = im.d.buildDB(ctx, path, build, log)
	if err == nil {
		err = checkBuilt(ctx, path, parsed, log)
	}

	if err == nil {
		err = syncStaged(dir, im.stage)
	}

	if err == nil {
		im.digest, err = fileDigest(path)
	}

	if err != nil {
		return err
	}

	im.aci = aci

	return im.d.step(stepBuilt)
}

// buildDB creates the database path, fills it with build, checkpoints and closes it.
func (d *Dir) buildDB(ctx context.Context, path string, build func(context.Context, *Store) error,
	log zerolog.Logger,
) error {
	err := d.touch(path)
	if err != nil {
		return err
	}

	opened, err := openDB(ctx, path, log)
	if err != nil {
		return err
	}

	err = build(ctx, opened)
	if err == nil {
		_, err = opened.db.Exec(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	}

	err = errors.Join(err, opened.Close())
	if err != nil {
		return fmt.Errorf("build staged account: %w", err)
	}

	return nil
}

// checkBuilt reopens the staged database path without migrating it and checks its device.
func checkBuilt(ctx context.Context, path string, aci uuid.UUID, log zerolog.Logger) error {
	opened, err := openCurrent(ctx, path, log)
	if err != nil {
		return fmt.Errorf("check staged account: %w", err)
	}

	device, err := opened.Devices.DeviceByACI(ctx, aci)
	if err == nil && (device == nil || !device.IsDeviceLoggedIn()) {
		err = errNotLoggedIn
	}

	err = errors.Join(err, opened.Close())
	if err != nil {
		return fmt.Errorf("check staged account: %w", err)
	}

	return nil
}

// syncStaged makes the staged account directory dir and its files durable.
func syncStaged(dir, stage string) error {
	for _, name := range []string{dbFile, dbFile + "-wal", dbFile + "-shm"} {
		err := syncFile(filepath.Join(dir, name))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}

	return errors.Join(syncDir(dir), syncDir(stage))
}

// Publish registers the built account as entry, which must have its ACI, under the registry
// lock: it refuses an existing entry with the ACI, number or PNI and anything at the account's
// path, then durably records the intent to publish, renames the account into place without
// replacing anything and appends entry to accounts.json. Once the intent may be durable, a
// failure is ErrRecoveryRequired: the next registry operation finishes the publication or
// reports what disagrees.
func (im *Import) Publish(entry AccountEntry) error {
	if im.aci == "" || entry.ACI != im.aci || im.committed {
		return fmt.Errorf("%w: publish %q, built %q, committed %t", errImportState, entry.ACI, im.aci, im.committed)
	}

	return im.d.withRegistry(func(reg *registry) error {
		err := reg.checkFree(entry)
		if err != nil {
			return err
		}

		err = writeDurable(filepath.Join(im.stage, entry.ACI, ownerMarker), []byte(im.token))
		if err == nil {
			err = im.d.step(stepMarked)
		}

		if err != nil {
			return err
		}

		err = im.commit(reg, entry)
		if err != nil {
			return fmt.Errorf("%w: %s: %w", ErrRecoveryRequired, im.stage, err)
		}

		return nil
	})
}

// checkFree refuses entry if it collides with a registry entry or anything at its path.
func (reg *registry) checkFree(entry AccountEntry) error {
	accounts, err := reg.accounts()
	if err != nil {
		return err
	}

	err = collision(accounts, entry)
	if err != nil {
		return err
	}

	final := reg.d.AccountDir(entry.ACI)

	_, err = os.Lstat(final)
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s exists (%w)", ErrImportCollision, final, err)
	}

	return nil
}

// Close ends the attempt. An attempt that never committed to publishing is removed; a
// committed one is left for recovery if Publish didn't finish it.
func (im *Import) Close() error {
	if im.owner == nil {
		return nil
	}

	var err error

	if !im.committed {
		err = os.RemoveAll(im.stage)
		if err == nil {
			err = syncDir(filepath.Dir(im.stage))
		}
	}

	err = errors.Join(err, im.owner.Close())
	im.owner = nil

	if err != nil {
		return fmt.Errorf("close import attempt: %w", err)
	}

	return nil
}

// commit durably records the intent to publish entry, then publishes it like recovery would.
func (im *Import) commit(reg *registry, entry AccountEntry) error {
	intent := importIntent{Version: intentVersion, Token: im.token, Entry: entry, Digest: im.digest}

	raw, err := json.Marshal(intent)
	if err != nil {
		return fmt.Errorf("encode import intent: %w", err)
	}

	im.committed = true

	err = writeDurable(filepath.Join(im.stage, intentFile), raw)
	if err == nil {
		err = im.d.step(stepIntent)
	}

	if err != nil {
		return err
	}

	return reg.rollForward(im.stage, intent)
}
