package mcp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/mcp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestConfirmIdentifiesChatsAndDeclinesBeforeSending(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		chats []string
		label string
	}{
		{"note to self", []string{"self"}, "yourself (note to self)"},
		{"named group", []string{familyTitle}, "group " + familyTitle},
		{"multiple", []string{aliceNumber, familyTitle}, alice + ", group " + familyTitle},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := writeFake()
			asked := make(chan string, 1)
			session := connectWith(t, fake, mcp.Options{Confirm: true, AttachDir: t.TempDir()}, testClient{
				options: &sdk.ClientOptions{
					ElicitationHandler: func(_ context.Context, req *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
						asked <- req.Params.Message

						return &sdk.ElicitResult{Action: "decline"}, nil
					},
				},
			}, allow(t, app.AllowAll))

			res := callRaw(t, session, sendMessage, map[string]any{
				recipientsArg: test.chats, textArg: replyBody, quoteArg: aliceACI + ":1000",
				"attachments": []string{notesFile},
			})
			if !res.IsError || !strings.Contains(text(res), mcp.ErrNotConfirmed.Error()) {
				t.Fatalf("declined call: %+v", res)
			}

			prompt := <-asked
			for _, want := range []string{"a reply", `"reply"`, notesFile, "to " + test.label + "?"} {
				if !strings.Contains(prompt, want) {
					t.Errorf("confirmation %q lacks %q", prompt, want)
				}
			}

			if sent, uploaded := fake.Sent(), fake.Uploaded(); len(sent) != 0 || len(uploaded) != 0 {
				t.Errorf("declined call sent %+v and uploaded %+v", sent, uploaded)
			}
		})
	}
}
