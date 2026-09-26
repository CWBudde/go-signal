package store

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	// dirPerm and filePerm keep keys and messages private to the user.
	dirPerm  = 0o700
	filePerm = 0o600

	// groupOtherPerm are the permission bits that must not be set on anything we own.
	groupOtherPerm = 0o077

	accountsFile = "accounts.json"
	dbFile       = "account.db"
	lockFile     = "lock"
	// attachmentsDir is where `mcp serve` saves attachments by default.
	attachmentsDir = "attachments"
	appDir         = "go-signal"
)

// DefaultDir returns $XDG_DATA_HOME/go-signal, falling back to ~/.local/share/go-signal.
func DefaultDir() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, appDir)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return appDir + "-data"
	}

	return filepath.Join(home, ".local", "share", appDir)
}

// Dir is an opened data directory.
type Dir struct {
	path string
	log  *slog.Logger
}

// OpenDir resolves path ("" means DefaultDir, a leading ~/ is expanded), creates it if needed
// and warns if it is accessible by other users.
func OpenDir(path string, log *slog.Logger) (*Dir, error) {
	path, err := resolve(path)
	if err != nil {
		return nil, err
	}

	dir := &Dir{path: path, log: log}

	err = dir.mkdir(path)
	if err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	return dir, nil
}

// Path returns the resolved data dir.
func (d *Dir) Path() string {
	return d.path
}

// AccountDir returns the directory holding the account with the given ACI.
func (d *Dir) AccountDir(aci string) string {
	return filepath.Join(d.path, aci)
}

// AttachmentsDir returns the default directory for the attachments that `mcp serve` downloads:
// "attachments" in the account's directory, with dataDir resolved as OpenDir does. It creates
// nothing; the attachments go when the account is unlinked.
func AttachmentsDir(dataDir, aci string) (string, error) {
	path, err := resolve(dataDir)
	if err != nil {
		return "", err
	}

	return filepath.Join(path, aci, attachmentsDir), nil
}

func resolve(path string) (string, error) {
	if path == "" {
		path = DefaultDir()
	}

	if rest, ok := strings.CutPrefix(path, "~"+string(filepath.Separator)); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand %s: %w", path, err)
		}

		path = filepath.Join(home, rest)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve data dir: %w", err)
	}

	return abs, nil
}

// mkdir creates path with dirPerm, or checks the permissions of an existing directory.
func (d *Dir) mkdir(path string) error {
	err := os.MkdirAll(path, dirPerm)
	if err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}

	d.checkPerm(path, info)

	return nil
}

// touch creates path with filePerm if it doesn't exist, or checks its permissions if it does.
// SQLite creates its -wal and -shm files with the database file's mode, so pre-creating the
// database this way covers them too.
func (d *Dir) touch(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, filePerm)
	if err != nil {
		return fmt.Errorf("create %s: %w", filepath.Base(path), err)
	}

	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", filepath.Base(path), err)
	}

	d.checkPerm(path, info)

	return nil
}

func (d *Dir) checkPerm(path string, info fs.FileInfo) {
	perm := info.Mode().Perm()
	if perm&groupOtherPerm == 0 {
		return
	}

	want := fs.FileMode(filePerm)
	if info.IsDir() {
		want = dirPerm
	}

	d.log.Warn("accessible by other users; consider chmod "+fmt.Sprintf("%o", want),
		"path", path, "mode", fmt.Sprintf("%o", perm))
}
