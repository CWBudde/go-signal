package signaltest_test

import (
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestFakeRejectsUnresolvedQuoteMention(t *testing.T) {
	t.Parallel()

	fake := profileFake()
	cli := profileClient(t, fake, "+12025550101", true)

	_, err := cli.Send(t.Context(), signal.SendRequest{
		Recipients: []signal.Recipient{{ACI: profileBobACI}}, Body: "reply",
		Quote: &signal.Quote{
			Author: signal.Recipient{ACI: profileAliceACI}, Text: "\uFFFC",
			Mentions: []signal.Mention{{Length: 1, Recipient: signal.Recipient{Username: "unresolved.42"}}},
		},
	})
	if !errors.Is(err, signal.ErrUnresolvable) || len(fake.Sent()) != 0 {
		t.Errorf("send = %v, sent %+v; want ErrUnresolvable and no send", err, fake.Sent())
	}
}
