package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/mcp"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	reactTool    = "react"
	emojiArg     = "emoji"
	timestampArg = "timestamp"
	quoteArg     = "quote"
	replyBody    = "reply"
)

// A tool error must preserve delivery outcomes: retrying an entire partially delivered call
// would send duplicates to the successful recipients.
func TestWriteToolsPreservePartialDelivery(t *testing.T) {
	t.Parallel()

	for name, args := range map[string]map[string]any{
		sendMessage:   {recipientsArg: []string{familyTitle}, textArg: "hi"},
		reactTool:     {messageArg: aliceACI + ":1000", chat: familyTitle, emojiArg: thumbsUp},
		deleteMessage: {chat: familyTitle, timestampArg: 1000},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fake := writeFake()
			fake.Groups[familyID] = append(fake.Groups[familyID], signal.Recipient{ACI: bobACI})
			fake.SendFailures = map[string]error{bobACI: signal.ErrUntrustedIdentity}
			session := connectWith(t, fake, mcp.Options{}, testClient{}, allow(t, app.GroupPrefix+familyID))
			res := callRaw(t, session, name, args)
			out := deliveryOutput(t, res)

			if !res.IsError || !strings.Contains(text(res), app.ErrSendFailed.Error()) {
				t.Errorf("partial delivery must be a tool error: %+v", res)
			}

			if out.Timestamp == 0 || len(out.Results) != 1 {
				t.Fatalf("delivery result: %+v", out)
			}

			checkPartialGroup(t, out)

			if got := fake.Sent(); len(got) != 1 {
				t.Errorf("sent %d requests; want one attempt", len(got))
			}
		})
	}
}

func checkPartialGroup(t *testing.T, out output.SendJSON) {
	t.Helper()

	group := out.Results[0]
	if group.GroupID != familyID || group.Success || len(group.Members) != 2 {
		t.Fatalf("partial group result: %+v", group)
	}

	if good, bad := group.Members[0], group.Members[1]; good.ACI != aliceACI || !good.Success ||
		bad.ACI != bobACI || bad.Success || !strings.Contains(bad.Error, signal.ErrUntrustedIdentity.Error()) {
		t.Errorf("member outcomes: %+v", group.Members)
	}
}

// deliveryOutput reads the common delivery fields even when IsError is true.
func deliveryOutput(t *testing.T, res *sdk.CallToolResult) output.SendJSON {
	t.Helper()

	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}

	var out output.SendJSON

	err = json.Unmarshal(data, &out)
	if err != nil {
		t.Fatalf("delivery output %s: %v", data, err)
	}

	return out
}

func TestSendReplyUsesInboxAuthorAndTimestamp(t *testing.T) {
	t.Parallel()

	fake := writeFake()
	session := connectWith(t, fake, mcp.Options{}, testClient{}, allow(t, aliceNumber))
	got := waitForPush(t, session, fake, photoMessage(1000))

	call(t, session, sendMessage, map[string]any{
		recipientsArg: []string{aliceNumber}, textArg: replyBody,
		quoteArg: got.Messages[0].ID, "quoteText": "original text",
	}, &sent{})

	reqs := fake.Sent()
	if len(reqs) != 1 || reqs[0].Quote == nil {
		t.Fatalf("sent %+v, want one quoted reply", reqs)
	}

	quote := reqs[0].Quote
	if quote.Author.ACI != aliceACI || quote.Timestamp != 1000 || quote.Text != "original text" {
		t.Errorf("quote %+v", quote)
	}
}

func TestWriteToolsRejectMissingMessageReferences(t *testing.T) {
	t.Parallel()

	fake := writeFake()
	session := connectWith(t, fake, mcp.Options{}, testClient{}, allow(t, aliceNumber))

	for _, ref := range []string{"not-an-id", "42"} {
		for name, args := range map[string]map[string]any{
			sendMessage: {recipientsArg: []string{aliceNumber}, textArg: replyBody, quoteArg: ref},
			reactTool:   {messageArg: ref, emojiArg: thumbsUp},
		} {
			if txt := toolError(t, session, name, args); !strings.Contains(txt, ref) {
				t.Errorf("%s: %q, want the invalid reference %q identified", name, txt, ref)
			}
		}
	}

	if txt := toolError(t, session, reactTool, map[string]any{
		messageArg: aliceACI + ":1000", emojiArg: thumbsUp,
	}); !strings.Contains(txt, "chat is required") {
		t.Errorf("unscoped reaction: %q", txt)
	}

	if got := fake.Sent(); len(got) != 0 {
		t.Errorf("invalid references sent messages: %+v", got)
	}
}
