package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// registryLockFile is the data dir's registry lock, separate from accounts.json so that the
	// locked file is never the one being replaced.
	registryLockFile = "accounts.lock"
	// importsDir holds private import attempts, removedDir the directories of removed accounts
	// until they are deleted.
	importsDir = ".imports"
	removedDir = ".removed"

	// registryWait bounds how long a registry operation retries a held registry lock. Holders
	// never wait for another lock, so this only covers ordinary contention.
	registryWait  = 2 * time.Second
	registryRetry = 10 * time.Millisecond

	tokenBytes = 16
)

var (
	// ErrRegistryBusy means another process kept the registry lock for longer than registryWait.
	ErrRegistryBusy = errors.New("account registry busy")
	// ErrAccountMissing means a registered account's directory, lock file or database is gone.
	ErrAccountMissing = errors.New("registered account data is missing")
	// ErrAccountChanged means the selected account was removed or replaced since it was opened.
	ErrAccountChanged = errors.New("account changed since it was opened; run the command again")
	// ErrRecoveryRequired means an interrupted import can't be finished automatically.
	ErrRecoveryRequired = errors.New("interrupted account import needs recovery")
	// ErrImportUnsupported means this platform lacks what publishing an import needs.
	ErrImportUnsupported = errors.New("account import is not supported on this platform")
)

// registry is the registry while its lock is held. Its methods never take the lock again.
type registry struct {
	d *Dir
}

// withRegistry runs locked holding the registry lock, after finishing or cleaning up interrupted
// imports and removals (see recover). It never waits for an account lock.
func (d *Dir) withRegistry(locked func(*registry) error) error {
	path := filepath.Join(d.path, registryLockFile)

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, filePerm)
	if err != nil {
		return fmt.Errorf("open registry lock: %w", err)
	}

	defer func() { _ = file.Close() }()

	deadline := time.Now().Add(registryWait)

	for {
		err = tryLock(file)
		if err == nil {
			break
		}

		if !errors.Is(err, errWouldBlock) {
			return fmt.Errorf("lock registry: %w", err)
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("%w (%s)", ErrRegistryBusy, path)
		}

		time.Sleep(registryRetry)
	}

	reg := &registry{d: d}

	err = reg.recover()
	if err != nil {
		return err
	}

	return locked(reg)
}

// syncDir makes the entries of the directory path durable.
func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}

	err = dir.Sync()

	closeErr := dir.Close()
	if err == nil {
		err = closeErr
	}

	if err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}

	return nil
}

// syncFile makes the contents of the file path durable.
func syncFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}

	err = file.Sync()

	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}

	if err != nil {
		return fmt.Errorf("sync %s: %w", path, err)
	}

	return nil
}

// newToken returns a random name for an import attempt or a removed directory.
func newToken() (string, error) {
	raw := make([]byte, tokenBytes)

	_, err := rand.Read(raw)
	if err != nil {
		return "", fmt.Errorf("random token: %w", err)
	}

	return hex.EncodeToString(raw), nil
}

// isToken reports whether name is a name newToken returns.
func isToken(name string) bool {
	raw, err := hex.DecodeString(name)

	return err == nil && len(raw) == tokenBytes && hex.EncodeToString(raw) == name
}
