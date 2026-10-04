//go:build cgo || libsignal_go

package signal_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestMentionsSurviveInboxReopen(t *testing.T) {
	t.Parallel()

	dataDir := seedAccount(t)
	client := openInboxClient(t, dataDir)
	mentions := []signal.Mention{{Start: 3, Length: 1, Recipient: signal.Recipient{ACI: otherACI}}}
	msg := &signal.Message{
		Body: receivedMentionBody, Mentions: mentions,
		Quote: &signal.Quote{Text: receivedMentionBody, Mentions: mentions},
	}

	edit := &signal.Edit{Body: receivedMentionBody, Mentions: mentions}
	for _, evt := range []signal.Event{msg, edit} {
		_, err := client.InboxAdd(t.Context(), signal.InboxEntry{ReceivedAt: time.Unix(100, 0), Event: evt})
		if err != nil {
			t.Fatal(err)
		}
	}

	err := client.Close()
	if err != nil {
		t.Fatal(err)
	}

	client = openInboxClient(t, dataDir)

	entries, err := client.InboxList(t.Context(), signal.InboxQuery{})
	if err != nil || len(entries) != 2 {
		t.Fatalf("inbox = %+v, %v", entries, err)
	}

	if !reflect.DeepEqual(entries[0].Event, msg) || !reflect.DeepEqual(entries[1].Event, edit) {
		t.Errorf("reopened inbox lost mentions: %+v", entries)
	}
}
