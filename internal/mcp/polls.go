package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type pollCreateInput struct {
	GroupID      string   `json:"groupId,omitempty"      jsonschema:"canonical group ID; supply this or recipient"`
	Recipient    string   `json:"recipient,omitempty"    jsonschema:"user: number, ACI, @username or self; or groupId"`
	Question     string   `json:"question"               jsonschema:"poll question"`
	Options      []string `json:"options"                jsonschema:"two to ten poll options"`
	SingleChoice bool     `json:"singleChoice,omitempty" jsonschema:"allow only one selection instead of multiple"`
}
type pollVoteInput struct {
	GroupID       string   `json:"groupId,omitempty"       jsonschema:"canonical group ID; supply this or recipient"`
	Recipient     string   `json:"recipient,omitempty"     jsonschema:"user: number, ACI, @username or self; or groupId"`
	Target        string   `json:"target"                  jsonschema:"creator:timestamp (ACI, number, username, self)"`
	OptionIndexes []uint32 `json:"optionIndexes,omitempty" jsonschema:"distinct zero-based selections; omit with clear"`
	VoteCount     *uint32  `json:"voteCount,omitempty"     jsonschema:"positive counter override; omit to allocate"`
	Clear         bool     `json:"clear,omitempty"         jsonschema:"withdraw all selections"`
}
type pollCloseInput struct {
	GroupID   string `json:"groupId,omitempty"   jsonschema:"canonical group ID; supply this or recipient"`
	Recipient string `json:"recipient,omitempty" jsonschema:"user: number, ACI, @username or self; or groupId"`
	Timestamp uint64 `json:"timestamp"           jsonschema:"creation timestamp of our own poll"`
}
type pollShowInput struct {
	GroupID   string `json:"groupId,omitempty"   jsonschema:"canonical group ID; supply this or recipient"`
	Recipient string `json:"recipient,omitempty" jsonschema:"canonical direct chat ACI; supply this or groupId"`
	Target    string `json:"target"              jsonschema:"canonical poll creator ACI:creation-timestamp"`
	Durable   bool   `json:"durable,omitempty"   jsonschema:"read durable account-local observations"`
	ScanLimit *int   `json:"scanLimit,omitempty" jsonschema:"inbox scan size (1 to 10000); omit with durable"`
}

func addPollTools(server *sdk.Server, handlers *tools) {
	sdk.AddTool(server, &sdk.Tool{
		Name: "poll_show", Title: "Show poll observations",
		Description: "Read retained poll observations without receipts. Default is bounded inbox history; " +
			"durable reads account-local materialized observations. Completeness remains unknown, " +
			"and outgoing submissions do not establish observed state.", Annotations: readOnly(),
	}, handlers.pollShow)

	if handlers.readOnly {
		return
	}

	annotations := &sdk.ToolAnnotations{DestructiveHint: new(true), OpenWorldHint: new(true)}
	sdk.AddTool(server, &sdk.Tool{
		Name: "poll_create", Title: "Create poll",
		Description: "Create a poll in one group or direct chat. Returns its creator, creation timestamp " +
			"and delivery outcomes." + allowlistNote, Annotations: annotations,
	}, handlers.pollCreate)
	sdk.AddTool(server, &sdk.Tool{
		Name: "poll_vote", Title: "Vote on poll",
		Description: "Replace selections or explicitly withdraw them. Omit voteCount for a durable local counter; " +
			"coordinate unseen other-device votes. Failed submissions consume allocated counters. " +
			"Returns delivery outcomes." + allowlistNote, Annotations: annotations,
	}, handlers.pollVote)
	sdk.AddTool(server, &sdk.Tool{
		Name: "poll_close", Title: "Close own poll",
		Description: "Close a poll created by this account. Successful submission does not prove clients " +
			"applied the closure." + allowlistNote, Annotations: annotations,
	}, handlers.pollClose)
}

//nolint:wrapcheck // app errors identify their operation.
func (t *tools) pollCreate(
	ctx context.Context, req *sdk.CallToolRequest, in pollCreateInput,
) (*sdk.CallToolResult, output.PollSendJSON, error) {
	request := app.PollCreateRequest{
		GroupID:      in.GroupID,
		Recipient:    in.Recipient,
		Question:     in.Question,
		Options:      in.Options,
		SingleChoice: in.SingleChoice,
	}

	err := request.Check()
	if err != nil {
		return nil, output.PollSendJSON{}, err
	}

	action := fmt.Sprintf("Create poll %q with options %q (single choice: %t)", in.Question, in.Options, in.SingleChoice)

	ask, err := t.confirmPoll(ctx, req, in.GroupID, in.Recipient, action)
	if ask != nil || err != nil {
		return ask, output.PollSendJSON{}, err
	}

	result, err := t.app.PollCreate(ctx, request)

	return t.pollSendResult(ctx, result, err)
}

//nolint:wrapcheck // app errors identify their operation.
func (t *tools) pollVote(
	ctx context.Context, req *sdk.CallToolRequest, in pollVoteInput,
) (*sdk.CallToolResult, output.PollSendJSON, error) {
	request := app.PollVoteRequest{
		GroupID:       in.GroupID,
		Recipient:     in.Recipient,
		Target:        in.Target,
		OptionIndexes: in.OptionIndexes,
		Clear:         in.Clear,
	}
	if in.VoteCount != nil {
		if *in.VoteCount == 0 {
			return nil, output.PollSendJSON{}, fmt.Errorf("%w: voteCount must be positive when supplied", signal.ErrInvalidPoll)
		}

		request.VoteCount = *in.VoteCount
	}

	err := request.Check()
	if err != nil {
		return nil, output.PollSendJSON{}, err
	}

	action := fmt.Sprintf("Vote on poll %s with options %v (clear: %t, counter: automatic)",
		in.Target,
		in.OptionIndexes,
		in.Clear)
	if in.VoteCount != nil {
		action = fmt.Sprintf("Vote on poll %s with options %v (clear: %t, counter: %d)",
			in.Target,
			in.OptionIndexes,
			in.Clear,
			*in.VoteCount)
	}

	ask, err := t.confirmPoll(ctx, req, in.GroupID, in.Recipient, action)
	if ask != nil || err != nil {
		return ask, output.PollSendJSON{}, err
	}

	result, err := t.app.PollVote(ctx, request)

	return t.pollSendResult(ctx, result, err)
}

//nolint:wrapcheck // app errors identify their operation.
func (t *tools) pollClose(
	ctx context.Context, req *sdk.CallToolRequest, in pollCloseInput,
) (*sdk.CallToolResult, output.PollSendJSON, error) {
	request := app.PollCloseRequest{GroupID: in.GroupID, Recipient: in.Recipient, Target: in.Timestamp}

	err := request.Check()
	if err != nil {
		return nil, output.PollSendJSON{}, err
	}

	ask, err := t.confirmPoll(ctx, req, in.GroupID, in.Recipient, fmt.Sprintf("Close our poll at %d", in.Timestamp))
	if ask != nil || err != nil {
		return ask, output.PollSendJSON{}, err
	}

	result, err := t.app.PollClose(ctx, request)

	return t.pollSendResult(ctx, result, err)
}

func (t *tools) confirmPoll(
	ctx context.Context, req *sdk.CallToolRequest, groupID, recipient, action string,
) (*sdk.CallToolResult, error) {
	destination := recipient
	if groupID != "" {
		destination = app.GroupPrefix + groupID
	}

	return t.confirm(ctx, req, []string{destination}, t.names(ctx), action)
}

func (t *tools) pollSendResult(
	ctx context.Context, result app.PollSendResult, sendErr error,
) (*sdk.CallToolResult, output.PollSendJSON, error) {
	if sendErr != nil && !errors.Is(sendErr, app.ErrSendFailed) {
		return nil, output.PollSendJSON{}, sendErr
	}

	names := t.names(ctx)
	res, err := t.plain(names, func(p *output.Printer) error { return p.PollSend(result) })

	return sendResult(res, sendErr, err), output.NewPollSendJSON(result, names), err
}

//nolint:wrapcheck // app errors identify their operation.
func (t *tools) pollShow(
	ctx context.Context, _ *sdk.CallToolRequest, in pollShowInput,
) (*sdk.CallToolResult, output.PollStateJSON, error) {
	request := app.PollShowRequest{GroupID: in.GroupID, Recipient: in.Recipient, Target: in.Target, Durable: in.Durable}
	if in.ScanLimit != nil {
		if in.Durable || *in.ScanLimit < 1 {
			return nil,
				output.PollStateJSON{},
				fmt.Errorf("%w: scanLimit must be positive and cannot accompany durable",
					signal.ErrInvalidPoll)
		}

		request.ScanLimit = *in.ScanLimit
	}

	state, err := t.app.PollShow(ctx, request)
	if err != nil {
		return nil, output.PollStateJSON{}, err
	}

	names := t.names(ctx)
	res, err := t.plain(names, func(p *output.Printer) error { return p.PollState(state) })

	return res, output.NewPollStateJSON(state, names), err
}
