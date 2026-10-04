//nolint:lll // literal mention fixtures and independent expectations
package cmd_test

import (
	"reflect"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func TestReceiveMentions(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			own, alice, bob := signal.Recipient{ACI: testAccount().ACI}, signal.Recipient{ACI: aliceACI}, signal.Recipient{ACI: bobACI}
			fake := &signaltest.Fake{
				Linked: []signal.Account{*testAccount()}, Contacts: namedContacts(),
				GroupTitleCache: map[string]signal.CachedGroup{groupID: {Title: "Family"}},
			}
			events := []signal.Event{
				&signal.Message{
					Envelope: signal.Envelope{Sender: alice, Chat: signal.Chat{GroupID: groupID}, Timestamp: at(1)},
					Body:     "😀 \uFFFC \uFFFC", Mentions: []signal.Mention{{Start: 3, Length: 1, Recipient: bob}, {Start: 5, Length: 1, Recipient: own}},
					Quote: &signal.Quote{Author: bob, Timestamp: at(0), Text: "Hi \uFFFC", Mentions: []signal.Mention{{Start: 3, Length: 1, Recipient: alice}}},
				},
				&signal.Edit{
					Envelope:        signal.Envelope{Sender: own, Chat: signal.Chat{Recipient: bob}, Timestamp: at(2), Sync: true},
					TargetTimestamp: at(1), Body: "\uFFFC!", Mentions: []signal.Mention{{Length: 1, Recipient: alice}},
				},
				&signal.QueueEmpty{},
			}
			golden(t, "receive_mentions_"+format, receiveAllFrom(t, fake, events, "-o", format))
		})
	}
}

func TestSendQuoteMentions(t *testing.T) {
	t.Parallel()

	fake := sendFake()

	_, err := runSend(t, fake, "", sendCmd, aliceNumber, "-m", "reply", "--quote", "self:1789999999000",
		"--quote-text", "😀 @{@bob.42}")
	if err != nil {
		t.Fatal(err)
	}

	want := &signal.Quote{
		Author: signal.Recipient{ACI: testAccount().ACI, Number: testAccount().Number}, Timestamp: 1789999999000,
		Text: "😀 \uFFFC", Mentions: []signal.Mention{{Start: 3, Length: 1, Recipient: signal.Recipient{ACI: bobACI, Username: "@bob.42"[1:]}}},
	}
	if sent := fake.Sent(); len(sent) != 1 || !reflect.DeepEqual(sent[0].Quote, want) {
		t.Errorf("sent %+v, want quote %+v", sent, want)
	}
}
