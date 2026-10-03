package app

import (
	"context"
	"fmt"

	"github.com/cwbudde/go-signal/internal/signal"
)

// GroupCancelRequestRequest identifies a group already known to the selected account.
type GroupCancelRequestRequest struct {
	Group string
}

// Check validates the sensitive reference without opening an account or connecting.
func (r GroupCancelRequestRequest) Check() error {
	return signal.CheckGroupCancelRequestReference(r.Group) //nolint:wrapcheck // shared validation is already secret-free
}

type groupCancelRequestPreSubmissionError struct {
	stage string
	cause error
}

func (e groupCancelRequestPreSubmissionError) Error() string {
	return "groups cancel-request: " + e.stage + " failed before submission (details hidden to protect group secrets)"
}

func (e groupCancelRequestPreSubmissionError) Unwrap() error { return e.cause }

// GroupsCancelRequest resolves locally, connects once in send-only mode and cancels once.
// Partial results preserve HTTP acceptance and exact signed deletion verification on errors.
func (a *App) GroupsCancelRequest(
	ctx context.Context, req GroupCancelRequestRequest,
) (signal.GroupCancelRequestResult, error) {
	result := signal.GroupCancelRequestResult{}

	err := req.Check()
	if err != nil {
		return result, fmt.Errorf("groups cancel-request: %w", err)
	}

	ref, err := a.ResolveGroup(ctx, req.Group)
	if err != nil {
		return result, groupCancelRequestPreSubmissionError{stage: "resolution", cause: err}
	}

	err = a.connectSendOnly(ctx)
	if err != nil {
		return result, groupCancelRequestPreSubmissionError{stage: "connect", cause: err}
	}

	result, err = a.client.CancelGroupJoinRequest(ctx, ref)
	if err != nil {
		return result, fmt.Errorf("groups cancel-request: %w", signal.GroupCancelRequestOperationError(err, result))
	}

	return result, nil
}
