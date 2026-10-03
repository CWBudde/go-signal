package app

import (
	"context"
	"fmt"

	"github.com/cwbudde/go-signal/internal/signal"
)

// JoinGroupRequest contains a sensitive invite link, never a group title or ID.
type JoinGroupRequest struct {
	Link string
}

// Check validates the sensitive input without opening an account or connecting.
func (r JoinGroupRequest) Check() error {
	return signal.CheckGroupInviteLink(r.Link) //nolint:wrapcheck // validation is already secret-free
}

// groupJoinConnectError keeps transport identity without interpreting it as a
// membership outcome: failure to connect occurs before the join is submitted.
type groupJoinConnectError struct {
	cause error
}

func (e groupJoinConnectError) Error() string {
	return "groups join: connect failed before submission (details hidden to protect invite secrets)"
}

func (e groupJoinConnectError) Unwrap() error { return e.cause }

// GroupsJoin joins or requests membership once in send-only mode. Partial results
// survive errors; acceptance and verification are independent from the presence of an ID.
func (a *App) GroupsJoin(ctx context.Context, req JoinGroupRequest) (signal.GroupJoinResult, error) {
	err := req.Check()
	if err != nil {
		return signal.GroupJoinResult{}, fmt.Errorf("groups join: %w", err)
	}

	err = a.connectSendOnly(ctx)
	if err != nil {
		return signal.GroupJoinResult{}, groupJoinConnectError{cause: err}
	}

	result, err := a.client.JoinGroup(ctx, req.Link)
	if err != nil {
		return result, fmt.Errorf("groups join: %w", signal.GroupJoinOperationError(err, result))
	}

	return result, nil
}
