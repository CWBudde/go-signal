package app_test

import (
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

func TestSendEdit(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.Groups = map[string][]signal.Recipient{groupID: {{ACI: aliceACI}}}
	fake.SendFailures = map[string]error{aliceACI: errBoom}

	res, err := sender(t, fake).Send(t.Context(), app.SendRequest{
		Recipients: []string{app.SelfRecipient, aliceNumber, app.GroupPrefix + groupID},
		Body:       "Updated @{" + bobUsername + "}", EditTarget: sentAt - 1000,
	})
	if !errors.Is(err, app.ErrSendFailed) || res.Failed() != 2 {
		t.Fatalf("partial edit = %+v, %v", res, err)
	}

	sent := fake.Sent()
	if len(sent) != 2 {
		t.Fatalf("sent %d requests, want users and group", len(sent))
	}

	for _, req := range sent {
		if req.EditTarget != sentAt-1000 || req.Timestamp != sentAt ||
			req.Body != "Updated \uFFFC" || len(req.Mentions) != 1 {
			t.Errorf("edit = %+v", req)
		}
	}
}

func TestSendInvalidEdit(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		target uint64
		body   string
	}{
		{sentAt, body}, {sentAt + 1, body}, {^uint64(0), body}, {sentAt - 1, " \n"},
	} {
		fake := directory()

		_, err := sender(t, fake).Send(t.Context(), app.SendRequest{
			Recipients: []string{aliceNumber}, Body: test.body, EditTarget: test.target,
		})
		if !errors.Is(err, app.ErrInvalidEdit) || len(fake.Connects()) != 0 || len(fake.Sent()) != 0 {
			t.Errorf("edit %+v: %v, connects %d, sends %d", test, err, len(fake.Connects()), len(fake.Sent()))
		}
	}
}
