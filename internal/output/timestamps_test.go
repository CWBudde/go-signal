package output_test

import (
	"bytes"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
)

// TestEventTimestampPrefix catches rounded milliseconds and unsafe uint64 conversions.
func TestEventTimestampPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		timestamp uint64
		prefix    string
	}{
		{"first millisecond", sentAt + 1, "[2026-09-20 14:30:00 CEST; timestamp=1789907400001] "},
		{"same second", sentAt + 999, "[2026-09-20 14:30:00 CEST; timestamp=1789907400999] "},
		{"unknown", 0, ""},
		{"above int64", math.MaxInt64 + 1, "[timestamp=9223372036854775808] "},
		{"maximum uint64", math.MaxUint64, "[timestamp=18446744073709551615] "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			msg := incoming("hi")
			msg.Timestamp = test.timestamp

			got := renderEvent(t, output.Plain, msg)
			want := test.prefix + aliceACI + " → me: hi\n"

			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

// TestReferencedTimestamps catches confusing the event time with the target message time.
func TestReferencedTimestamps(t *testing.T) {
	t.Parallel()

	env := incoming("").Envelope
	env.Timestamp = sentAt + 999
	author := signal.Recipient{ACI: aliceACI}
	quoted := incoming("reply")
	quoted.Quote = &signal.Quote{Author: author, Timestamp: sentAt + 1, Text: "question"}

	tests := []struct {
		name string
		evt  signal.Event
	}{
		{"quote", quoted},
		{"edit", &signal.Edit{Envelope: env, TargetTimestamp: sentAt + 1, Body: "fixed"}},
		{"delete", &signal.Delete{Envelope: env, TargetTimestamp: sentAt + 1}},
		{"reaction", &signal.Reaction{Envelope: env, TargetAuthor: author, TargetTimestamp: sentAt + 1, Emoji: "👍"}},
		{"receipt", &signal.Receipt{Sender: author, Type: signal.ReceiptRead, Timestamps: []uint64{sentAt + 1}}},
		{"read sync", &signal.ReadSync{
			Timestamp: sentAt + 999, Messages: []signal.ReadMark{{Sender: author, Timestamp: sentAt + 1}},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := renderEvent(t, output.Plain, test.evt)
			if !strings.Contains(got, "2026-09-20 14:30:00 CEST; timestamp=1789907400001") {
				t.Errorf("original timestamp missing in %q", got)
			}
		})
	}

	unknown := &signal.Delete{Envelope: env}
	if got := renderEvent(t, output.Plain, unknown); !strings.Contains(got, "[deleted message sent -]") {
		t.Errorf("unknown target: %q", got)
	}
}

// TestMessageTimestampRenderingPaths catches media or inbox output bypassing the event formatter.
func TestMessageTimestampRenderingPaths(t *testing.T) {
	t.Parallel()

	msg := incoming("hi")
	msg.Sync = true
	msg.Chat = signal.Chat{GroupID: "test-group"}

	tests := []struct {
		name  string
		print func(*output.Printer) error
	}{
		{"event", func(p *output.Printer) error { return p.Event(msg) }},
		{"saved attachments", func(p *output.Printer) error { return p.SavedMessage(msg, nil) }},
		{"saved media", func(p *output.Printer) error { return p.SavedMessageMedia(msg, app.MessageMediaResult{}) }},
		{"inbox", func(p *output.Printer) error {
			return p.InboxEntries([]signal.InboxEntry{{ID: 1, Event: msg}})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			err := test.print(output.New(&out, output.Plain, time.UTC))
			if err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(out.String(), "[2026-09-20 12:30:00 UTC; timestamp=1789907400000] me → group:test-group: hi") {
				t.Errorf("timestamp or sync route missing: %q", out.String())
			}
		})
	}
}
