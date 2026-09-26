package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrAccountInUse means another process holds the account's lock.
var ErrAccountInUse = errors.New("account in use by another go-signal process")

// Lock is an exclusive per-account lock. It is released by Unlock or when the process exits.
type Lock struct {
	file *os.File
}

// Lock takes the lock of the account with the given ACI without blocking. It fails with
// ErrAccountInUse if another process (or another Lock in this process) holds it.
func (d *Dir) Lock(aci string) (*Lock, error) {
	dir := d.AccountDir(aci)

	err := d.mkdir(dir)
	if err != nil {
		return nil, fmt.Errorf("create account dir: %w", err)
	}

	path := filepath.Join(dir, lockFile)

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, filePerm)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	err = tryLock(file)
	if err != nil {
		_ = file.Close()

		if errors.Is(err, errWouldBlock) {
			return nil, fmt.Errorf("%w%s", ErrAccountInUse, holder(path))
		}

		return nil, fmt.Errorf("lock %s: %w", path, err)
	}

	// Record our PID for the error message of the next contender; failures don't matter.
	if file.Truncate(0) == nil {
		_, _ = file.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}

	return &Lock{file: file}, nil
}

// Probe reports whether the lock of the account with the given ACI is free, without keeping it:
// it fails with ErrAccountInUse if another process (or a Lock in this process) holds it. An
// account without a lock file is free.
func (d *Dir) Probe(aci string) error {
	path := filepath.Join(d.AccountDir(aci), lockFile)

	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	// Closing releases the lock, if we got it.
	defer func() { _ = file.Close() }()

	err = tryLock(file)
	if errors.Is(err, errWouldBlock) {
		return fmt.Errorf("%w%s", ErrAccountInUse, holder(path))
	}

	if err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}

	return nil
}

// Unlock releases the lock. The lock file stays so that the next Lock doesn't race a removal.
func (l *Lock) Unlock() error {
	err := l.file.Close()
	if err != nil {
		return fmt.Errorf("release lock: %w", err)
	}

	return nil
}

// holder describes the PID recorded in the lock file, if any.
func holder(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	pid := strings.TrimSpace(string(raw))
	if pid == "" {
		return ""
	}

	return " (pid " + pid + ")"
}
