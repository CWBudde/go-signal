package mcp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/mcp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	editTimestampArg = "editTimestamp"
	correctedText    = "corrected"
)

func TestSendMessageEdit(t *testing.T) {
	t.Parallel()

	fake := writeFake()
	asked := make(chan string, 1)
	session := connectWith(t, fake, mcp.Options{Confirm: true}, testClient{
		options: &sdk.ClientOptions{
			ElicitationHandler: func(_ context.Context, req *sdk.ElicitRequest) (*sdk.ElicitResult, error) {
				asked <- req.Params.Message
				return &sdk.ElicitResult{Action: accept}, nil
			},
		},
	}, allow(t, aliceNumber))

	var out sent
	call(t, session, sendMessage, map[string]any{
		recipientsArg: []string{aliceNumber}, textArg: correctedText, editTimestampArg: 1000,
	}, &out)

	reqs := fake.Sent()
	if len(reqs) != 1 || reqs[0].EditTarget != 1000 || reqs[0].Body != correctedText || reqs[0].Timestamp <= 1000 {
		t.Errorf("edit requests = %+v", reqs)
	}

	question := <-asked
	if !strings.Contains(question, "Edit our message at 1000") || !strings.Contains(question, correctedText) {
		t.Errorf("confirmation = %q", question)
	}
}

func TestSendMessageEditRejected(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		target  any
		allowed bool
		want    string
	}{
		{"zero", 0, true, app.ErrInvalidEdit.Error()},
		{"negative", -1, true, ""},
		{"not a timestamp", "bad", true, ""},
		{"not allowed", 1000, false, app.ErrRecipientNotAllowed.Error()},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := writeFake()

			allowed := []string{}
			if test.allowed {
				allowed = append(allowed, aliceNumber)
			}

			session := connectWith(t, fake, mcp.Options{}, testClient{}, allow(t, allowed...))

			res := callRaw(t, session, sendMessage, map[string]any{
				recipientsArg: []string{aliceNumber}, textArg: correctedText, editTimestampArg: test.target,
			})
			if !res.IsError || !strings.Contains(text(res), test.want) || len(fake.Sent()) != 0 {
				t.Errorf("result = %s; sent %d", text(res), len(fake.Sent()))
			}
		})
	}
}
