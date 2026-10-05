//nolint:lll // Complete wire fixtures and independent result assertions.
package cmd_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	pollRecipientFlag        = "--recipient"
	directPollClearFlag      = "--clear"
	directPollClearOperation = "vote_clear"
)

func TestDirectPollCommands(t *testing.T) {
	t.Parallel()

	for _, operation := range []struct {
		name string
		args []string
	}{
		{pollCreateVerb, []string{pollQuestionFlag, pollQuestionText, pollOptionFlag, pollToday, pollOptionFlag, pollTomorrow}},
		{pollVoteVerb, []string{targetFlg, aliceACI + ":" + targetTS, pollCountFlag, "1", pollOptionFlag, "0"}},
		{directPollClearOperation, []string{targetFlg, aliceACI + ":" + targetTS, pollCountFlag, "2", directPollClearFlag}},
		{pollCloseVerb, []string{targetFlg, targetTS}},
	} {
		for _, format := range []string{formatPlain, formatJSON} {
			t.Run(operation.name+"_"+format, func(t *testing.T) {
				t.Parallel()

				fake := sendFake()
				verb, _, _ := strings.Cut(operation.name, "_")
				args := append([]string{"-o", format, pollCmdName, verb, pollRecipientFlag, aliceNumber}, operation.args...)

				out, err := runSend(t, fake, "", args...)
				if err != nil {
					t.Fatal(err)
				}

				sent := fake.Sent()
				if len(sent) != 1 || sent[0].GroupID != "" || len(sent[0].Recipients) != 1 || sent[0].Recipients[0].ACI != aliceACI {
					t.Fatalf("destination %+v", sent)
				}

				golden(t, "poll_direct_"+operation.name+"_"+format, out)
			})
		}
	}
}

func TestDirectPollCommandPreflight(t *testing.T) {
	t.Parallel()

	for _, destination := range [][]string{
		{pollRecipientFlag, ""},
		{pollRecipientFlag, "invalid"},
		{pollRecipientFlag, aliceACI, pollRecipientFlag, aliceACI},
		{pollRecipientFlag, aliceACI, groupFlag, groupID},
		{pollRecipientFlag, "group:" + groupID},
		{pollRecipientFlag, "00000000-0000-0000-0000-000000000000"},
	} {
		fake := &signaltest.Fake{}

		args := append([]string{pollCmdName, pollCreateVerb, pollQuestionFlag, "Q", pollOptionFlag, pollFirst, pollOptionFlag, pollSecond}, destination...)

		_, err := runSend(t, fake, "", args...)
		if err == nil || errors.Is(err, signal.ErrNotLinked) || len(fake.Connects()) != 0 {
			t.Fatalf("preflight %v: %v", destination, err)
		}
	}
}

func TestDirectPollShowCommand(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := sendFake()

			client, err := fake.Factory(t.Context(), signal.Options{})
			if err != nil {
				t.Fatal(err)
			}

			_, err = client.InboxAdd(t.Context(), signal.InboxEntry{Chat: signal.Chat{Recipient: signal.Recipient{ACI: aliceACI}}, Event: &signal.Message{
				Envelope: signal.Envelope{Sender: signal.Recipient{ACI: aliceACI}, Chat: signal.Chat{Recipient: signal.Recipient{ACI: aliceACI, Number: aliceNumber}}, Timestamp: 1234},
				Poll:     &signal.Poll{Question: "Q", Options: []string{"a", "b"}},
			}})
			if err != nil {
				t.Fatal(err)
			}

			err = client.Close()
			if err != nil {
				t.Fatal(err)
			}

			out, err := runSend(t, fake, "", "-o", format, pollCmdName, showCmd, pollRecipientFlag, aliceACI, targetFlg, aliceACI+":1234")
			if err != nil {
				t.Fatal(err)
			}

			if len(fake.Connects()) != 0 || len(fake.Sent()) != 0 {
				t.Fatal("show connected or sent")
			}

			if format == formatPlain && !strings.HasPrefix(out, "Poll "+aliceACI+":1234 in "+aliceACI+"\n") {
				t.Fatalf("direct chat heading: %s", out)
			}

			golden(t, "poll_direct_show_"+format, out)
		})
	}
}

func TestDirectPollPartialDelivery(t *testing.T) {
	t.Parallel()

	fake := sendFake()
	fake.SendFailures = map[string]error{aliceACI: errUnreachable}

	out, err := runSend(t, fake, "", "-o", formatJSON, pollCmdName, pollCloseVerb, pollRecipientFlag, aliceNumber, targetFlg, targetTS)
	if !errors.Is(err, app.ErrSendFailed) || !strings.Contains(out, `"success":false`) || len(fake.Sent()) != 1 {
		t.Fatalf("outcome %s %v", out, err)
	}
}

func TestDirectPollSelectedAccountAndQueuedEvents(t *testing.T) {
	t.Parallel()

	fake := sendFake()
	selected := *testAccount()
	selected.ACI = carolACI
	selected.Number = "+15550999"
	fake.Linked = append(fake.Linked, selected)
	fake.Incoming = []signal.Event{&signal.Message{
		Envelope: signal.Envelope{Sender: signal.Recipient{ACI: aliceACI}, Chat: signal.Chat{Recipient: signal.Recipient{ACI: aliceACI}}, Timestamp: 1234},
		Poll:     &signal.Poll{Question: "incoming", Options: []string{pollFirst, pollSecond}},
	}}

	out, err := runSend(t, fake, "", "-a", selected.Number, "-o", formatJSON, pollCmdName, pollCreateVerb,
		pollRecipientFlag, app.SelfRecipient, pollQuestionFlag, "Q", pollOptionFlag, pollFirst, pollOptionFlag, pollSecond)
	if err != nil {
		t.Fatal(err)
	}

	sent := fake.Sent()
	if len(sent) != 1 || len(sent[0].Recipients) != 1 || sent[0].Recipients[0].ACI != carolACI || !strings.Contains(out, `"targetAuthor":{"aci":"`+carolACI+`"}`) {
		t.Fatalf("selected send %+v %s", sent, out)
	}

	if fake.Delivered() != 0 || len(fake.Receipts()) != 0 || len(fake.Inbox()) != 0 {
		t.Fatal("poll send consumed or stored queued events, or sent receipts")
	}
}
