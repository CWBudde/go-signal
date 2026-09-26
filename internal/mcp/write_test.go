package mcp_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/mcp"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	sendMessage   = "send_message"
	deleteMessage = "delete_message"
	thumbsUp      = "👍"
	notesFile     = "notes.txt"
	messageArg    = "message"
	recipientsArg = "recipients"
	textArg       = "text"
	familyTitle   = "Family"
	accept        = "accept"
)

var errUnexpectedElicit = errors.New("unexpected elicitation")

// sent is the structured output of send_message, react and delete_message.
type sent struct {
	Timestamp uint64 `json:"timestamp"`
	Results   []struct {
		Type    string `json:"type"`
		ACI     string `json:"aci"`
		GroupID string `json:"groupId"`
		Success bool   `json:"success"`
	} `json:"results"`
}

// writeFake returns toolsFake with the members of Family for sending.
func writeFake() *signaltest.Fake {
	fake := toolsFake()
	fake.Groups = map[string][]signal.Recipient{familyID: {{ACI: testAccount().ACI}, {ACI: aliceACI}}}

	return fake
}

// allow returns the App option that allows entries.
func allow(t *testing.T, entries ...string) app.Option {
	t.Helper()

	list, err := app.ParseAllowlist(entries)
	if err != nil {
		t.Fatal(err)
	}

	return app.WithAllowlist(list)
}

func TestSendMessage(t *testing.T) {
	t.Parallel()

	fake := writeFake()
	session := connectWith(t, fake, mcp.Options{}, testClient{}, allow(t, aliceNumber, app.GroupPrefix+familyID))

	var out sent

	txt := call(t, session, sendMessage, map[string]any{
		recipientsArg: []string{aliceNumber, familyTitle}, textArg: "hi",
	}, &out)

	checkSent(t, out, txt)

	if got := fake.Sent(); len(got) != 2 || got[0].Body != "hi" || got[1].GroupID != familyID {
		t.Errorf("sent %+v", got)
	}
}

// checkSent checks the result of sending to Alice and Family.
func checkSent(t *testing.T, out sent, txt string) {
	t.Helper()

	if len(out.Results) != 2 || !out.Results[0].Success || out.Results[0].ACI != aliceACI ||
		out.Results[1].GroupID != familyID || !out.Results[1].Success || out.Timestamp == 0 {
		t.Errorf("result %+v", out)
	}

	if !strings.Contains(txt, "sent") {
		t.Errorf("text %q", txt)
	}
}

func TestWriteToolsRejected(t *testing.T) {
	t.Parallel()

	fake := writeFake()
	// Without allowed recipients, the tools send to no one.
	session := connectWith(t, fake, mcp.Options{}, testClient{}, allow(t))

	for name, args := range map[string]map[string]any{
		sendMessage:   {recipientsArg: []string{aliceNumber}, textArg: "hi"},
		"react":       {messageArg: aliceACI + ":1000", chat: familyTitle, "emoji": thumbsUp},
		deleteMessage: {chat: aliceACI, "timestamp": 1000},
	} {
		res := callRaw(t, session, name, args)
		if !res.IsError || !strings.Contains(text(res), app.ErrRecipientNotAllowed.Error()) {
			t.Errorf("%s: %+v, want the recipient rejected", name, text(res))
		}
	}

	if len(fake.Sent()) != 0 {
		t.Errorf("sent %+v", fake.Sent())
	}
}

func TestSendMessageAttachments(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, notesFile), []byte("text"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	args := func(path string) map[string]any {
		return map[string]any{recipientsArg: []string{aliceNumber}, "attachments": []string{path}}
	}

	// Without an attachment directory, there are no attachments.
	session := connectWith(t, writeFake(), mcp.Options{}, testClient{}, allow(t, app.AllowAll))

	res := callRaw(t, session, sendMessage, args(filepath.Join(dir, notesFile)))
	if !res.IsError || !strings.Contains(text(res), "--attach-dir") {
		t.Errorf("without attach dir: %q", text(res))
	}

	fake := writeFake()
	session = connectWith(t, fake, mcp.Options{AttachDir: dir}, testClient{}, allow(t, app.AllowAll))

	call(t, session, sendMessage, args(notesFile), &sent{})

	res = callRaw(t, session, sendMessage, args("../"+filepath.Base(dir)+"x"))
	if !res.IsError || !strings.Contains(text(res), app.ErrOutsideAttachDir.Error()) {
		t.Errorf("outside: %q", text(res))
	}

	if got := fake.Uploaded(); len(got) != 1 || got[0].Filename != notesFile {
		t.Errorf("uploaded %+v", got)
	}
}

func TestReactAndDelete(t *testing.T) {
	t.Parallel()

	fake := writeFake()
	session := connectWith(t, fake, mcp.Options{}, testClient{}, allow(t, aliceNumber))

	got := waitForPush(t, session, fake, photoMessage(1000))

	var out sent

	call(t, session, "react", map[string]any{messageArg: got.Messages[0].ID, "emoji": thumbsUp}, &out)
	call(t, session, deleteMessage, map[string]any{chat: aliceNumber, "timestamp": 900}, &out)

	reqs := fake.Sent()
	if len(reqs) != 2 {
		t.Fatalf("sent %+v", reqs)
	}

	reaction := reqs[0].Reaction
	if reaction == nil || reaction.Emoji != thumbsUp || reaction.TargetAuthor.ACI != aliceACI ||
		reaction.TargetTimestamp != 1000 || reqs[0].Recipients[0].ACI != aliceACI {
		t.Errorf("reaction %+v", reqs[0])
	}

	if reqs[1].DeleteTarget != 900 || reqs[1].Recipients[0].ACI != aliceACI {
		t.Errorf("delete %+v", reqs[1])
	}
}

func TestConfirm(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"", "2025-11-25"} {
		for _, action := range []string{accept, "decline"} {
			fake := writeFake()
			asked := make(chan string, 1)
			session := connectWith(t, fake, mcp.Options{Confirm: true}, testClient{
				options: &sdk.ClientOptions{
					ElicitationHandler: func(_ context.Context, req *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
						asked <- req.Params.Message

						return &sdk.ElicitResult{Action: action}, nil
					},
				},
				session: &sdk.ClientSessionOptions{ProtocolVersion: version},
			}, allow(t, aliceNumber))

			res := callRaw(t, session, sendMessage, map[string]any{recipientsArg: []string{aliceNumber}, textArg: "hi"})

			accepted := action == accept
			if res.IsError == accepted || (len(fake.Sent()) == 1) != accepted {
				t.Errorf("%q %s: error %v (%s), sent %d", version, action, res.IsError, text(res), len(fake.Sent()))
			}

			if msg := <-asked; !strings.Contains(msg, `"hi"`) || !strings.Contains(msg, alice) {
				t.Errorf("%q %s: asked %q", version, action, msg)
			}
		}
	}
}

func TestConfirmWithoutElicitation(t *testing.T) {
	t.Parallel()

	fake := writeFake()
	session := connectWith(t, fake, mcp.Options{Confirm: true}, testClient{}, allow(t, aliceNumber))

	res := callRaw(t, session, sendMessage, map[string]any{recipientsArg: []string{aliceNumber}, textArg: "hi"})
	if !res.IsError || !strings.Contains(text(res), "elicitation") || len(fake.Sent()) != 0 {
		t.Errorf("got %q, sent %d; want an error", text(res), len(fake.Sent()))
	}
}

// TestConfirmBound answers the confirmation by hand: an answer only counts once, and only for the
// call it was asked for.
func TestConfirmBound(t *testing.T) {
	t.Parallel()

	fake := writeFake()
	session := connectWith(t, fake, mcp.Options{Confirm: true}, testClient{options: &sdk.ClientOptions{
		ElicitationHandler: func(context.Context, *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
			return nil, errUnexpectedElicit
		},
		MultiRoundTrip: &sdk.MultiRoundTripOptions{Disabled: true},
	}}, allow(t, aliceNumber))

	args := map[string]any{recipientsArg: []string{aliceNumber}, textArg: "hi"}

	callWith := func(args map[string]any, answers sdk.InputResponseMap) *sdk.CallToolResult {
		res, err := session.CallTool(t.Context(), &sdk.CallToolParams{
			Name: sendMessage, Arguments: args, InputResponses: answers,
		})
		if err != nil {
			t.Fatalf("call: %v", err)
		}

		return res
	}

	asked := callWith(args, nil)
	if !asked.NeedsInput() || len(asked.InputRequests) != 1 {
		t.Fatalf("got %+v, want an input request", asked)
	}

	answers := sdk.InputResponseMap{}
	for id := range asked.InputRequests {
		answers[id] = &sdk.ElicitResult{Action: accept}
	}

	other := map[string]any{recipientsArg: []string{aliceNumber}, textArg: "something else"}
	if res := callWith(other, answers); !res.IsError || !strings.Contains(text(res), "another call") {
		t.Errorf("other arguments: %q, want rejected", text(res))
	}

	// The answer was used up by the rejected call, so the server asks again.
	if res := callWith(args, answers); !res.NeedsInput() || len(fake.Sent()) != 0 {
		t.Errorf("replayed answer: %+v, sent %d; want a new request", res, len(fake.Sent()))
	}
}
