package mcp_test

import (
	"errors"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var errInbox = errors.New("inbox table locked")

// toolError calls the tool and returns its error text; the call must fail as a tool error.
func toolError(t *testing.T, session *sdk.ClientSession, name string, args map[string]any) string {
	t.Helper()

	res := callRaw(t, session, name, args)
	if !res.IsError {
		t.Errorf("%s %v succeeded", name, args)
	}

	return text(res)
}

func TestInboxToolsRejectBadArguments(t *testing.T) {
	t.Parallel()

	session := connect(t, toolsFake())
	unknown := map[string]any{chat: "Book Club"}

	for _, name := range []string{messagesList, messagesWait, markRead} {
		if txt := toolError(t, session, name, unknown); !strings.Contains(txt, "Book Club") {
			t.Errorf("%s: %q, want the unknown chat named", name, txt)
		}
	}

	for _, id := range []string{"zz", "0", "42"} {
		if txt := toolError(t, session, attachmentGet, map[string]any{"message": id}); !strings.Contains(txt, id) {
			t.Errorf("attachment of %s: %q, want the entry named", id, txt)
		}
	}
}

func TestInboxFailures(t *testing.T) {
	t.Parallel()

	fake := toolsFake()
	fake.InboxErr = errInbox
	session := connect(t, fake)

	for _, name := range []string{messagesList, messagesWait, markRead} {
		if txt := toolError(t, session, name, nil); !strings.Contains(txt, errInbox.Error()) {
			t.Errorf("%s: %q, want the inbox error", name, txt)
		}
	}

	for _, uri := range []string{chatsURI, chatURIPrefix + aliceACI} {
		_, err := session.ReadResource(t.Context(), &sdk.ReadResourceParams{URI: uri})
		if err == nil || !strings.Contains(err.Error(), errInbox.Error()) {
			t.Errorf("read %s: %v, want the inbox error", uri, err)
		}
	}
}
