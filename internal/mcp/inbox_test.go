package mcp_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/mcp"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	messagesList  = "messages_list"
	messagesWait  = "messages_wait"
	attachmentGet = "attachment_get"
	markRead      = "mark_read"
	cursor        = "cursor"
	chat          = "chat"
	chatsURI      = "signal://chats"
	chatURIPrefix = "signal://chat/"
)

// messages is the structured output of messages_list and messages_wait.
type messages struct {
	Messages []struct {
		ID     string         `json:"id"`
		Unread bool           `json:"unread"`
		Event  map[string]any `json:"event"`
	} `json:"messages"`
	Cursor string `json:"cursor"`
	More   bool   `json:"more"`
}

func photoMessage(ts uint64) *signal.Message {
	aliceRcpt := signal.Recipient{ACI: aliceACI, Number: aliceNumber}

	return &signal.Message{
		Envelope: signal.Envelope{Sender: aliceRcpt, Chat: signal.Chat{Recipient: aliceRcpt}, Timestamp: ts},
		Body:     "look",
		Attachments: []signal.Attachment{{
			ContentType: "image/png", Filename: "photo.png", Size: 3, Remote: signal.RemoteAttachment{CDNKey: "cdn-photo"},
		}},
	}
}

// TestInboxFlow waits for a message, reads it, fetches its attachment and marks it read, with no
// other receive running: what PLAN.md 5.4 asks for.
func TestInboxFlow(t *testing.T) {
	t.Parallel()

	fake := toolsFake()
	fake.Attachments = map[string][]byte{"cdn-photo": []byte("png")}
	dir := t.TempDir()
	session := connectWith(t, fake, mcp.Options{DownloadDir: dir}, nil)

	got := waitForPush(t, session, fake, photoMessage(1000))
	if len(got.Messages) != 1 || got.Cursor != "1" {
		t.Fatalf("waited: %+v", got)
	}

	msg := got.Messages[0]
	sender, _ := msg.Event["sender"].(map[string]any)

	if msg.ID != "1" || !msg.Unread || msg.Event["body"] != "look" || sender["name"] != alice {
		t.Errorf("message %+v, want Alice's unread photo", msg)
	}

	checkAttachment(t, session, msg.ID, filepath.Join(dir, "1000-1-photo.png"))
	checkMarkRead(t, session, fake)
}

// waitForPush calls messages_wait from the cursor of an empty inbox, pushes evt while it waits
// and returns the result.
func waitForPush(t *testing.T, session *sdk.ClientSession, fake *signaltest.Fake, evt signal.Event) messages {
	t.Helper()

	var got messages

	txt := call(t, session, messagesList, nil, &got)
	if len(got.Messages) != 0 || got.Cursor != "0" || !strings.Contains(txt, "No new messages") {
		t.Fatalf("empty inbox: %+v, %q", got, txt)
	}

	waited := make(chan *sdk.CallToolResult, 1)
	args := map[string]any{cursor: got.Cursor, "timeout": 30}

	go func() {
		res, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: messagesWait, Arguments: args})
		if err != nil {
			res = &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: err.Error()}}}
		}

		waited <- res
	}()

	if !fake.Push(evt) {
		t.Fatal("push failed")
	}

	got = messages{}
	decode(t, <-waited, &got)

	return got
}

// checkAttachment fetches the first attachment of the message with the ID, a PNG, and checks
// that it was saved to path and returned as an image.
func checkAttachment(t *testing.T, session *sdk.ClientSession, id, path string) {
	t.Helper()

	res := callRaw(t, session, attachmentGet, map[string]any{"message": id})
	if res.IsError {
		t.Fatalf("attachment_get: %s", text(res))
	}

	data, err := os.ReadFile(path)
	if err != nil || string(data) != "png" || !strings.Contains(text(res), path) {
		t.Errorf("file %q, %v; text %q", data, err, text(res))
	}

	if len(res.Content) != 2 {
		t.Fatalf("content %+v, want text and the image", res.Content)
	}

	if img, ok := res.Content[1].(*sdk.ImageContent); !ok || string(img.Data) != "png" || img.MIMEType != "image/png" {
		t.Errorf("image content %+v", res.Content[1])
	}
}

// checkMarkRead marks Alice's messages read and checks that she got a receipt and that they are
// read.
func checkMarkRead(t *testing.T, session *sdk.ClientSession, fake *signaltest.Fake) {
	t.Helper()

	var marked struct {
		Messages int `json:"messages"`
		Senders  int `json:"senders"`
	}

	call(t, session, markRead, map[string]any{chat: aliceNumber}, &marked)

	if marked.Messages != 1 || marked.Senders != 1 || len(fake.Receipts()) != 1 {
		t.Errorf("marked %+v, receipts %+v", marked, fake.Receipts())
	}

	var after messages

	call(t, session, messagesList, map[string]any{chat: aliceNumber}, &after)

	if len(after.Messages) != 1 || after.Messages[0].Unread {
		t.Errorf("after mark_read: %+v, want the message read", after)
	}
}

func TestMessagesList(t *testing.T) {
	t.Parallel()

	fake := toolsFake()
	session := connect(t, fake)

	for ts := range uint64(3) {
		fake.Push(photoMessage(1000 + ts))
	}

	// Wait until all three are stored.
	var got messages

	call(t, session, messagesWait, map[string]any{cursor: "2"}, &got)

	call(t, session, messagesList, map[string]any{cursor: "0", "limit": 2, chat: aliceNumber}, &got)

	if len(got.Messages) != 2 || got.Cursor != "2" || !got.More {
		t.Errorf("first page %+v", got)
	}

	txt := call(t, session, messagesList, map[string]any{"since": "1970-01-01T00:00:01.002Z"}, &got)

	if len(got.Messages) != 1 || got.Messages[0].ID != "3" || !strings.HasPrefix(txt, "#3 (unread) ") {
		t.Errorf("since: %+v, %q", got, txt)
	}

	for _, args := range []map[string]any{
		{cursor: "x"},
		{"since": "yesterday"},
		{chat: "Nobody"},
	} {
		res := callRaw(t, session, messagesList, args)
		if !res.IsError {
			t.Errorf("%v: got %+v, want a tool error", args, res)
		}
	}
}

func TestMessagesWaitTimeout(t *testing.T) {
	t.Parallel()

	session := connect(t, toolsFake())

	var got messages

	start := time.Now()
	txt := call(t, session, messagesWait, map[string]any{"timeout": 1}, &got)

	if len(got.Messages) != 0 || got.Cursor != "0" || !strings.Contains(txt, "No new messages") {
		t.Errorf("timeout: %+v, %q", got, txt)
	}

	if elapsed := time.Since(start); elapsed < time.Second {
		t.Errorf("returned after %v, want the timeout", elapsed)
	}
}

func TestChatResources(t *testing.T) {
	t.Parallel()

	fake := toolsFake()
	updated := make(chan string, 10)
	session := connectWith(t, fake, mcp.Options{}, &sdk.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, req *sdk.ResourceUpdatedNotificationRequest) {
			updated <- req.Params.URI
		},
	})

	own := signal.Recipient{ACI: testAccount().ACI}
	fake.Push(&signal.Message{
		Envelope: signal.Envelope{Sender: own, Chat: signal.Chat{GroupID: familyID}, Timestamp: 5, Sync: true},
		Body:     "hello family",
	})

	subscribe(t, session, fake, updated, chatsURI, chatURIPrefix+aliceACI)

	// Alice's chat is the newest; the group's URI has its ID percent-encoded.
	familyURI := chatURIPrefix + "group%3A" + strings.NewReplacer("+", "%2B", "/", "%2F", "=", "%3D").Replace(familyID)
	content := readResource(t, session, chatsURI)

	if !strings.Contains(content, `"uri":"`+chatURIPrefix+aliceACI+`"`) || !strings.Contains(content, familyURI) ||
		strings.Index(content, aliceACI) > strings.Index(content, familyURI) {
		t.Errorf("chats %s, want Alice's chat, then %s", content, familyURI)
	}

	content = readResource(t, session, familyURI)
	if !strings.Contains(content, "hello family") || !strings.Contains(content, `"groupTitle":"Family"`) ||
		strings.Contains(content, "look") {
		t.Errorf("family chat %s", content)
	}

	_, err := session.ReadResource(t.Context(), &sdk.ReadResourceParams{URI: chatURIPrefix + "%zz"})
	if err == nil {
		t.Error("read a bad chat URI")
	}
}

// subscribe subscribes to the uris and pushes messages from Alice until each of them was
// updated. The SDK subscribes in the background (subscriptions/listen), so the first messages
// may come too early.
func subscribe(t *testing.T, session *sdk.ClientSession, fake *signaltest.Fake, updated <-chan string, uris ...string) {
	t.Helper()

	for _, uri := range uris {
		err := session.Subscribe(t.Context(), &sdk.SubscribeParams{URI: uri})
		if err != nil {
			t.Fatalf("subscribe %s: %v", uri, err)
		}
	}

	got := map[string]bool{}

	for ts := uint64(6); len(got) < len(uris); ts++ {
		if ts > 100 {
			t.Fatalf("updates %v, want %v", got, uris)
		}

		fake.Push(photoMessage(ts))

		select {
		case uri := <-updated:
			got[uri] = true
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// readResource reads the JSON resource uri and returns its text.
func readResource(t *testing.T, session *sdk.ClientSession, uri string) string {
	t.Helper()

	res, err := session.ReadResource(t.Context(), &sdk.ReadResourceParams{URI: uri})
	if err != nil {
		t.Fatalf("read %s: %v", uri, err)
	}

	if len(res.Contents) != 1 || res.Contents[0].MIMEType != "application/json" {
		t.Fatalf("read %s: %+v, want one JSON text", uri, res.Contents)
	}

	return res.Contents[0].Text
}

// TestServeLost checks that the server ends with the error of a connection lost for good,
// although the MCP client keeps stdin open.
func TestServeLost(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{testAccount()}}

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}

	defer client.Close()

	events := make(chan signal.Event, 1)
	events <- &signal.Connection{State: signal.StateLoggedOut}

	stdin, stdinWriter := io.Pipe()
	defer stdinWriter.Close()

	err = mcp.Serve(t.Context(), app.New(client), events, mcp.Options{}, stdin, io.Discard)
	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Errorf("serve: %v, want ErrDeviceUnlinked", err)
	}
}
