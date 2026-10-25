//go:build !linux

package store

import "fmt"

// importSupported is false: publishing needs an atomic no-replace directory rename, which is
// only implemented for Linux (renameat2).
const importSupported = false

func renameNoReplace(oldpath, newpath string) error {
	return fmt.Errorf("%w: rename %s to %s", ErrImportUnsupported, oldpath, newpath)
}
