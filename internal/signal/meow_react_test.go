//go:build cgo && !purego

package signal_test

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"google.golang.org/protobuf/proto"
)

func TestDataMessageReaction(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{7}, 32)
	req := signal.SendRequest{
		Recipients: []signal.Recipient{{ACI: sendACI}},
		Timestamp:  2,
		Reaction: &signal.OutgoingReaction{
			Emoji: "👍", Remove: true, TargetAuthor: signal.Recipient{ACI: otherACI}, TargetTimestamp: 1,
		},
	}

	msg, err := signal.DataMessage(req, nil, key)
	if err != nil {
		t.Fatal(err)
	}

	author := uuid.MustParse(otherACI)

	want := &signalpb.DataMessage{
		Timestamp:               new(uint64(2)),
		ProfileKey:              key,
		RequiredProtocolVersion: new(uint32(signalpb.DataMessage_REACTIONS)),
		Reaction: &signalpb.DataMessage_Reaction{
			Emoji:                 new("👍"),
			Remove:                new(true),
			TargetAuthorAciBinary: author[:],
			TargetSentTimestamp:   new(uint64(1)),
		},
	}
	if !proto.Equal(msg, want) {
		t.Errorf("got %v, want %v", msg, want)
	}

	req.Reaction.TargetAuthor = signal.Recipient{Number: "+15550101"}

	_, err = signal.DataMessage(req, nil, nil)
	if !errors.Is(err, signal.ErrUnresolvable) {
		t.Errorf("author without ACI: got %v, want ErrUnresolvable", err)
	}
}

func TestDataMessageDelete(t *testing.T) {
	t.Parallel()

	msg, err := signal.DataMessage(signal.SendRequest{Timestamp: 2, DeleteTarget: 1}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	want := &signalpb.DataMessage{
		Timestamp: new(uint64(2)),
		Delete:    &signalpb.DataMessage_Delete{TargetSentTimestamp: new(uint64(1))},
	}
	if !proto.Equal(msg, want) {
		t.Errorf("got %v, want %v", msg, want)
	}
}

func TestReceiptContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		typ  signal.ReceiptType
		want signalpb.ReceiptMessage_Type
	}{
		{signal.ReceiptDelivery, signalpb.ReceiptMessage_DELIVERY},
		{signal.ReceiptRead, signalpb.ReceiptMessage_READ},
		{signal.ReceiptViewed, signalpb.ReceiptMessage_VIEWED},
	}

	for _, test := range tests {
		content, err := signal.ReceiptContent(test.typ, []uint64{1, 2})

		receipt := content.GetReceiptMessage()
		if err != nil || receipt.GetType() != test.want || !slices.Equal(receipt.GetTimestamp(), []uint64{1, 2}) {
			t.Errorf("%s: got %v, %v", test.typ, content, err)
		}
	}

	for _, typ := range []signal.ReceiptType{0, signal.ReceiptViewed + 1} {
		_, err := signal.ReceiptContent(typ, []uint64{1})
		if !errors.Is(err, signal.ErrInvalidReceipt) {
			t.Errorf("type %d: got %v, want ErrInvalidReceipt", typ, err)
		}
	}

	_, err := signal.ReceiptContent(signal.ReceiptRead, nil)
	if !errors.Is(err, signal.ErrInvalidReceipt) {
		t.Errorf("no timestamps: got %v, want ErrInvalidReceipt", err)
	}
}
