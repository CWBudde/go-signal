package mcp

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	// errNoAttachDir means that send_message got attachments but has no directory to take them from.
	errNoAttachDir = errors.New("attachments are disabled (mcp serve --attach-dir)")
	// errMessageRef means that a message reference is neither an inbox entry ID nor
	// <author>:<timestamp>.
	errMessageRef = errors.New("want an inbox message id or <author>:<timestamp>")
	// errNoChat means that react got a message as <author>:<timestamp> without its chat.
	errNoChat = errors.New("chat is required unless message is an inbox message id")
)

// allowlistNote is appended to the descriptions of the tools that send.
const allowlistNote = " The server only sends to the chats it was started to allow (mcp serve --allow-recipient); " +
	"other recipients are rejected."

type sendMessageInput struct {
	Recipients  []string `json:"recipients"            jsonschema:"users (number, ACI, @username, self) or groups"`
	Text        string   `json:"text,omitempty"        jsonschema:"the message; @{<user>} mentions a user"`
	Attachments []string `json:"attachments,omitempty" jsonschema:"files to attach, relative to the attachment directory"`
	Quote       string   `json:"quote,omitempty"       jsonschema:"reply to a message: inbox id or <author>:<timestamp>"`
	QuoteText   string   `json:"quoteText,omitempty"   jsonschema:"quoted text, shown if the recipient lost the message"`
}

type reactInput struct {
	Message string `json:"message"          jsonschema:"the message: its inbox id, or <author>:<timestamp> with chat"`
	Chat    string `json:"chat,omitempty"   jsonschema:"the message's chat (a user or group); default the inbox entry's"`
	Emoji   string `json:"emoji"            jsonschema:"a single emoji"`
	Remove  bool   `json:"remove,omitempty" jsonschema:"take back this reaction"`
}

type deleteMessageInput struct {
	Chat      string `json:"chat"      jsonschema:"the chat the message went to: a user or group"`
	Timestamp uint64 `json:"timestamp" jsonschema:"the timestamp of our own message, from send_message"`
}

// addWriteTools registers the tools that send messages.
func addWriteTools(server *sdk.Server, handlers *tools) {
	sdk.AddTool(server, &sdk.Tool{
		Name:  "send_message",
		Title: "Send message",
		Description: "Send a text message, with optional attachments and a quoted reply, to users or groups. It " +
			"also shows up on your other devices. The result has the message's timestamp (for delete_message) " +
			"and the outcome per recipient." + allowlistNote,
		Annotations: &sdk.ToolAnnotations{DestructiveHint: new(false), OpenWorldHint: new(true)},
	}, handlers.sendMessage)

	sdk.AddTool(server, &sdk.Tool{
		Name:  "react",
		Title: "React to message",
		Description: "React to a message with an emoji, or take the reaction back. Name the message by its " +
			"inbox id (from messages_list), or as <author>:<timestamp> together with its chat." + allowlistNote,
		Annotations: &sdk.ToolAnnotations{DestructiveHint: new(false), OpenWorldHint: new(true)},
	}, handlers.react)

	sdk.AddTool(server, &sdk.Tool{
		Name:  "delete_message",
		Title: "Delete message for everyone",
		Description: "Delete one of our own messages for everyone in its chat (remote delete), by the timestamp " +
			"send_message returned. Signal apps ignore deletes of old messages." + allowlistNote,
		Annotations: &sdk.ToolAnnotations{DestructiveHint: new(true), OpenWorldHint: new(true)},
	}, handlers.deleteMessage)
}

func (t *tools) sendMessage(
	ctx context.Context, req *sdk.CallToolRequest, in sendMessageInput,
) (*sdk.CallToolResult, output.SendJSON, error) {
	if len(in.Attachments) > 0 && t.attachDir == "" {
		return nil, output.SendJSON{}, errNoAttachDir
	}

	send := app.SendRequest{
		Body: in.Text, Attachments: in.Attachments, AttachDir: t.attachDir, QuoteText: in.QuoteText,
	}

	names := t.names(ctx)

	chats, err := t.chatArgs(ctx, in.Recipients)
	if err != nil {
		return nil, output.SendJSON{}, err
	}

	send.Recipients = chats

	if in.Quote != "" {
		ref, _, err := t.messageRef(ctx, in.Quote)
		if err != nil {
			return nil, output.SendJSON{}, fmt.Errorf("quote: %w", err)
		}

		send.Quote = ref
	}

	ask, err := t.confirm(ctx, req, chats, names, sendSummary(in))
	if ask != nil || err != nil {
		return ask, output.SendJSON{}, err
	}

	sent, err := t.app.Send(ctx, send)
	if err != nil && !errors.Is(err, app.ErrSendFailed) {
		return nil, output.SendJSON{}, err //nolint:wrapcheck // app wraps it
	}

	res, plainErr := t.plain(names, func(p *output.Printer) error { return p.Send(sent) })

	return sendResult(res, err, plainErr), output.NewSendJSON(sent, names), plainErr
}

func (t *tools) react(
	ctx context.Context, req *sdk.CallToolRequest, in reactInput,
) (*sdk.CallToolResult, output.ReactJSON, error) {
	target, chat, err := t.messageRef(ctx, in.Message)
	if err != nil {
		return nil, output.ReactJSON{}, fmt.Errorf("message: %w", err)
	}

	if in.Chat != "" {
		resolved, err := t.app.ResolveChat(ctx, in.Chat)
		if err != nil {
			return nil, output.ReactJSON{}, err //nolint:wrapcheck // app wraps it
		}

		chat = chatArg(resolved)
	}

	if chat == "" {
		return nil, output.ReactJSON{}, errNoChat
	}

	names := t.names(ctx)

	action := "React with " + in.Emoji + " to message " + target
	if in.Remove {
		action = "Take back the reaction " + in.Emoji + " on message " + target
	}

	ask, err := t.confirm(ctx, req, []string{chat}, names, action)
	if ask != nil || err != nil {
		return ask, output.ReactJSON{}, err
	}

	reacted, err := t.app.React(ctx, app.ReactRequest{
		Recipients: []string{chat}, Target: target, Emoji: in.Emoji, Remove: in.Remove,
	})
	if err != nil && !errors.Is(err, app.ErrSendFailed) {
		return nil, output.ReactJSON{}, err //nolint:wrapcheck // app wraps it
	}

	res, plainErr := t.plain(names, func(p *output.Printer) error { return p.React(reacted) })

	return sendResult(res, err, plainErr), output.NewReactJSON(reacted, names), plainErr
}

func (t *tools) deleteMessage(
	ctx context.Context, req *sdk.CallToolRequest, in deleteMessageInput,
) (*sdk.CallToolResult, output.DeleteJSON, error) {
	chats, err := t.chatArgs(ctx, []string{in.Chat})
	if err != nil {
		return nil, output.DeleteJSON{}, err
	}

	names := t.names(ctx)
	action := "Delete our message " + strconv.FormatUint(in.Timestamp, 10) + " for everyone"

	ask, err := t.confirm(ctx, req, chats, names, action)
	if ask != nil || err != nil {
		return ask, output.DeleteJSON{}, err
	}

	deleted, err := t.app.Delete(ctx, app.DeleteRequest{Recipients: chats, Target: in.Timestamp})
	if err != nil && !errors.Is(err, app.ErrSendFailed) {
		return nil, output.DeleteJSON{}, err //nolint:wrapcheck // app wraps it
	}

	res, plainErr := t.plain(names, func(p *output.Printer) error { return p.Delete(deleted) })

	return sendResult(res, err, plainErr), output.NewDeleteJSON(deleted, names), plainErr
}

// sendResult marks res as an error when sending failed for some recipients (sendErr wraps
// app.ErrSendFailed); the structured content still has the outcome per recipient.
func sendResult(res *sdk.CallToolResult, sendErr, plainErr error) *sdk.CallToolResult {
	if sendErr == nil || plainErr != nil {
		return res
	}

	res.IsError = true
	res.Content = append(res.Content, &sdk.TextContent{Text: sendErr.Error()})

	return res
}

// chatArgs resolves chat arguments (see app.App.ResolveChat) to recipient arguments.
func (t *tools) chatArgs(ctx context.Context, args []string) ([]string, error) {
	out := make([]string, 0, len(args))

	for _, arg := range args {
		chat, err := t.app.ResolveChat(ctx, arg)
		if err != nil {
			return nil, err //nolint:wrapcheck // app wraps it
		}

		out = append(out, chatArg(chat))
	}

	return out, nil
}

// chatArg returns the recipient argument (see app.ParseRecipient) of chat.
func chatArg(chat signal.Chat) string {
	if chat.IsGroup() {
		return app.GroupPrefix + chat.GroupID
	}

	if chat.Recipient.ACI != "" {
		return chat.Recipient.ACI
	}

	return chat.Recipient.String()
}

// messageRef turns a message reference into <author>:<timestamp> (see app.ParseTarget): an inbox
// entry ID, which also gives the message's chat as a recipient argument, or <author>:<timestamp>
// itself, which gives no chat.
func (t *tools) messageRef(ctx context.Context, ref string) (string, string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") {
		return ref, "", nil
	}

	_, err := strconv.ParseInt(ref, 10, 64)
	if err != nil {
		return "", "", fmt.Errorf("%q: %w", ref, errMessageRef)
	}

	msg, err := t.inbox.Message(ctx, ref)
	if err != nil {
		return "", "", err //nolint:wrapcheck // app wraps it
	}

	author := msg.Sender.ACI
	if author == "" {
		author = msg.Sender.String()
	}

	return author + ":" + strconv.FormatUint(msg.Timestamp, 10), chatArg(msg.Chat), nil
}

// sendSummary describes what send_message sends, for the confirmation.
func sendSummary(in sendMessageInput) string {
	var out strings.Builder

	out.WriteString("Send")

	if in.Quote != "" {
		out.WriteString(" a reply")
	}

	if in.Text != "" {
		fmt.Fprintf(&out, " %q", in.Text)
	}

	if len(in.Attachments) > 0 {
		fmt.Fprintf(&out, " with the attachments %s", strings.Join(in.Attachments, ", "))
	}

	return out.String()
}
