//nolint:lll // literal mention fixtures and independent expectations
package app_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

const selfQuote = "self:1789999999000"

func TestSendQuoteTextMentions(t *testing.T) {
	t.Parallel()

	fake := directory()

	_, err := sender(t, fake).Send(t.Context(), app.SendRequest{
		Recipients: []string{aliceNumber}, Body: "Hi @{self}", Quote: selfQuote,
		QuoteText: "😀 @{@bob.42} and @{" + aliceNumber + "}",
	})
	if err != nil {
		t.Fatal(err)
	}

	sent := fake.Sent()

	want := &signal.Quote{
		Author:    signal.Recipient{ACI: testAccount().ACI, Number: testAccount().Number},
		Timestamp: 1789999999000, Text: "😀 \uFFFC and \uFFFC",
		Mentions: []signal.Mention{
			{Start: 3, Length: 1, Recipient: signal.Recipient{ACI: bobACI, Username: bobUsername[1:]}},
			{Start: 9, Length: 1, Recipient: signal.Recipient{ACI: aliceACI, PNI: carolACI, Number: aliceNumber}},
		},
	}
	if len(sent) != 1 || !reflect.DeepEqual(sent[0].Quote, want) || len(sent[0].Mentions) != 1 || sent[0].Mentions[0].Start != 3 {
		t.Errorf("sent %+v, want quote %+v and separate body mention", sent, want)
	}
}

func TestQuoteMentionNotOnSignalPreventsSend(t *testing.T) {
	t.Parallel()

	fake := directory()

	_, err := sender(t, fake).Send(t.Context(), app.SendRequest{
		Recipients: []string{aliceNumber}, Body: "reply", Quote: selfQuote, QuoteText: "@{+4915100000000}",
	})
	if !errors.Is(err, signal.ErrNotOnSignal) || len(fake.Sent()) != 0 {
		t.Errorf("send = %v, sent %+v; want ErrNotOnSignal and no send", err, fake.Sent())
	}
}

func TestEventRecipientsIncludesMentions(t *testing.T) {
	t.Parallel()

	alice, bob := signal.Recipient{ACI: aliceACI}, signal.Recipient{ACI: bobACI}

	mention := []signal.Mention{{Length: 1, Recipient: bob}}
	for _, test := range []struct {
		evt  signal.Event
		want []signal.Recipient
	}{
		{&signal.Message{Mentions: mention, Quote: &signal.Quote{Author: alice, Mentions: mention}}, []signal.Recipient{{}, {}, alice, bob, bob}},
		{&signal.Edit{Mentions: mention}, []signal.Recipient{{}, {}, bob}},
	} {
		if got := app.EventRecipients(test.evt); !slices.Equal(got, test.want) {
			t.Errorf("%T recipients = %+v, want %+v", test.evt, got, test.want)
		}
	}
}
