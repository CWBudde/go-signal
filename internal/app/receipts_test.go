package app_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func message(sender string, timestamp uint64, sync bool) *signal.Message {
	return &signal.Message{Envelope: signal.Envelope{
		Sender: signal.Recipient{ACI: sender}, Timestamp: timestamp, Sync: sync,
	}}
}

func TestReadReceipts(t *testing.T) {
	t.Parallel()

	fake := directory()
	receipts := connected(t, fake).ReadReceipts()

	events := []struct {
		evt  signal.Event
		want bool
	}{
		{message(aliceACI, 1, false), true},
		{message(bobACI, 2, false), true},
		{message(aliceACI, 3, false), true},
		{message(aliceACI, 3, false), true}, // a duplicate is only confirmed once
		{message(testAccount().ACI, 4, true), false},
		{message("", 5, false), false},
		{&signal.Reaction{Envelope: signal.Envelope{Sender: signal.Recipient{ACI: aliceACI}, Timestamp: 6}}, false},
		{&signal.Typing{Envelope: signal.Envelope{Sender: signal.Recipient{ACI: aliceACI}, Timestamp: 7}}, false},
	}

	for i, test := range events {
		if got := receipts.Add(test.evt); got != test.want {
			t.Errorf("event %d: Add = %v, want %v", i, got, test.want)
		}
	}

	if !receipts.Pending() {
		t.Fatal("nothing pending")
	}

	err := receipts.Flush(t.Context())
	if err != nil {
		t.Fatalf("flush: %v", err)
	}

	want := []signaltest.ReceiptCall{
		{Sender: signal.Recipient{ACI: aliceACI}, Type: signal.ReceiptRead, Timestamps: []uint64{1, 3}},
		{Sender: signal.Recipient{ACI: bobACI}, Type: signal.ReceiptRead, Timestamps: []uint64{2}},
	}
	if got := fake.Receipts(); !reflect.DeepEqual(got, want) {
		t.Errorf("receipts %+v, want %+v", got, want)
	}

	if receipts.Pending() {
		t.Error("still pending after Flush")
	}
}

func TestReadReceiptsError(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.ReceiptErr = errBoom
	receipts := connected(t, fake).ReadReceipts()

	receipts.Add(message(aliceACI, 1, false))
	receipts.Add(message(bobACI, 2, false))

	err := receipts.Flush(t.Context())
	if !errors.Is(err, errBoom) {
		t.Fatalf("got %v, want errBoom", err)
	}

	if receipts.Pending() {
		t.Error("failed receipts are kept")
	}
}
