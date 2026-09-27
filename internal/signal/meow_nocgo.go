//go:build !cgo && !libsignal_go

package signal

import "context"

// Open is unavailable without cgo.
func Open(context.Context, Options) (Client, error) {
	return nil, ErrCGORequired
}
