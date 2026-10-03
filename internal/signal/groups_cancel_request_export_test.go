//go:build cgo || libsignal_go

package signal

import "context"

// GroupCancelRequestOperations exposes isolated cancellation dependencies.
type GroupCancelRequestOperations = groupCancelRequestOperations

// CancelGroupRequestWithOperations runs the cancellation policy with offline dependencies.
func CancelGroupRequestWithOperations(ctx context.Context, ops GroupCancelRequestOperations, ref string,
) (GroupCancelRequestResult, error) {
	return cancelGroupRequestWithOperations(ctx, ops, ref)
}
