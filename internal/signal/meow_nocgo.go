//go:build !cgo && !libsignal_go

package signal

import "context"

// Backend is "none": without cgo and without the libsignal_go tag there is no Signal protocol
// implementation.
const Backend = "none"

// Open is unavailable without cgo.
func Open(context.Context, Options) (Client, error) {
	return nil, ErrCGORequired
}
