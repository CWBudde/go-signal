//go:build linux

package store

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// importSupported reports whether this platform can publish imports (see renameNoReplace).
const importSupported = true

// renameNoReplace renames oldpath to newpath atomically, failing instead of replacing anything
// at newpath.
func renameNoReplace(oldpath, newpath string) error {
	err := unix.Renameat2(unix.AT_FDCWD, oldpath, unix.AT_FDCWD, newpath, unix.RENAME_NOREPLACE)
	if err != nil {
		return fmt.Errorf("rename %s to %s: %w", oldpath, newpath, err)
	}

	return nil
}
