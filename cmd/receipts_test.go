package cmd_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const receiptsFlag = "--send-read-receipts"

func incoming(sender string, timestamp uint64, body string) *signal.Message {
	return &signal.Message{
		Envelope: signal.Envelope{Sender: signal.Recipient{ACI: sender}, Timestamp: timestamp},
		Body:     body,
	}
}

// receiptsFake has messages from alice and bob, a sync transcript and a reaction.
func receiptsFake() *signaltest.Fake {
	own := testAccount()
	sync := incoming(own.ACI, 4, "from my phone")
	sync.Sync = true

	return &signaltest.Fake{
		Linked: []signal.Account{*own},
		Incoming: []signal.Event{
			&signal.Connection{State: signal.StateConnected},
			incoming(aliceACI, 1, "first"),
			incoming(bobACI, 2, "second"),
			incoming(aliceACI, 3, "third"),
			sync,
			&signal.Reaction{Envelope: signal.Envelope{Sender: signal.Recipient{ACI: bobACI}, Timestamp: 5}, Emoji: "👍"},
			&signal.QueueEmpty{},
		},
	}
}

func readReceipt(sender string, timestamps ...uint64) signaltest.ReceiptCall {
	return signaltest.ReceiptCall{Sender: signal.Recipient{ACI: sender}, Type: signal.ReceiptRead, Timestamps: timestamps}
}

func TestReceiveSendsReadReceipts(t *testing.T) {
	t.Parallel()

	fake := receiptsFake()

	out, err := run(t, fake, "receive", "--timeout", "50ms", receiptsFlag)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	if !strings.Contains(out, "third") {
		t.Errorf("output %q", out)
	}

	// The receipts are sent in one batch per sender when receive ends.
	want := []signaltest.ReceiptCall{readReceipt(aliceACI, 1, 3), readReceipt(bobACI, 2)}
	if got := fake.Receipts(); !reflect.DeepEqual(got, want) {
		t.Errorf("receipts %+v, want %+v", got, want)
	}
}

func TestReceiveWithoutReadReceipts(t *testing.T) {
	t.Parallel()

	fake := receiptsFake()

	_, err := run(t, fake, "receive", "--timeout", "50ms")
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	if got := fake.Receipts(); len(got) != 0 {
		t.Errorf("receipts %+v without %s", got, receiptsFlag)
	}
}

func TestReceiveReadReceiptsMax(t *testing.T) {
	t.Parallel()

	fake := receiptsFake()

	_, err := run(t, fake, "receive", "--max", "2", receiptsFlag)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	want := []signaltest.ReceiptCall{readReceipt(aliceACI, 1), readReceipt(bobACI, 2)}
	if got := fake.Receipts(); !reflect.DeepEqual(got, want) {
		t.Errorf("receipts %+v, want %+v", got, want)
	}
}

func TestReceiveReadReceiptsFail(t *testing.T) {
	t.Parallel()

	fake := receiptsFake()
	fake.ReceiptErr = errUnreachable

	out, err := run(t, fake, "receive", "--timeout", "50ms", receiptsFlag)
	if err != nil {
		t.Fatalf("a failed receipt should not fail receive: %v", err)
	}

	if !strings.Contains(out, "third") {
		t.Errorf("output %q", out)
	}
}

func TestReceiveFollowSendsReadReceipts(t *testing.T) {
	t.Parallel()

	fake := receiptsFake()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)

	go func() {
		_, err := runContext(t, ctx, fake, "receive", "--follow", receiptsFlag)
		done <- err
	}()

	// While receive keeps running, the receipts go out after a short delay.
	deadline := time.Now().Add(5 * time.Second)
	for len(fake.Receipts()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if got := fake.Receipts(); len(got) != 2 {
		t.Errorf("receipts %+v, want one per sender while following", got)
	}

	cancel()

	err := <-done
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
}
