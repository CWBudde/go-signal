//go:build !cgo

package signal

import "context"

// Link is unavailable without cgo.
func Link(context.Context, string, string, func(string)) (*Account, error) {
	return nil, ErrCGORequired
}

// Receive is unavailable without cgo.
func Receive(context.Context, string, func(any)) error {
	return ErrCGORequired
}
