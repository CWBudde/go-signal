package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	aliceACI = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	// sentAt is 2026-09-20 12:30:00 UTC in ms.
	sentAt = 1789907400000
)

func renderEvent(t *testing.T, format output.Format, evt signal.Event) string {
	t.Helper()

	var out bytes.Buffer

	err := output.New(&out, format, time.FixedZone("CEST", 2*60*60)).Event(evt)
	if err != nil {
		t.Fatal(err)
	}

	return out.String()
}

func incoming(body string) *signal.Message {
	alice := signal.Recipient{ACI: aliceACI}

	return &signal.Message{
		Envelope: signal.Envelope{Sender: alice, Chat: signal.Chat{Recipient: alice}, Timestamp: sentAt},
		Body:     body,
	}
}

func TestEventPlainUsesLocation(t *testing.T) {
	t.Parallel()

	evt := &signal.Edit{Envelope: incoming("").Envelope, TargetTimestamp: sentAt - 60_000, Body: "fixed"}

	got := renderEvent(t, output.Plain, evt)
	want := "[2026-09-20 14:30:00 CEST] " + aliceACI +
		" → me: [edit of message sent 2026-09-20 14:29:00 CEST] fixed\n"

	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestEventPlainEscapesControlCharacters(t *testing.T) {
	t.Parallel()

	got := renderEvent(t, output.Plain, incoming("line 1\nline 2\r\t\x1b[31mred\u202e"))

	if strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") {
		t.Errorf("not a single line: %q", got)
	}

	want := `line 1\nline 2\r\t\u001b[31mred\u202e`
	if !strings.HasSuffix(got, ": "+want+"\n") {
		t.Errorf("got %q, want the body escaped as %q", got, want)
	}
}

func TestEventPlainAttachmentSizes(t *testing.T) {
	t.Parallel()

	msg := incoming("")
	for _, size := range []uint32{0, 999, 1000, 12_345, 3_400_000, 2_000_000_000} {
		msg.Attachments = append(msg.Attachments, signal.Attachment{ContentType: "application/pdf", Size: size})
	}

	got := renderEvent(t, output.Plain, msg)
	for _, want := range []string{
		"[attachment application/pdf]",
		"[attachment application/pdf 999 B]",
		"[attachment application/pdf 1.0 KB]",
		"[attachment application/pdf 12.3 KB]",
		"[attachment application/pdf 3.4 MB]",
		"[attachment application/pdf 2.0 GB]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestEventPlainSkipsConnectionEvents(t *testing.T) {
	t.Parallel()

	for _, evt := range []signal.Event{&signal.Connection{State: signal.StateConnected}, &signal.QueueEmpty{}} {
		if got := renderEvent(t, output.Plain, evt); got != "" {
			t.Errorf("%T printed %q", evt, got)
		}

		if got := renderEvent(t, output.JSON, evt); got == "" {
			t.Errorf("%T missing in JSON", evt)
		}
	}
}

func TestEventJSON(t *testing.T) {
	t.Parallel()

	got := renderEvent(t, output.JSON, incoming("a <b> & c\nd"))

	if strings.Count(got, "\n") != 1 {
		t.Errorf("not one NDJSON line: %q", got)
	}

	if !strings.Contains(got, `"body":"a <b> & c\nd"`) {
		t.Errorf("body not written as is: %s", got)
	}

	var doc map[string]any

	err := json.Unmarshal([]byte(got), &doc)
	if err != nil {
		t.Fatal(err)
	}

	for field, want := range map[string]any{
		"version": float64(output.SchemaVersion),
		"type":    "message",
		"time":    "2026-09-20T12:30:00Z",
		"sync":    false,
	} {
		if doc[field] != want {
			t.Errorf("%s = %v, want %v", field, doc[field], want)
		}
	}
}

func TestEventJSONReceiptWithoutTimestamps(t *testing.T) {
	t.Parallel()

	got := renderEvent(t, output.JSON, &signal.Receipt{Sender: signal.Recipient{ACI: aliceACI}, Type: signal.ReceiptRead})

	if !strings.Contains(got, `"receiptType":"read","timestamps":[]`) {
		t.Errorf("unexpected receipt: %s", got)
	}
}
