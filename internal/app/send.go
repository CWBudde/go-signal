package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
)

var (
	// ErrEmptyMessage means that the message body is empty or only white space.
	ErrEmptyMessage = errors.New("message is empty")
	// ErrNoRecipients means that a send names no recipient.
	ErrNoRecipients = errors.New("no recipients")
	// ErrSendFailed means that sending to at least one recipient (or group member) failed. The
	// SendResult that comes with it is complete.
	ErrSendFailed = errors.New("sending failed")

	errNoResult = errors.New("no result from the client")
)

// SendRequest is the input of Send.
type SendRequest struct {
	// Recipients are recipient arguments (see ParseRecipient): users, group:<id> and self.
	Recipients []string
	Body       string
}

// SendResult is the output of Send.
type SendResult struct {
	// Timestamp is the sent timestamp (ms since epoch) of the message; it is the same for all
	// recipients and, together with our ACI, identifies the message.
	Timestamp uint64
	// Results has one entry per target, in the order of the arguments (without duplicates).
	Results []TargetResult
}

// Failed returns the number of targets that didn't get the message (completely).
func (r SendResult) Failed() int {
	failed := 0

	for _, res := range r.Results {
		if !res.OK() {
			failed++
		}
	}

	return failed
}

// TargetResult is the outcome of sending to one target.
type TargetResult struct {
	Target Target
	// Unidentified reports that a user got the message with sealed sender.
	Unidentified bool
	// Members are the results per group member (without us) of a group target.
	Members []signal.RecipientResult
	// Err is set when sending to the target failed as a whole.
	Err error
}

// OK reports whether the target got the message: no error, and every group member got it.
func (r TargetResult) OK() bool {
	return r.Err == nil && r.FailedMembers() == 0
}

// FailedMembers returns the number of group members that didn't get the message.
func (r TargetResult) FailedMembers() int {
	failed := 0

	for _, member := range r.Members {
		if member.Err != nil {
			failed++
		}
	}

	return failed
}

// Send sends a text message to every recipient in req with the same timestamp. It connects the
// client in send-only mode (signal.SendOnly), so the client must not be connected yet: incoming
// messages stay on the server for the next receive. Invalid arguments fail before connecting,
// and recipients that can't be resolved (e.g. signal.ErrNotOnSignal) fail before anything is
// sent. A note-to-self (self, or our own number or ACI) only goes to our other devices; every
// other message is also synced to them.
//
// If sending fails for some targets, Send returns the complete result together with an error
// wrapping ErrSendFailed (and signal.ErrDeviceUnlinked, if that was the cause).
func (a *App) Send(ctx context.Context, req SendRequest) (SendResult, error) {
	if strings.TrimSpace(req.Body) == "" {
		return SendResult{}, fmt.Errorf("send: %w", ErrEmptyMessage)
	}

	err := checkRecipients(req.Recipients)
	if err != nil {
		return SendResult{}, fmt.Errorf("send: %w", err)
	}

	err = a.client.Connect(ctx, signal.SendOnly())
	if err != nil {
		return SendResult{}, fmt.Errorf("send: connect: %w", err)
	}

	targets, err := a.ResolveRecipients(ctx, req.Recipients)
	if err != nil {
		return SendResult{}, fmt.Errorf("send: %w", err)
	}

	res := SendResult{
		Timestamp: uint64(a.now().UnixMilli()), //nolint:gosec // the clock is after 1970
		Results:   make([]TargetResult, len(targets)),
	}

	for i, target := range targets {
		res.Results[i].Target = target
	}

	a.sendToUsers(ctx, req.Body, &res)
	a.sendToGroups(ctx, req.Body, &res)

	return res, res.err()
}

// checkRecipients parses args (see ParseRecipient) without resolving them.
func checkRecipients(args []string) error {
	if len(args) == 0 {
		return ErrNoRecipients
	}

	var errs []error

	for _, arg := range args {
		_, err := ParseRecipient(arg)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// sendToUsers sends body to all users (and self) among res.Results in one request.
func (a *App) sendToUsers(ctx context.Context, body string, res *SendResult) {
	var (
		users   []signal.Recipient
		indices []int
	)

	for i, result := range res.Results {
		if !result.Target.IsGroup() {
			users = append(users, result.Target.Recipient)
			indices = append(indices, i)
		}
	}

	if len(users) == 0 {
		return
	}

	sent, err := a.client.Send(ctx, signal.SendRequest{Recipients: users, Body: body, Timestamp: res.Timestamp})

	for n, i := range indices {
		result := &res.Results[i]

		switch {
		case err != nil:
			result.Err = err
		case n < len(sent.Results):
			result.Unidentified = sent.Results[n].Unidentified
			result.Err = sent.Results[n].Err
		default:
			result.Err = errNoResult
		}
	}
}

// sendToGroups sends body to each group among res.Results.
func (a *App) sendToGroups(ctx context.Context, body string, res *SendResult) {
	for i := range res.Results {
		result := &res.Results[i]
		if !result.Target.IsGroup() {
			continue
		}

		sent, err := a.client.Send(ctx, signal.SendRequest{
			GroupID: result.Target.GroupID, Body: body, Timestamp: res.Timestamp,
		})
		if err != nil {
			result.Err = err

			continue
		}

		result.Members = sent.Results
	}
}

// err returns the error for a result with failed targets, or nil.
func (r SendResult) err() error {
	failed := r.Failed()
	if failed == 0 {
		return nil
	}

	err := fmt.Errorf("send: %w for %d of %d recipients", ErrSendFailed, failed, len(r.Results))

	for _, res := range r.Results {
		// Keep the cause that decides the exit code.
		if errors.Is(res.Err, signal.ErrDeviceUnlinked) {
			return fmt.Errorf("%w: %w", err, res.Err)
		}
	}

	return err
}
