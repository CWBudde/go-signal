//go:build cgo || purego

package signal_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"google.golang.org/protobuf/proto"
)

const (
	sendACI  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	otherACI = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	otherPNI = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
)

func TestDataMessage(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{7}, 32)

	msg, err := signal.DataMessage(signal.SendRequest{Body: "hi there", Timestamp: 1790000000000}, nil, key)
	if err != nil {
		t.Fatal(err)
	}

	want := &signalpb.DataMessage{Body: new("hi there"), Timestamp: new(uint64(1790000000000)), ProfileKey: key}
	if !proto.Equal(msg, want) {
		t.Errorf("got %v, want %v", msg, want)
	}
}

func TestDataMessageWithout(t *testing.T) {
	t.Parallel()

	msg, err := signal.DataMessage(signal.SendRequest{Body: "hi", Timestamp: 1}, nil, nil)
	if err != nil || msg.GetProfileKey() != nil {
		t.Errorf("got profile key %x without one (%v)", msg.GetProfileKey(), err)
	}

	// An attachment without text has no body.
	pointer := &signalpb.AttachmentPointer{ContentType: new("image/png")}

	msg, err = signal.DataMessage(signal.SendRequest{Timestamp: 2}, []*signalpb.AttachmentPointer{pointer}, nil)
	if err != nil || msg.Body != nil || len(msg.GetAttachments()) != 1 || msg.GetAttachments()[0] != pointer {
		t.Errorf("got %v (%v)", msg, err)
	}
}

func TestDataMessageQuoteAndMention(t *testing.T) {
	t.Parallel()

	req := signal.SendRequest{
		Body:      "hi \uFFFC",
		Timestamp: 2,
		Quote:     &signal.Quote{Author: signal.Recipient{ACI: otherACI}, Timestamp: 1, Text: "the question"},
		Mentions:  []signal.Mention{{Start: 3, Length: 1, Recipient: signal.Recipient{ACI: sendACI}}},
	}

	msg, err := signal.DataMessage(req, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	quote := msg.GetQuote()
	if quote.GetId() != 1 || quote.GetText() != "the question" || quote.GetType() != signalpb.DataMessage_Quote_NORMAL ||
		uuid.UUID(quote.GetAuthorAciBinary()).String() != otherACI {
		t.Errorf("quote %v", quote)
	}

	ranges := msg.GetBodyRanges()
	if len(ranges) != 1 || ranges[0].GetStart() != 3 || ranges[0].GetLength() != 1 ||
		uuid.UUID(ranges[0].GetMentionAciBinary()).String() != sendACI {
		t.Errorf("body ranges %v", ranges)
	}
}

func TestDataMessageUnresolved(t *testing.T) {
	t.Parallel()

	for _, req := range []signal.SendRequest{
		{Body: "x", Quote: &signal.Quote{Author: signal.Recipient{Number: "+15550101"}, Timestamp: 1}},
		{Body: "\uFFFC", Mentions: []signal.Mention{{Length: 1, Recipient: signal.Recipient{Username: "bob.42"}}}},
	} {
		_, err := signal.DataMessage(req, nil, nil)
		if !errors.Is(err, signal.ErrUnresolvable) {
			t.Errorf("%+v: got %v, want ErrUnresolvable", req, err)
		}
	}
}

func TestPointerMetadata(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(1790000000000)
	att := signal.OutgoingAttachment{ContentType: "image/jpeg", Filename: "photo.jpg", Width: 640, Height: 480}

	got := signal.PointerMetadata(&signalpb.AttachmentPointer{Size: new(uint32(9))}, att, now)
	if got.GetContentType() != "image/jpeg" || got.GetFileName() != "photo.jpg" || got.GetWidth() != 640 ||
		got.GetHeight() != 480 || got.GetUploadTimestamp() != 1790000000000 || got.GetSize() != 9 {
		t.Errorf("got %v", got)
	}

	got = signal.PointerMetadata(&signalpb.AttachmentPointer{}, signal.OutgoingAttachment{ContentType: "text/plain"}, now)
	if got.FileName != nil || got.Width != nil || got.Height != nil {
		t.Errorf("got unknown fields: %v", got)
	}
}

func TestConvertRecipientResult(t *testing.T) {
	t.Parallel()

	rcpt := signal.Recipient{ACI: sendACI, Number: "+15550101"}
	aci := libsignalgo.NewACIServiceID(uuid.MustParse(sendACI))
	sent := func(ok, unidentified bool, err error) signalmeow.SendMessageResult {
		return signalmeow.SendMessageResult{
			WasSuccessful:        ok,
			SuccessfulSendResult: signalmeow.SuccessfulSendResult{Recipient: aci, Unidentified: unidentified},
			FailedSendResult:     signalmeow.FailedSendResult{Recipient: aci, Error: err},
		}
	}

	tests := []struct {
		name string
		self bool
		sent signalmeow.SendMessageResult
		want signal.RecipientResult
	}{
		{"sealed sender", false, sent(true, true, nil), signal.RecipientResult{Recipient: rcpt, Unidentified: true}},
		{"failed", false, sent(false, false, errBoom), signal.RecipientResult{Recipient: rcpt, Err: errBoom}},
		{"note to self", true, sent(true, false, nil), signal.RecipientResult{Recipient: rcpt}},
		{"sync failed", true, sent(false, false, nil), signal.RecipientResult{Recipient: rcpt, Err: signal.ErrSyncFailed}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			checkResult(t, signal.ConvertRecipientResult(rcpt, test.self, test.sent), test.want)
		})
	}

	// A failure without an error still fails.
	got := signal.ConvertRecipientResult(rcpt, false, sent(false, false, nil))
	if got.Err == nil {
		t.Error("failure without error: got success")
	}
}

// checkResult compares got with want, errors with errors.Is.
func checkResult(t *testing.T, got, want signal.RecipientResult) {
	t.Helper()

	if got.Recipient != want.Recipient || got.Unidentified != want.Unidentified ||
		!errors.Is(got.Err, want.Err) || (want.Err == nil) != (got.Err == nil) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestGroupResults(t *testing.T) {
	t.Parallel()

	if got := signal.GroupResults(nil); got != nil {
		t.Errorf("nil result: got %v", got)
	}

	got := signal.GroupResults(&signalmeow.GroupMessageSendResult{
		SuccessfullySentTo: []signalmeow.SuccessfulSendResult{
			{Recipient: libsignalgo.NewACIServiceID(uuid.MustParse(sendACI)), Unidentified: true},
		},
		FailedToSendTo: []signalmeow.FailedSendResult{
			{Recipient: libsignalgo.NewACIServiceID(uuid.MustParse(otherACI)), Error: errBoom},
			{Recipient: libsignalgo.NewPNIServiceID(uuid.MustParse(otherPNI))},
		},
	})

	want := []signal.RecipientResult{
		{Recipient: signal.Recipient{ACI: sendACI}, Unidentified: true},
		{Recipient: signal.Recipient{ACI: otherACI}, Err: errBoom},
		{Recipient: signal.Recipient{PNI: otherPNI}, Err: signal.ErrSendFailed},
	}

	if len(got) != len(want) {
		t.Fatalf("got %+v, want %d members", got, len(want))
	}

	for i := range want {
		checkResult(t, got[i], want[i])
	}
}

func TestACIServiceID(t *testing.T) {
	t.Parallel()

	id, err := signal.ACIServiceID(signal.Recipient{ACI: sendACI})
	if err != nil || id.Type != libsignalgo.ServiceIDTypeACI || id.UUID.String() != sendACI {
		t.Errorf("got %v, %v", id, err)
	}

	for _, rcpt := range []signal.Recipient{{Number: "+15550101"}, {ACI: "not-a-uuid"}} {
		_, err := signal.ACIServiceID(rcpt)
		if !errors.Is(err, signal.ErrUnresolvable) {
			t.Errorf("%+v: got %v, want ErrUnresolvable", rcpt, err)
		}
	}
}
