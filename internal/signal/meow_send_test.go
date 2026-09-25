//go:build cgo

package signal_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
)

const (
	sendACI  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	otherACI = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	otherPNI = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
)

func TestDataMessage(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{7}, 32)

	msg := signal.DataMessage("hello", 1790000000000, key)
	if msg.GetBody() != "hello" || msg.GetTimestamp() != 1790000000000 || !bytes.Equal(msg.GetProfileKey(), key) {
		t.Errorf("got %v", msg)
	}

	if msg.GetGroupV2() != nil || msg.GetExpireTimer() != 0 || msg.GetFlags() != 0 {
		t.Errorf("got extra fields: %v", msg)
	}

	if msg := signal.DataMessage("hi", 1, nil); msg.GetProfileKey() != nil {
		t.Errorf("got profile key %x without one", msg.GetProfileKey())
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

func TestCheckSendRequest(t *testing.T) {
	t.Parallel()

	users := []signal.Recipient{{ACI: sendACI}}
	group := "Z3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXA="

	tests := []struct {
		name string
		req  signal.SendRequest
		want error
	}{
		{"users", signal.SendRequest{Recipients: users, Body: "hi"}, nil},
		{"group", signal.SendRequest{GroupID: group, Body: "hi"}, nil},
		{"neither", signal.SendRequest{Body: "hi"}, signal.ErrInvalidSendRequest},
		{"both", signal.SendRequest{Recipients: users, GroupID: group}, signal.ErrInvalidSendRequest},
		{"attachment", signal.SendRequest{Recipients: users, Attachments: []string{"a.png"}}, signal.ErrNotImplemented},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := signal.CheckSendRequest(test.req)
			if !errors.Is(err, test.want) || (test.want == nil && err != nil) {
				t.Errorf("got %v, want %v", err, test.want)
			}
		})
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
