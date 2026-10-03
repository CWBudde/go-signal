package app

import (
	"context"
	"fmt"

	"github.com/cwbudde/go-signal/internal/signal"
)

// GroupAcceptRequest identifies an invitation already known to the selected account.
type GroupAcceptRequest struct {
	Group string
}

// Check validates the sensitive reference without opening an account or connecting.
func (r GroupAcceptRequest) Check() error {
	return signal.CheckGroupAcceptReference(r.Group) //nolint:wrapcheck // shared validation is already secret-free
}

type groupAcceptPreSubmissionError struct {
	stage string
	cause error
}

func (e groupAcceptPreSubmissionError) Error() string {
	return "groups accept: " + e.stage + " failed before submission (details hidden to protect group secrets)"
}

func (e groupAcceptPreSubmissionError) Unwrap() error { return e.cause }

// GroupsAccept resolves locally, connects once in send-only mode and accepts once.
// Partial results preserve HTTP acceptance and fresh membership verification on errors.
func (a *App) GroupsAccept(ctx context.Context, req GroupAcceptRequest) (signal.GroupAcceptResult, error) {
	err := req.Check()
	if err != nil {
		return signal.GroupAcceptResult{}, fmt.Errorf("groups accept: %w", err)
	}

	ref, err := a.ResolveGroup(ctx, req.Group)
	if err != nil {
		return signal.GroupAcceptResult{}, groupAcceptPreSubmissionError{stage: "resolution", cause: err}
	}

	err = a.connectSendOnly(ctx)
	if err != nil {
		return signal.GroupAcceptResult{}, groupAcceptPreSubmissionError{stage: "connect", cause: err}
	}

	result, err := a.client.AcceptGroupInvitation(ctx, ref)
	if err != nil {
		return result, fmt.Errorf("groups accept: %w", signal.GroupAcceptOperationError(err, result))
	}

	return result, nil
}
