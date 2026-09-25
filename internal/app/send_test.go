package app_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	sentAt = 1790000000000
	body   = "hello"
)

// sender returns an App with a fixed clock on an unconnected client of fake.
func sender(t *testing.T, fake *signaltest.Fake) *app.App {
	t.Helper()

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return app.New(client, app.WithClock(func() time.Time { return time.UnixMilli(sentAt) }))
}

func TestSendUsers(t *testing.T) {
	t.Parallel()

	fake := directory()

	res, err := sender(t, fake).Send(t.Context(), app.SendRequest{
		Recipients: []string{aliceNumber, bobUsername, carolACI},
		Body:       body,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if res.Timestamp != sentAt || len(res.Results) != 3 || res.Failed() != 0 {
		t.Fatalf("got %+v", res)
	}

	for _, result := range res.Results {
		if !result.OK() || !result.Unidentified {
			t.Errorf("result %+v: want sealed-sender success", result)
		}
	}

	want := []signal.SendRequest{{
		Recipients: []signal.Recipient{
			{ACI: aliceACI, PNI: carolACI, Number: aliceNumber},
			{ACI: bobACI, Username: bobUsername[1:]},
			{ACI: carolACI},
		},
		Body:      body,
		Timestamp: sentAt,
	}}
	if got := fake.Sent(); !reflect.DeepEqual(got, want) {
		t.Errorf("sent %+v, want %+v", got, want)
	}

	if len(fake.Connects()) != 1 {
		t.Errorf("connects: %v", fake.Connects())
	}
}

func TestSendNoteToSelf(t *testing.T) {
	t.Parallel()

	fake := directory()
	own := testAccount()

	res, err := sender(t, fake).Send(t.Context(), app.SendRequest{
		Recipients: []string{"self", own.Number, own.ACI},
		Body:       "note",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if len(res.Results) != 1 || !res.Results[0].Target.Self || !res.Results[0].OK() {
		t.Fatalf("got %+v, want one note-to-self", res.Results)
	}

	if res.Results[0].Unidentified {
		t.Error("note-to-self sent with sealed sender")
	}

	sent := fake.Sent()
	if len(sent) != 1 || !reflect.DeepEqual(sent[0].Recipients, []signal.Recipient{{ACI: own.ACI, Number: own.Number}}) {
		t.Errorf("sent %+v, want one request to our own ACI", sent)
	}
}

func TestSendGroup(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.Groups = map[string][]signal.Recipient{
		groupID: {{ACI: testAccount().ACI}, {ACI: aliceACI}, {ACI: bobACI}},
	}
	fake.SendFailures = map[string]error{bobACI: errBoom}

	res, err := sender(t, fake).Send(t.Context(), app.SendRequest{
		Recipients: []string{aliceNumber, app.GroupPrefix + groupID},
		Body:       "hi all",
	})
	if !errors.Is(err, app.ErrSendFailed) {
		t.Fatalf("got %v, want ErrSendFailed", err)
	}

	if len(res.Results) != 2 || !res.Results[0].OK() || res.Failed() != 1 {
		t.Fatalf("got %+v", res.Results)
	}

	checkGroupResult(t, res.Results[1])

	sent := fake.Sent()
	if len(sent) != 2 || sent[1].GroupID != groupID || sent[1].Timestamp != sentAt || sent[0].Timestamp != sentAt {
		t.Errorf("sent %+v, want a user and a group request with the same timestamp", sent)
	}
}

// checkGroupResult checks that alice got the group message and bob didn't.
func checkGroupResult(t *testing.T, group app.TargetResult) {
	t.Helper()

	if group.Err != nil || len(group.Members) != 2 || group.FailedMembers() != 1 {
		t.Fatalf("group result %+v: want 2 members (without us), 1 failed", group)
	}

	if !errors.Is(group.Members[1].Err, errBoom) || group.Members[1].Recipient.ACI != bobACI {
		t.Errorf("member %+v: want bob failed", group.Members[1])
	}
}

func TestSendPartialFailure(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.SendFailures = map[string]error{bobACI: errBoom}

	res, err := sender(t, fake).Send(t.Context(), app.SendRequest{
		Recipients: []string{aliceNumber, bobUsername, app.GroupPrefix + groupID},
		Body:       body,
	})
	if !errors.Is(err, app.ErrSendFailed) {
		t.Fatalf("got %v, want ErrSendFailed", err)
	}

	if res.Failed() != 2 || !res.Results[0].OK() {
		t.Fatalf("got %+v, want bob and the unknown group failed", res.Results)
	}

	if !errors.Is(res.Results[1].Err, errBoom) {
		t.Errorf("bob: got %v", res.Results[1].Err)
	}

	if !errors.Is(res.Results[2].Err, signal.ErrUnknownGroup) {
		t.Errorf("group: got %v, want ErrUnknownGroup", res.Results[2].Err)
	}
}

func TestSendNotOnSignal(t *testing.T) {
	t.Parallel()

	fake := directory()

	_, err := sender(t, fake).Send(t.Context(), app.SendRequest{
		Recipients: []string{aliceNumber, "+4915100000000"},
		Body:       body,
	})
	if !errors.Is(err, signal.ErrNotOnSignal) {
		t.Fatalf("got %v, want ErrNotOnSignal", err)
	}

	if len(fake.Sent()) != 0 {
		t.Errorf("sent %+v, want nothing", fake.Sent())
	}
}

func TestSendInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  app.SendRequest
		want error
	}{
		{"empty body", app.SendRequest{Recipients: []string{aliceNumber}, Body: " \n"}, app.ErrEmptyMessage},
		{"no recipients", app.SendRequest{Body: "hi"}, app.ErrNoRecipients},
		{"invalid recipient", app.SendRequest{Recipients: []string{"alice"}, Body: "hi"}, app.ErrInvalidRecipient},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := directory()

			_, err := sender(t, fake).Send(t.Context(), test.req)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}

			if len(fake.Connects()) != 0 {
				t.Error("connected before validating the request")
			}
		})
	}
}

func TestSendConnectionLost(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.Incoming = []signal.Event{
		&signal.Message{Body: "incoming"},
		&signal.Connection{State: signal.StateLoggedOut},
	}

	res, err := sender(t, fake).Send(t.Context(), app.SendRequest{Recipients: []string{carolACI}, Body: body})
	if !errors.Is(err, app.ErrSendFailed) || !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Fatalf("got %v, want ErrSendFailed and ErrDeviceUnlinked", err)
	}

	if res.Failed() != 1 {
		t.Errorf("got %+v", res.Results)
	}

	// Send-only: incoming events stay on the server.
	if fake.Delivered() != 0 {
		t.Errorf("delivered %d events, want 0", fake.Delivered())
	}
}

func TestSendClientError(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.SendErr = errBoom

	res, err := sender(t, fake).Send(t.Context(), app.SendRequest{
		Recipients: []string{aliceNumber, "self"},
		Body:       body,
	})
	if !errors.Is(err, app.ErrSendFailed) {
		t.Fatalf("got %v, want ErrSendFailed", err)
	}

	for _, result := range res.Results {
		if !errors.Is(result.Err, errBoom) {
			t.Errorf("result %+v: want boom", result)
		}
	}
}

func TestSendConnectError(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.ConnectErr = errBoom

	_, err := sender(t, fake).Send(t.Context(), app.SendRequest{Recipients: []string{carolACI}, Body: body})
	if !errors.Is(err, errBoom) || errors.Is(err, app.ErrSendFailed) {
		t.Fatalf("got %v, want the connect error", err)
	}
}
