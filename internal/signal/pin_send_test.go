//nolint:goconst,lll // complete payload fixtures and discriminating assertions
package signal_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	pinTestGroupID  = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	nilACITestValue = "00000000-0000-0000-0000-000000000000"
)

func TestPinSendStandalone(t *testing.T) {
	t.Parallel()

	pin := &signal.OutgoingPin{TargetAuthor: signal.Recipient{ACI: selfACI}, TargetTimestamp: 42, DurationSeconds: 60}
	unpin := &signal.OutgoingUnpin{TargetAuthor: pin.TargetAuthor, TargetTimestamp: 42}

	destinations := []signal.SendRequest{
		{Recipients: []signal.Recipient{{ACI: "22222222-2222-4222-8222-222222222222"}}},
		{GroupID: pinTestGroupID},
	}
	for _, dest := range destinations {
		for _, remove := range []bool{false, true} {
			req := dest
			if remove {
				req.Unpin = unpin
			} else {
				req.Pin = pin
			}

			err := req.Check()
			if err != nil {
				t.Fatalf("valid request: %v", err)
			}

			for name, change := range map[string]func(*signal.SendRequest){
				"pin body":        func(r *signal.SendRequest) { r.Body = "mixed" },
				"pin attachments": func(r *signal.SendRequest) { r.Attachments = []signal.UploadedAttachment{{}} },
				"pin quote":       func(r *signal.SendRequest) { r.Quote = &signal.Quote{} },
				"pin mentions":    func(r *signal.SendRequest) { r.Mentions = []signal.Mention{{}} },
				"pin reaction":    func(r *signal.SendRequest) { r.Reaction = &signal.OutgoingReaction{} },
				"pin delete":      func(r *signal.SendRequest) { r.DeleteTarget = 1 },
				"pin edit":        func(r *signal.SendRequest) { r.EditTarget = 1 },
				"pin sticker":     func(r *signal.SendRequest) { r.Sticker = &signal.OutgoingSticker{} },
				"pin poll create": func(r *signal.SendRequest) { r.PollCreate = &signal.Poll{Question: "q", Options: []string{"a", "b"}} },
				"pin poll vote": func(r *signal.SendRequest) {
					r.PollVote = &signal.OutgoingPollVote{TargetAuthor: pin.TargetAuthor, TargetTimestamp: 42, VoteCount: 1}
				},
				"pin poll close": func(r *signal.SendRequest) { r.PollClose = &signal.OutgoingPollClose{TargetTimestamp: 42} },
				"pin opposite":   func(r *signal.SendRequest) { r.Pin, r.Unpin = pin, unpin },
			} {
				mixed := req
				change(&mixed)

				if !errors.Is(mixed.Check(), signal.ErrInvalidPin) {
					t.Errorf("%s remove=%t: accepted mixed request or wrong error: %v", name, remove, mixed.Check())
				}
			}
		}
	}

	for _, req := range []signal.SendRequest{
		{Recipients: destinations[0].Recipients, Pin: &signal.OutgoingPin{}},
		{Recipients: destinations[0].Recipients, Unpin: &signal.OutgoingUnpin{}},
	} {
		if !errors.Is(req.Check(), signal.ErrInvalidPin) {
			t.Errorf("accepted malformed payload: %+v", req)
		}
	}
}

func TestPinCodec(t *testing.T) {
	t.Parallel()

	env := signal.Envelope{Sender: signal.Recipient{ACI: "22222222-2222-4222-8222-222222222222"}, Chat: signal.Chat{GroupID: "pin-group"}, Timestamp: 43, Sync: true}
	for _, event := range []signal.Event{
		&signal.Pin{Envelope: env, OutgoingPin: signal.OutgoingPin{TargetAuthor: env.Sender, TargetTimestamp: 42, DurationSeconds: 60}},
		&signal.Pin{Envelope: env, OutgoingPin: signal.OutgoingPin{TargetAuthor: env.Sender, TargetTimestamp: 42, Forever: true}},
		&signal.Unpin{Envelope: env, OutgoingUnpin: signal.OutgoingUnpin{TargetAuthor: env.Sender, TargetTimestamp: 42}},
	} {
		data, err := signal.MarshalEvent(event, env.Chat)
		if err != nil {
			t.Fatal(err)
		}

		got, chat := signal.UnmarshalEvent(data)
		if !reflect.DeepEqual(got, event) || chat != env.Chat {
			t.Fatalf("roundtrip = %#v, %+v; want %#v", got, chat, event)
		}
	}
}

func TestPinLegacyCodec(t *testing.T) {
	t.Parallel()

	legacy := []byte(`{"type":"message","event":{"Timestamp":42,"Body":"old"},"chat":{"GroupID":"legacy"}}`)
	event, chat := signal.UnmarshalEvent(legacy)

	message, ok := event.(*signal.Message)
	if !ok || message.Body != "old" || message.Timestamp != 42 || chat.GroupID != "legacy" {
		t.Fatalf("legacy = %#v, %+v", event, chat)
	}

	legacy = []byte(`{"type":"unsupported","event":{"Type":"pinMessage"},"chat":{"GroupID":"legacy"}}`)
	event, _ = signal.UnmarshalEvent(legacy)

	unsupported, ok := event.(*signal.Unsupported)
	if !ok || unsupported.Type != "pinMessage" {
		t.Fatalf("old unsupported pin = %#v", event)
	}
}
