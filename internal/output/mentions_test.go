//nolint:lll // literal mention fixtures and independent expectations
package output_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
)

const receivedMentionBody = "😀 \uFFFC"

const invalidRangeBody = "😀 \uFFFC x"

func mentionPrinter(buf *bytes.Buffer, format output.Format) *output.Printer {
	printer := output.New(buf, format, time.UTC)
	printer.SetNames(app.NewNames(otherMentionACI, []signal.Contact{
		{Recipient: signal.Recipient{ACI: aliceACI}, ProfileName: "Alice\n\x1b"},
	}))

	return printer
}

const otherMentionACI = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

func TestMentionPlainText(t *testing.T) {
	t.Parallel()

	mentions := []signal.Mention{
		{Start: 5, Length: 1, Recipient: signal.Recipient{ACI: otherMentionACI}},
		{Start: 3, Length: 1, Recipient: signal.Recipient{ACI: aliceACI}},
	}
	wantMentions := append([]signal.Mention(nil), mentions...)

	body := "😀 \uFFFC \uFFFC"
	for _, evt := range []signal.Event{
		&signal.Message{Body: body, Mentions: mentions, Quote: &signal.Quote{Text: body, Mentions: mentions}},
		&signal.Edit{Body: body, Mentions: mentions},
	} {
		var buf bytes.Buffer

		err := mentionPrinter(&buf, output.Plain).Event(evt)
		if err != nil {
			t.Fatal(err)
		}

		want := `😀 @Alice\n\u001b @me`
		if !strings.Contains(buf.String(), want) || strings.Contains(buf.String(), "\uFFFC") {
			t.Errorf("%T rendered %q, want named mentions %q", evt, buf.String(), want)
		}

		if !reflect.DeepEqual(mentions, wantMentions) {
			t.Error("rendering mutated mention order")
		}
	}
}

func TestMentionInvalidRanges(t *testing.T) {
	t.Parallel()

	alice := signal.Recipient{ACI: aliceACI}
	unknown := signal.Recipient{ACI: "cccccccc-cccc-4ccc-8ccc-cccccccccccc"}

	tests := []struct {
		name     string
		mentions []signal.Mention
		want     string
	}{
		{"unknown", []signal.Mention{{Start: 3, Length: 1, Recipient: unknown}}, "😀 @" + unknown.ACI + " x"},
		{"zero length", []signal.Mention{{Start: 3, Recipient: alice}}, invalidRangeBody},
		{"out of bounds", []signal.Mention{{Start: 50, Length: 1, Recipient: alice}}, invalidRangeBody},
		{"overflow", []signal.Mention{{Start: 3, Length: ^uint32(0), Recipient: alice}}, invalidRangeBody},
		{"split surrogate start", []signal.Mention{{Start: 1, Length: 1, Recipient: alice}}, invalidRangeBody},
		{"split surrogate end", []signal.Mention{{Length: 1, Recipient: alice}}, invalidRangeBody},
		{"empty recipient", []signal.Mention{{Start: 3, Length: 1}}, invalidRangeBody},
		{"duplicate", []signal.Mention{{Start: 3, Length: 1, Recipient: unknown}, {Start: 3, Length: 1, Recipient: alice}}, "😀 @" + unknown.ACI + " x"},
		{"overlap", []signal.Mention{{Start: 3, Length: 2, Recipient: unknown}, {Start: 4, Length: 2, Recipient: alice}}, "😀 @" + unknown.ACI + "x"},
		{"invalid then valid", []signal.Mention{{Start: 3, Length: 99, Recipient: alice}, {Start: 3, Length: 1, Recipient: unknown}}, "😀 @" + unknown.ACI + " x"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := renderEvent(t, output.Plain, &signal.Message{Body: invalidRangeBody, Mentions: test.mentions})
			if !strings.HasSuffix(got, ": "+test.want+"\n") {
				t.Errorf("got %q, want text %q", got, test.want)
			}
		})
	}
}

func TestMentionJSONKeepsRawOffsetsAndNames(t *testing.T) {
	t.Parallel()

	mentions := []signal.Mention{{Start: 3, Length: 1, Recipient: signal.Recipient{ACI: aliceACI}}}
	for _, evt := range []signal.Event{
		&signal.Message{Body: receivedMentionBody, Mentions: mentions, Quote: &signal.Quote{Text: receivedMentionBody, Mentions: mentions}},
		&signal.Edit{Body: receivedMentionBody, Mentions: mentions},
	} {
		var buf bytes.Buffer

		err := mentionPrinter(&buf, output.JSON).Event(evt)
		if err != nil {
			t.Fatal(err)
		}

		var doc map[string]json.RawMessage

		err = json.Unmarshal(buf.Bytes(), &doc)
		if err != nil {
			t.Fatal(err)
		}

		want := `[{"start":3,"length":1,"recipient":{"aci":"` + aliceACI + `","name":"Alice\n\u001b"}}]`
		if string(doc["body"]) != `"😀 ￼"` || string(doc["mentions"]) != want || string(doc["version"]) != "1" {
			t.Errorf("%T raw text/metadata changed or absent: %s", evt, buf.String())
		}

		if _, ok := evt.(*signal.Message); ok && strings.Count(buf.String(), `"mentions":`) != 2 {
			t.Errorf("quote metadata absent: %s", buf.String())
		}
	}
}

func TestQuoteMentionExpandedBeforeTruncation(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	printer := output.New(&buf, output.Plain, time.UTC)
	printer.SetNames(app.NewNames("", []signal.Contact{
		{Recipient: signal.Recipient{ACI: aliceACI}, ProfileName: strings.Repeat("A", 50)},
	}))

	err := printer.Event(&signal.Message{Body: "reply", Quote: &signal.Quote{
		Text: "Hi \uFFFC!", Mentions: []signal.Mention{{Start: 3, Length: 1, Recipient: signal.Recipient{ACI: aliceACI}}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	want := "[quote unknown -: Hi @" + strings.Repeat("A", 35) + "…] reply\n"
	if !strings.HasSuffix(buf.String(), want) {
		t.Errorf("got %q, want expanded and truncated quote %q", buf.String(), want)
	}
}

func TestMentionJSONPreservesInvalidOffsets(t *testing.T) {
	t.Parallel()

	mentions := []signal.Mention{
		{Start: 3, Recipient: signal.Recipient{ACI: aliceACI}},
		{Start: 99, Length: 1, Recipient: signal.Recipient{ACI: aliceACI}},
		{Start: 1, Length: 1, Recipient: signal.Recipient{ACI: aliceACI}},
	}
	data := renderEvent(t, output.JSON, &signal.Message{Body: receivedMentionBody, Mentions: mentions})

	var doc struct {
		Mentions []struct{ Start, Length uint32 }
	}

	err := json.Unmarshal([]byte(data), &doc)
	if err != nil {
		t.Fatal(err)
	}

	if len(doc.Mentions) != 3 || doc.Mentions[0].Start != 3 || doc.Mentions[0].Length != 0 ||
		doc.Mentions[1].Start != 99 || doc.Mentions[1].Length != 1 ||
		doc.Mentions[2].Start != 1 || doc.Mentions[2].Length != 1 {
		t.Errorf("JSON dropped or rewrote raw offsets: %s", data)
	}
}
