//go:build cgo || libsignal_go

package signal_test

import (
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"google.golang.org/protobuf/proto"
)

func TestEditEnvelope(t *testing.T) {
	t.Parallel()

	msg, err := signal.DataMessage(signal.SendRequest{
		Body: "replacement", Timestamp: 2000, EditTarget: 1000,
		Quote:    &signal.Quote{Author: signal.Recipient{ACI: selfACI}, Timestamp: 500, Text: "quoted"},
		Mentions: []signal.Mention{{Start: 0, Length: 1, Recipient: signal.Recipient{ACI: selfACI}}},
	}, []*signalpb.AttachmentPointer{{FileName: new("photo.png")}}, []byte("profile key"))
	if err != nil {
		t.Fatal(err)
	}

	wrapped := signal.WrapOutgoing(msg, 1000)

	edit := wrapped.GetEditMessage()
	if edit == nil || edit.GetTargetSentTimestamp() != 1000 || !proto.Equal(edit.GetDataMessage(), msg) {
		t.Fatalf("edit envelope = %v", wrapped)
	}

	if wrapped.GetDataMessage() != nil || edit.GetDataMessage().GetTimestamp() != 2000 {
		t.Error("edit sent as an ordinary message or with the original timestamp")
	}

	normal := signal.WrapOutgoing(msg, 0)
	if normal.GetEditMessage() != nil || !proto.Equal(normal.GetDataMessage(), msg) {
		t.Errorf("ordinary envelope = %v", normal)
	}
}
