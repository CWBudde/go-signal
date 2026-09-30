package daemon_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/daemon"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func allowAll(t *testing.T) app.Option {
	t.Helper()

	allowed, err := app.ParseAllowlist([]string{"*"})
	if err != nil {
		t.Fatal(err)
	}

	return app.WithAllowlist(allowed)
}

func TestStrictWriteInput(t *testing.T) {
	t.Parallel()
	server := startServer(t, testFake(), daemon.Options{}, allowAll(t))

	tests := []struct {
		name, path, body string
		headers          map[string]string
		status           int
		code             string
	}{
		{"malformed", messagesPath, `{"text":`, nil, 400, invalidRequestCode},
		{"array", messagesPath, `[]`, nil, 400, invalidRequestCode},
		{nullBody, messagesPath, nullBody, nil, 400, invalidRequestCode},
		{"unknown", messagesPath, `{"recipients":["self"],"text":"hi","attachments":[]}`, nil, 400, invalidRequestCode},
		{"two objects", messagesPath, `{"recipients":["self"],"text":"hi"}{}`, nil, 400, invalidRequestCode},
		{"missing recipients", messagesPath, `{"text":"hi"}`, nil, 400, invalidRequestCode},
		{"blank text", messagesPath, `{"recipients":["self"],"text":" "}`, nil, 400, invalidRequestCode},
		{"wrong field type", messagesPath, `{"recipients":"self","text":"hi"}`, nil, 400, invalidRequestCode},
		{
			"media type", messagesPath, selfSendBody,
			map[string]string{contentTypeHeader: "text/plain"},
			415, unsupportedMediaCode,
		},
		{
			"missing media type", messagesPath, selfSendBody,
			map[string]string{contentTypeHeader: ""},
			415, unsupportedMediaCode,
		},
		{
			"too large", messagesPath, `{"recipients":["self"],"text":"` + strings.Repeat("x", 1<<20) + `"}`,
			nil, 413, "request_too_large",
		},
		{"mark empty", markReadPath, "", map[string]string{contentTypeHeader: "application/json"}, 400, invalidRequestCode},
		{"mark media", markReadPath, `{}`, map[string]string{contentTypeHeader: "text/plain"}, 415, unsupportedMediaCode},
		{"mark unknown", markReadPath, `{"unknown":1}`, nil, 400, invalidRequestCode},
		{"mark null", markReadPath, nullBody, nil, 400, invalidRequestCode},
		{"mark cursor", markReadPath, `{"cursor":"-1"}`, nil, 400, invalidRequestCode},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			response := server.request(t, http.MethodPost, testCase.path, testCase.body, testCase.headers)
			assertError(t, response, testCase.status, testCase.code)
		})
	}

	t.Cleanup(func() {
		if len(server.fake.Sent()) != 0 || len(server.fake.Receipts()) != 0 {
			t.Error("invalid requests performed writes")
		}
	})
}

func TestWritePolicies(t *testing.T) {
	t.Parallel()

	empty, err := app.ParseAllowlist(nil)
	if err != nil {
		t.Fatal(err)
	}

	server := startServer(t, testFake(), daemon.Options{}, app.WithAllowlist(empty))
	assertError(t, server.request(t, http.MethodPost, messagesPath, selfSendBody, nil), 403, "forbidden")

	readOnly := startServer(t, testFake(), daemon.Options{ReadOnly: true}, allowAll(t))
	for _, path := range []string{messagesPath, markReadPath} {
		assertError(t, readOnly.request(t, http.MethodPost, path, selfSendBody, nil), 403, "forbidden")
	}

	if len(server.fake.Sent()) != 0 || len(readOnly.fake.Sent()) != 0 || len(readOnly.fake.Receipts()) != 0 {
		t.Error("policy rejection performed writes")
	}
	// The send allowlist does not restrict explicit read receipts.
	seed(t, server, aliceACI, 1000)

	var read struct {
		OK                bool
		Messages, Senders int
	}
	decodeResponse(t, server.request(t, http.MethodPost, markReadPath, `{}`, nil), &read)

	if !read.OK || read.Messages != 1 || read.Senders != 1 {
		t.Errorf("mark read %+v", read)
	}
}

type sendOutcome struct {
	ACI     string
	Success bool
	Error   string
}

func TestSendPreservesPartialOutcomesWithoutRetry(t *testing.T) {
	t.Parallel()

	fake := testFake()
	fake.SendFailures = map[string]error{bobACI: errDelivery}
	server := startServer(t, fake, daemon.Options{}, allowAll(t))
	bodyJSON := `{"recipients":["` + aliceACI + `","` + bobACI + `"],"text":"hi"}`
	resp := server.request(t, http.MethodPost, messagesPath, bodyJSON, nil)

	var body struct {
		OK   bool
		Send struct {
			Timestamp uint64
			Results   []sendOutcome
		}
		Error struct{ Code string }
	}
	decodeResponse(t, resp, &body)

	if resp.StatusCode != http.StatusOK || body.OK || body.Error.Code != "send_failed" || body.Send.Timestamp == 0 ||
		len(body.Send.Results) != 2 {
		t.Fatalf("partial result %+v, status %d", body, resp.StatusCode)
	}

	want := []sendOutcome{{ACI: aliceACI, Success: true}, {ACI: bobACI, Error: "delivery failed"}}
	if !reflect.DeepEqual(body.Send.Results, want) {
		t.Errorf("outcomes %+v", body.Send.Results)
	}

	sent := fake.Sent()
	if len(sent) != 1 || sent[0].Body != "hi" || len(sent[0].Recipients) != 2 {
		t.Errorf("sent %+v; expected one send", sent)
	}
}

type failingReceiptClient struct {
	signal.Client

	attempts atomic.Int32
}

func (c *failingReceiptClient) SendReceipt(
	ctx context.Context, sender signal.Recipient, typ signal.ReceiptType, timestamps []uint64,
) error {
	c.attempts.Add(1)

	if sender.ACI == bobACI {
		return errReceipt
	}

	return c.Client.SendReceipt(ctx, sender, typ, timestamps) //nolint:wrapcheck // test wrapper preserves the client error
}

func TestMarkReadPreservesPartialCounts(t *testing.T) {
	t.Parallel()

	fake := testFake()

	wrapped := &failingReceiptClient{Client: openClient(t, fake)}
	server := serveClient(t, fake, wrapped, listenLoopback(t), daemon.Options{})

	seed(t, server, aliceACI, 1000)
	seed(t, server, bobACI, 2000)
	resp := server.request(t, http.MethodPost, markReadPath, `{}`, nil)

	var result struct {
		OK                bool
		Messages, Senders int
		Error             struct{ Code string }
	}
	decodeResponse(t, resp, &result)

	if resp.StatusCode != http.StatusOK || result.OK || result.Messages != 1 || result.Senders != 1 ||
		result.Error.Code != "mark_read_failed" {
		t.Errorf("result %+v, status %d", result, resp.StatusCode)
	}

	entries := fake.Inbox()
	if entries[0].Unread || !entries[1].Unread || wrapped.attempts.Load() != 2 {
		t.Errorf("entries %+v, attempts %d", entries, wrapped.attempts.Load())
	}
}

func TestSuccessfulTextSend(t *testing.T) {
	t.Parallel()
	server := startServer(t, testFake(), daemon.Options{}, allowAll(t))
	resp := server.request(t, http.MethodPost, messagesPath, `{"recipients":["self"],"text":"hello"}`, nil)

	var body map[string]json.RawMessage
	decodeResponse(t, resp, &body)

	if resp.StatusCode != http.StatusOK || string(body["ok"]) != "true" || body["error"] != nil || body["send"] == nil {
		t.Errorf("result %s status %d", body, resp.StatusCode)
	}
}

func TestMarkReadCursorBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		cursor            string
		messages, senders int
		unread            []bool
		receipts          []signaltest.ReceiptCall
	}{
		{cursor: "0", unread: []bool{true, true}},
		{cursor: "00", unread: []bool{true, true}},
		{
			cursor: "1", messages: 1, senders: 1, unread: []bool{false, true},
			receipts: []signaltest.ReceiptCall{{
				Sender: signal.Recipient{ACI: aliceACI}, Type: signal.ReceiptRead, Timestamps: []uint64{1000},
			}},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.cursor, func(t *testing.T) {
			t.Parallel()
			server := startServer(t, testFake(), daemon.Options{})
			seed(t, server, aliceACI, 1000)
			seed(t, server, aliceACI, 2000)
			response := server.request(t, http.MethodPost, markReadPath, `{"cursor":"`+testCase.cursor+`"}`, nil)

			var result struct {
				OK                bool
				Messages, Senders int
				Error             *json.RawMessage
			}
			decodeResponse(t, response, &result)

			if response.StatusCode != http.StatusOK {
				t.Errorf("status %d", response.StatusCode)
			}

			want := struct {
				OK                bool
				Messages, Senders int
				Error             *json.RawMessage
			}{OK: true, Messages: testCase.messages, Senders: testCase.senders}
			if !reflect.DeepEqual(result, want) {
				t.Errorf("mark read %+v, want %+v", result, want)
			}

			unread := make([]bool, 0, 2)
			for _, entry := range server.fake.Inbox() {
				unread = append(unread, entry.Unread)
			}

			if !reflect.DeepEqual(unread, testCase.unread) {
				t.Errorf("unread %v, want %v", unread, testCase.unread)
			}

			if receipts := server.fake.Receipts(); !reflect.DeepEqual(receipts, testCase.receipts) {
				t.Errorf("receipts %+v, want %+v", receipts, testCase.receipts)
			}
		})
	}
}
