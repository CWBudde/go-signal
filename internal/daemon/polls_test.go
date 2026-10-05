package daemon_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/daemon"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	pollsPath = "/v1/polls"
	votePath  = "/v1/polls/vote"
	closePath = "/v1/polls/close"
)

func TestDaemonPollLifecycle(t *testing.T) {
	t.Parallel()
	server := startServer(t, testFake(), daemon.Options{}, allowAll(t))

	var created struct {
		OK   bool
		Poll struct {
			Operation       string
			TargetTimestamp uint64
		}
	}

	resp := server.request(t,
		http.MethodPost,
		pollsPath,
		`{"recipient":"self","question":"Lunch?","options":["Yes","No"]}`,
		nil)
	decodeResponse(t, resp, &created)

	if resp.StatusCode != http.StatusOK ||
		!created.OK ||
		created.Poll.Operation != "create" ||
		created.Poll.TargetTimestamp == 0 {
		t.Fatalf("creation %+v, status %d", created, resp.StatusCode)
	}

	var vote struct {
		OK   bool
		Poll struct{ VoteCount uint32 }
	}
	for _, want := range []uint32{1, 2} {
		decodeResponse(t,
			server.request(t,
				http.MethodPost,
				votePath,
				`{"recipient":"self","target":"`+ownACI+`:1000","optionIndexes":[0]}`,
				nil),
			&vote)

		if !vote.OK || vote.Poll.VoteCount != want {
			t.Fatalf("vote %+v", vote)
		}
	}

	var closed struct{ OK bool }
	decodeResponse(t, server.request(t, http.MethodPost, closePath, `{"recipient":"self","timestamp":1000}`, nil), &closed)

	if !closed.OK || len(server.fake.Sent()) != 4 {
		t.Fatalf("close %+v", closed)
	}
}

func TestDaemonPollValidation(t *testing.T) {
	t.Parallel()

	server := startServer(t, testFake(), daemon.Options{}, allowAll(t))
	for _, test := range []struct{ path, body string }{
		{pollsPath, `{"recipient":"self","question":"?","options":["Only"]}`},
		{pollsPath, `{"recipient":"self","question":"?","options":["Yes","No"],"extra":true}`},
		{pollsPath, `{"recipient":"self","groupId":"bad","question":"?","options":["Yes","No"]}`},
		{votePath, `{"recipient":"self","target":"` + ownACI + `:1000","optionIndexes":[0],"voteCount":0}`},
		{votePath, `{"recipient":"self","target":"bad","optionIndexes":[0]}`},
		{votePath, `{"recipient":"self","target":"` + ownACI + `:1000","clear":true,"optionIndexes":[0]}`},
		{closePath, `{"recipient":"self","timestamp":0}`},
	} {
		assertError(t, server.request(t, http.MethodPost, test.path, test.body, nil), 400, invalidRequestCode)
	}

	for _, query := range []string{
		"recipient=" + ownACI + "&target=" + ownACI + ":1000&durable=true&scanLimit=1000",
		"recipient=" + ownACI + "&target=" + ownACI + ":1000&scanLimit=0",
		"recipient=" + ownACI + "&target=" + ownACI + ":1000&scanLimit=10001",
		"recipient=" + ownACI + "&target=" + ownACI + ":1000&durable=bad",
		"recipient=" + ownACI + "&target=" + ownACI + ":1000&target=" + ownACI + ":1001",
		"recipient=self&target=" + ownACI + ":1000",
		"recipient=" + ownACI + "&target=self:1000",
		"recipient=" + ownACI + "&target=" + ownACI + ":1000&extra=true",
	} {
		assertError(t, server.request(t, http.MethodGet, pollsPath+"?"+query, "", nil), 400, invalidRequestCode)
	}

	if len(server.fake.Sent()) != 0 {
		t.Fatal("invalid polls sent")
	}
}

func TestDaemonPollWritePolicies(t *testing.T) {
	t.Parallel()

	for _, readOnly := range []bool{false, true} {
		empty, err := app.ParseAllowlist(nil)
		if err != nil {
			t.Fatal(err)
		}

		server := startServer(t, testFake(), daemon.Options{ReadOnly: readOnly}, app.WithAllowlist(empty))
		for path, body := range map[string]string{
			pollsPath: `{"recipient":"self","question":"?","options":["Yes","No"]}`,
			votePath:  `{"recipient":"self","target":"` + ownACI + `:1000","optionIndexes":[0]}`,
			closePath: `{"recipient":"self","timestamp":1000}`,
		} {
			assertError(t, server.request(t, http.MethodPost, path, body, nil), 403, "forbidden")
			assertError(t,
				server.request(t,
					http.MethodPost,
					path,
					body,
					map[string]string{authorizationHeader: ""}),
				401,
				unauthorizedCode)
		}

		if len(server.fake.Sent()) != 0 {
			t.Fatal("denied polls sent")
		}
	}
}

//nolint:cyclop // verifies lifecycle outcomes and absence of unintended writes.
func TestDaemonPollDurableAfterPruning(t *testing.T) {
	t.Parallel()
	server := startServer(t, testFake(), daemon.Options{ReadOnly: true}, allowAll(t))
	msg := &signal.Message{
		Envelope: signal.Envelope{
			Chat:      signal.Chat{Recipient: signal.Recipient{ACI: aliceACI}},
			Sender:    signal.Recipient{ACI: aliceACI},
			Timestamp: 1000,
		},
		Poll: &signal.Poll{
			Question: "Lunch?",
			Options: []string{
				"Yes",
				"No",
			},
		},
	}

	_, err := server.client.InboxAdd(t.Context(), signal.InboxEntry{ReceivedAt: time.Now(), Chat: msg.Chat, Event: msg})
	if err != nil {
		t.Fatal(err)
	}

	query := pollsPath + "?recipient=" + aliceACI + "&target=" + url.QueryEscape(aliceACI+":1000")

	var result struct{ PollState output.PollStateJSON }
	decodeResponse(t, server.request(t, http.MethodGet, query, "", nil), &result)

	if !result.PollState.CreationPresent || result.PollState.Scanned != 1 {
		t.Fatalf("bounded %+v", result)
	}

	_, err = server.client.InboxPrune(t.Context(), time.Time{}, 1)
	if err != nil {
		t.Fatal(err)
	}

	seed(t, server, aliceACI, 1001)

	_, err = server.client.InboxPrune(t.Context(), time.Time{}, 1)
	if err != nil {
		t.Fatal(err)
	}

	result.PollState = output.PollStateJSON{}
	decodeResponse(t, server.request(t, http.MethodGet, query+"&durable=true", "", nil), &result)

	if !result.PollState.CreationPresent ||
		result.PollState.Source != "durable" ||
		result.PollState.Observations != 1 ||
		result.PollState.Completeness != "unknown" ||
		len(server.fake.Receipts()) != 0 {
		t.Fatalf("durable %+v", result)
	}

	result.PollState = output.PollStateJSON{}
	decodeResponse(t, server.request(t, http.MethodGet, query, "", nil), &result)

	if result.PollState.CreationPresent {
		t.Fatalf("pruned bounded %+v", result)
	}
}

//nolint:cyclop // verifies lifecycle outcomes and absence of unintended writes.
func TestDaemonPollPartialGroupDelivery(t *testing.T) {
	t.Parallel()

	groupID := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	fake := testFake()
	fake.Groups = map[string][]signal.Recipient{groupID: {{ACI: ownACI}, {ACI: aliceACI}, {ACI: bobACI}}}
	fake.SendFailures = map[string]error{bobACI: errDelivery}
	server := startServer(t, fake, daemon.Options{}, allowAll(t))

	for path, body := range map[string]string{
		pollsPath: `{"groupId":"` + groupID + `","question":"?","options":["Yes","No"]}`,
		votePath:  `{"groupId":"` + groupID + `","target":"` + ownACI + `:1000","optionIndexes":[0]}`,
		closePath: `{"groupId":"` + groupID + `","timestamp":1000}`,
	} {
		var result struct {
			OK    bool
			Poll  output.PollSendJSON
			Error struct{ Code string }
		}

		resp := server.request(t, http.MethodPost, path, body, nil)
		decodeResponse(t, resp, &result)

		if resp.StatusCode != http.StatusOK ||
			result.OK ||
			result.Error.Code != "send_failed" ||
			result.Poll.Timestamp == 0 ||
			len(result.Poll.Results) != 1 ||
			len(result.Poll.Results[0].Members) != 2 {
			t.Fatalf("partial %+v", result)
		}

		members := result.Poll.Results[0].Members
		if !members[0].Success || members[1].Success || members[1].Error != errDelivery.Error() {
			t.Fatalf("members %+v", members)
		}
	}

	if len(fake.Sent()) != 3 {
		t.Fatal("partial calls retried")
	}
}

func TestDaemonPollExhaustedCounter(t *testing.T) {
	t.Parallel()
	server := startServer(t, testFake(), daemon.Options{}, allowAll(t))

	var result struct {
		OK   bool
		Poll struct{ VoteCount uint32 }
	}

	body := fmt.Sprintf(`{"recipient":"self","target":"%s:1000","optionIndexes":[0],"voteCount":4294967295}`, ownACI)
	decodeResponse(t, server.request(t, http.MethodPost, votePath, body, nil), &result)

	if !result.OK || result.Poll.VoteCount != 1<<32-1 {
		t.Fatalf("explicit %+v", result)
	}

	assertError(t,
		server.request(t,
			http.MethodPost,
			votePath,
			`{"recipient":"self","target":"`+ownACI+`:1000","optionIndexes":[0]}`,
			nil),
		409,
		"poll_vote_exhausted")

	if len(server.fake.Sent()) != 1 {
		t.Fatal("exhausted vote sent")
	}
}
