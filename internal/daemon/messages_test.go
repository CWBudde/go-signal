package daemon_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/daemon"
	"github.com/cwbudde/go-signal/internal/signal"
)

func message(aci string, timestamp uint64) *signal.Message {
	user := signal.Recipient{ACI: aci}

	return &signal.Message{
		Envelope: signal.Envelope{Sender: user, Chat: signal.Chat{Recipient: user}, Timestamp: timestamp}, Body: "hello",
	}
}

func seed(t *testing.T, server *runningServer, aci string, timestamp uint64) {
	t.Helper()

	msg := message(aci, timestamp)

	_, err := server.client.InboxAdd(t.Context(), signal.InboxEntry{
		ReceivedAt: time.Now(), Time: time.UnixMilli(int64(timestamp)), Chat: msg.Chat, Event: msg, Unread: true,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMessageQueryValidation(t *testing.T) {
	t.Parallel()

	server := startServer(t, testFake(), daemon.Options{})
	for _, query := range []string{
		"cursor=-1", "cursor=no", "cursor=9223372036854775808",
		"limit=0", "limit=-1", "limit=201", "limit=no", "since=yesterday", "chat=Unknown%20Group",
	} {
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			assertError(t, server.request(t, http.MethodGet, "/v1/messages?"+query, "", nil), 400, invalidRequestCode)
		})
	}

	for _, query := range []string{"cursor=-1", "cursor=no", "chat=Unknown%20Group"} {
		t.Run("events "+query, func(t *testing.T) {
			t.Parallel()
			assertError(t, server.request(t, http.MethodGet, "/v1/events?"+query, "", nil), 400, invalidRequestCode)
		})
	}
}

func TestMessagesPagingFilteringAndNoReceipts(t *testing.T) {
	t.Parallel()
	server := startServer(t, testFake(), daemon.Options{})
	seed(t, server, aliceACI, 1000)
	seed(t, server, bobACI, 2000)
	seed(t, server, aliceACI, 3000)

	tests := []struct {
		path   string
		ids    string
		cursor string
		more   bool
	}{
		{"/v1/messages?limit=2", "2,3", "3", false},
		{"/v1/messages?cursor=0&limit=2", "1,2", "2", true},
		{"/v1/messages?cursor=2&limit=2", "3", "3", false},
		{"/v1/messages?cursor=3", "", "3", false},
		{"/v1/messages?cursor=0&chat=" + aliceACI, "1,3", "3", false},
		{"/v1/messages?since=1970-01-01T00:00:02Z", "2,3", "3", false},
	}
	for _, testCase := range tests {
		t.Run(testCase.path, func(t *testing.T) {
			t.Parallel()

			var body struct {
				Messages []struct{ ID string }
				Cursor   string
				More     bool
			}

			resp := server.request(t, http.MethodGet, testCase.path, "", nil)
			decodeResponse(t, resp, &body)

			ids := make([]string, 0, len(body.Messages))
			for _, entry := range body.Messages {
				ids = append(ids, entry.ID)
			}

			if resp.StatusCode != http.StatusOK || body.Messages == nil || strings.Join(ids, ",") != testCase.ids ||
				body.Cursor != testCase.cursor || body.More != testCase.more {
				t.Errorf("page %+v status %d", body, resp.StatusCode)
			}
		})
	}

	t.Cleanup(func() {
		if len(server.fake.Receipts()) != 0 {
			t.Error("listing sent read receipts")
		}
	})
}

type failedResolveClient struct {
	signal.Client

	err error
}

func (c *failedResolveClient) Resolve(context.Context, []signal.Recipient) ([]signal.Recipient, error) {
	return nil, c.err
}

func TestChatResolutionKeepsOperationalError(t *testing.T) {
	t.Parallel()

	fake := testFake()

	client := &failedResolveClient{Client: openClient(t, fake), err: signal.ErrNotConnected}
	server := serveClient(t, fake, client, listenLoopback(t), daemon.Options{})

	assertError(t, server.request(t, http.MethodGet, "/v1/messages?chat=%2B49111111111", "", nil), 503, "unavailable")
}
