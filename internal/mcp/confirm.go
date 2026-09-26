package mcp

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	// ErrNotConfirmed means that the user declined a write tool's call (mcp serve --confirm).
	ErrNotConfirmed = errors.New("the user did not confirm")
	// errNoElicitation means that --confirm is on but the client can't ask the user.
	errNoElicitation = errors.New("mcp serve --confirm needs a client that supports elicitation")
	// errConfirmMismatch means that a confirmation came back for other arguments.
	errConfirmMismatch = errors.New("the confirmation was for another call")
)

// confirmTTL is how long a confirmation request stays valid.
const confirmTTL = 10 * time.Minute

// confirmer asks the user to confirm the calls of write tools through elicitation. The tool
// returns an input request, and the client calls it again with the user's answer; the SDK does
// that round trip itself for clients on protocol versions before multi round-trip requests.
type confirmer struct {
	mu      sync.Mutex
	pending map[string]pendingCall
}

// pendingCall is a call waiting for the user's answer.
type pendingCall struct {
	call    string
	expires time.Time
}

func newConfirmer() *confirmer {
	return &confirmer{pending: map[string]pendingCall{}}
}

// confirm checks chats (recipient arguments) against the allowlist and, with --confirm, asks the
// user to confirm action on them. It returns nil, nil when the call may go ahead, a result to
// return when the user must be asked first, or an error.
func (t *tools) confirm(
	ctx context.Context, req *sdk.CallToolRequest, chats []string, names app.Names, action string,
) (*sdk.CallToolResult, error) {
	if t.confirmer == nil {
		return nil, nil //nolint:nilnil // no result: go ahead
	}

	// Don't ask about a call that the allowlist rejects anyway.
	err := t.app.CheckRecipients(ctx, chats)
	if err != nil {
		return nil, err //nolint:wrapcheck // app wraps it
	}

	return t.confirmer.check(req, action+" to "+chatLabels(chats, names)+"?")
}

// check returns nil, nil if req carries the user's acceptance of a pending request for the same
// call, else a result that asks the user with message.
func (c *confirmer) check(req *sdk.CallToolRequest, message string) (*sdk.CallToolResult, error) {
	call := callKey(req.Params)
	now := time.Now()

	for id, resp := range req.Params.InputResponses {
		pending, ok := c.take(id, now)
		if !ok {
			continue
		}

		if pending.call != call {
			return nil, errConfirmMismatch
		}

		answer, _ := resp.(*sdk.ElicitResult)
		if answer == nil || answer.Action != "accept" {
			return nil, ErrNotConfirmed
		}

		return nil, nil //nolint:nilnil // no result: go ahead
	}

	if !canElicit(req.Session) {
		return nil, errNoElicitation
	}

	id := c.add(call, now)

	return &sdk.CallToolResult{InputRequests: sdk.InputRequestMap{id: &sdk.ElicitParams{
		Message:         message,
		RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}}}, nil
}

// add stores a pending call and returns its request ID; it drops expired ones.
func (c *confirmer) add(call string, now time.Time) string {
	c.mu.Lock()
	defer c.mu.Unlock()

	for id, pending := range c.pending {
		if now.After(pending.expires) {
			delete(c.pending, id)
		}
	}

	id := "confirm-" + rand.Text()
	c.pending[id] = pendingCall{call: call, expires: now.Add(confirmTTL)}

	return id
}

// take removes the pending call with the ID and returns it unless it expired.
func (c *confirmer) take(id string, now time.Time) (pendingCall, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	pending, ok := c.pending[id]
	delete(c.pending, id)

	return pending, ok && !now.After(pending.expires)
}

// callKey identifies a call by its tool and arguments, independent of the arguments' key order
// and spacing.
func callKey(params *sdk.CallToolParamsRaw) string {
	var args any

	err := json.Unmarshal(params.Arguments, &args)
	if err != nil {
		return params.Name + " " + string(params.Arguments)
	}

	canonical, err := json.Marshal(args)
	if err != nil {
		return params.Name + " " + string(params.Arguments)
	}

	return params.Name + " " + string(canonical)
}

func canElicit(session *sdk.ServerSession) bool {
	if session == nil {
		return false
	}

	params := session.InitializeParams()

	return params != nil && params.Capabilities != nil && params.Capabilities.Elicitation != nil
}

// chatLabels names recipient arguments (group:<id> or an ACI) for the user.
func chatLabels(chats []string, names app.Names) string {
	labels := make([]string, 0, len(chats))

	for _, chat := range chats {
		if groupID, ok := strings.CutPrefix(chat, app.GroupPrefix); ok {
			if title := names.GroupTitle(groupID); title != "" {
				labels = append(labels, "group "+title)
			} else {
				labels = append(labels, chat)
			}

			continue
		}

		rcpt := signal.Recipient{ACI: chat}

		switch label := names.Label(rcpt); {
		case names.IsSelf(rcpt):
			labels = append(labels, "yourself (note to self)")
		case label != "":
			labels = append(labels, label)
		default:
			labels = append(labels, chat)
		}
	}

	return strings.Join(labels, ", ")
}
