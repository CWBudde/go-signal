//go:build !cgo && !purego

package signal

import "context"

// Open is unavailable without cgo.
func Open(context.Context, Options) (Client, error) {
	return nil, ErrCGORequired
}
