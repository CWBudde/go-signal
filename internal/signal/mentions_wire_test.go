//go:build cgo || libsignal_go

//nolint:lll // literal mention fixtures and independent expectations
package signal_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/google/uuid"
)

const receivedMentionBody = "😀 \uFFFC"

//nolint:cyclop // verify both event variants and their separate mention-bearing fields
func TestConvertReceivedMentions(t *testing.T) {
	t.Parallel()

	alice, bob := uuid.MustParse(sendACI), uuid.MustParse(otherACI)
	ranges := []*signalpb.BodyRange{
		{Start: new(uint32(3)), Length: new(uint32(1)), AssociatedValue: &signalpb.BodyRange_MentionAciBinary{MentionAciBinary: bob[:]}},
		{Start: new(uint32(5)), Length: new(uint32(1)), AssociatedValue: &signalpb.BodyRange_MentionAci{MentionAci: sendACI}},
	}
	msg := &signalpb.DataMessage{
		Body: new("😀 \uFFFC \uFFFC"), BodyRanges: ranges,
		Quote: &signalpb.DataMessage_Quote{AuthorAci: new(sendACI), Text: new("😀 \uFFFC \uFFFC"), BodyRanges: ranges},
	}

	want := []signal.Mention{
		{Start: 3, Length: 1, Recipient: signal.Recipient{ACI: otherACI}},
		{Start: 5, Length: 1, Recipient: signal.Recipient{ACI: sendACI}},
	}
	for _, content := range []signalpb.ChatEventContent{msg, &signalpb.EditMessage{DataMessage: msg}} {
		evt := signal.ConvertEvent(chatEvent(alice, alice.String(), content), otherACI)
		switch evt := evt.(type) {
		case *signal.Message:
			if evt.Body != "😀 \uFFFC \uFFFC" || !reflect.DeepEqual(evt.Mentions, want) ||
				evt.Quote == nil || !reflect.DeepEqual(evt.Quote.Mentions, want) {
				t.Errorf("message lost raw text or mention ranges: %+v", evt)
			}
		case *signal.Edit:
			if evt.Body != "😀 \uFFFC \uFFFC" || !reflect.DeepEqual(evt.Mentions, want) {
				t.Errorf("edit lost raw text or mention ranges: %+v", evt)
			}
		default:
			t.Errorf("got %T, want message or edit", evt)
		}
	}
}

func TestConvertQuoteBinaryAuthor(t *testing.T) {
	t.Parallel()

	aci := uuid.MustParse(sendACI)
	msg := &signalpb.DataMessage{Body: new("reply"), Quote: &signalpb.DataMessage_Quote{AuthorAciBinary: aci[:]}}
	evt := signal.ConvertEvent(chatEvent(aci, aci.String(), msg), otherACI)

	message, ok := evt.(*signal.Message)
	if !ok {
		t.Fatalf("got %T, want message", evt)
	}

	if got := message.Quote.Author.ACI; got != sendACI {
		t.Errorf("quote author = %q, want %s", got, sendACI)
	}
}

func TestConvertMentionsSkipsMalformedIdentitiesAndStyles(t *testing.T) {
	t.Parallel()

	aci := uuid.MustParse(sendACI)
	msg := &signalpb.DataMessage{Body: new("\uFFFC"), BodyRanges: []*signalpb.BodyRange{
		nil,
		{AssociatedValue: &signalpb.BodyRange_Style_{Style: signalpb.BodyRange_BOLD}},
		{AssociatedValue: &signalpb.BodyRange_MentionAci{MentionAci: "invalid-mention-ACI"}},
		{AssociatedValue: &signalpb.BodyRange_MentionAci{MentionAci: uuid.Nil.String()}},
		{AssociatedValue: &signalpb.BodyRange_MentionAciBinary{MentionAciBinary: []byte{1}}},
		{AssociatedValue: &signalpb.BodyRange_MentionAciBinary{MentionAciBinary: make([]byte, 16)}},
		{Length: new(uint32(1)), AssociatedValue: &signalpb.BodyRange_MentionAci{MentionAci: strings.ToUpper(sendACI)}},
	}}
	evt := signal.ConvertEvent(chatEvent(aci, aci.String(), msg), otherACI)
	want := []signal.Mention{{Length: 1, Recipient: signal.Recipient{ACI: sendACI}}}

	message, ok := evt.(*signal.Message)
	if !ok {
		t.Fatalf("got %T, want message", evt)
	}

	if got := message.Mentions; !reflect.DeepEqual(got, want) {
		t.Errorf("mentions = %+v, want %+v", got, want)
	}
}

func TestDataMessageQuoteMentions(t *testing.T) {
	t.Parallel()

	req := signal.SendRequest{Body: "reply", Quote: &signal.Quote{
		Author: signal.Recipient{ACI: otherACI}, Timestamp: 1, Text: receivedMentionBody,
		Mentions: []signal.Mention{{Start: 3, Length: 1, Recipient: signal.Recipient{ACI: sendACI}}},
	}}

	msg, err := signal.DataMessage(req, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	ranges := msg.GetQuote().GetBodyRanges()

	aci := uuid.MustParse(sendACI)
	if len(ranges) != 1 || ranges[0].GetStart() != 3 || ranges[0].GetLength() != 1 ||
		!reflect.DeepEqual(ranges[0].GetMentionAciBinary(), aci[:]) {
		t.Errorf("quote ranges = %v", ranges)
	}
}
