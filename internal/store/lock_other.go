//go:build !unix

package store

import (
	"errors"
	"os"
)

// errWouldBlock is never returned: without flock, locking is best effort (always succeeds).
var errWouldBlock = errors.New("would block")

func tryLock(*os.File) error {
	return nil
}
