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
	// Body is the message text. @{<recipient>} placeholders for users (see ParseRecipient)
	// become mentions. It may be empty when there are attachments.
	Body string
	// Attachments are paths of files to attach, each at most MaxAttachmentSize.
	Attachments []string
	// AttachDir, if set, confines Attachments to this directory: their paths are relative to it,
	// and files outside it (also through symlinks) fail with ErrOutsideAttachDir.
	AttachDir string
	// Quote makes the message a reply to the message <author>:<timestamp> (see ParseQuote).
	Quote string
	// QuoteText is the quoted text that clients show when they don't have the quoted message.
	QuoteText string
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

// Send sends a message to every recipient in req with the same timestamp. It connects the
// client in send-only mode (signal.SendOnly) unless it is connected already: incoming
// messages stay on the server for the next receive. Invalid arguments and unreadable or too
// large attachments fail before connecting; recipients, mentioned users or a quote author that
// can't be resolved (e.g. signal.ErrNotOnSignal) fail before anything is uploaded or sent. The
// attachments are uploaded once for all recipients. A note-to-self (self, or our own number or
// ACI) only goes to our other devices; every other message is also synced to them.
//
// If sending fails for some targets, Send returns the complete result together with an error
// wrapping ErrSendFailed (and signal.ErrDeviceUnlinked, if that was the cause).
func (a *App) Send(ctx context.Context, req SendRequest) (SendResult, error) {
	req, files, err := prepare(req)
	if err != nil {
		return SendResult{}, fmt.Errorf("send: %w", err)
	}

	return a.sendContent(ctx, "send", req.Recipients, func(ctx context.Context) (content, error) {
		return a.buildContent(ctx, req, files)
	})
}

// sendContent runs action (send, react, delete): it connects in send-only mode, resolves the
// recipient arguments, checks them against the allowlist (see WithAllowlist), builds the message
// with build (after resolving, so that it can resolve users and upload) and sends it to every
// target with one timestamp.
func (a *App) sendContent(
	ctx context.Context, action string, recipients []string, build func(context.Context) (content, error),
) (SendResult, error) {
	err := a.connectSendOnly(ctx)
	if err != nil {
		return SendResult{}, fmt.Errorf("%s: connect: %w", action, err)
	}

	targets, err := a.ResolveRecipients(ctx, recipients)
	if err != nil {
		return SendResult{}, fmt.Errorf("%s: %w", action, err)
	}

	err = a.checkAllowed(ctx, targets)
	if err != nil {
		return SendResult{}, fmt.Errorf("%s: %w", action, err)
	}

	msg, err := build(ctx)
	if err != nil {
		return SendResult{}, fmt.Errorf("%s: %w", action, err)
	}

	res := SendResult{
		Timestamp: uint64(a.now().UnixMilli()), //nolint:gosec // the clock is after 1970
		Results:   make([]TargetResult, len(targets)),
	}

	for i, target := range targets {
		res.Results[i].Target = target
	}

	a.sendToUsers(ctx, msg, &res)
	a.sendToGroups(ctx, msg, &res)

	return res, res.err(action)
}

// prepare checks req without connecting and reads its attachments. An empty body is fine with
// attachments; one of only white space is dropped then.
func prepare(req SendRequest) (SendRequest, []signal.OutgoingAttachment, error) {
	if strings.TrimSpace(req.Body) == "" {
		if len(req.Attachments) == 0 {
			return req, nil, ErrEmptyMessage
		}

		req.Body = ""
	}

	err := checkRequest(req)
	if err != nil {
		return req, nil, err
	}

	files, err := loadAttachments(req.AttachDir, req.Attachments)
	if err != nil {
		return req, nil, err
	}

	return req, files, nil
}

// checkRecipients checks recipient arguments without resolving them: there is at least one, and
// each is valid.
func checkRecipients(args []string) []error {
	if len(args) == 0 {
		return []error{ErrNoRecipients}
	}

	var errs []error

	for _, arg := range args {
		_, err := ParseRecipient(arg)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errs
}

// checkRequest checks the recipient and quote arguments of req without resolving them.
func checkRequest(req SendRequest) error {
	if len(req.Recipients) == 0 {
		return ErrNoRecipients
	}

	errs := checkRecipients(req.Recipients)

	if req.Quote != "" {
		_, _, err := ParseQuote(req.Quote)
		if err != nil {
			errs = append(errs, err)
		}
	} else if req.QuoteText != "" {
		errs = append(errs, fmt.Errorf("%w: quote text without a quote", ErrInvalidQuote))
	}

	return errors.Join(errs...)
}

// request returns the signal.SendRequest of msg with timestamp.
func (msg content) request(timestamp uint64) signal.SendRequest {
	return signal.SendRequest{
		Body:         msg.body,
		Timestamp:    timestamp,
		Attachments:  msg.attachments,
		Quote:        msg.quote,
		Mentions:     msg.mentions,
		Reaction:     msg.reaction,
		DeleteTarget: msg.deleteTarget,
	}
}

// sendToUsers sends msg to all users (and self) among res.Results in one request.
func (a *App) sendToUsers(ctx context.Context, msg content, res *SendResult) {
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

	req := msg.request(res.Timestamp)
	req.Recipients = users

	sent, err := a.client.Send(ctx, req)

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

// sendToGroups sends msg to each group among res.Results.
func (a *App) sendToGroups(ctx context.Context, msg content, res *SendResult) {
	for i := range res.Results {
		result := &res.Results[i]
		if !result.Target.IsGroup() {
			continue
		}

		req := msg.request(res.Timestamp)
		req.GroupID = result.Target.GroupID

		sent, err := a.client.Send(ctx, req)
		if err != nil {
			result.Err = err

			continue
		}

		result.Members = sent.Results
	}
}

// err returns the error of action (send, react, delete) for a result with failed targets, or
// nil.
func (r SendResult) err(action string) error {
	failed := r.Failed()
	if failed == 0 {
		return nil
	}

	err := fmt.Errorf("%s: %w for %d of %d recipients", action, ErrSendFailed, failed, len(r.Results))

	for _, res := range r.Results {
		// Keep the cause that decides the exit code.
		if errors.Is(res.Err, signal.ErrDeviceUnlinked) {
			return fmt.Errorf("%w: %w", err, res.Err)
		}
	}

	return err
}
