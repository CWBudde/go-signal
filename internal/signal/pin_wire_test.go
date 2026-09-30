//go:build cgo || libsignal_go

//nolint:cyclop,funlen,goconst,lll // complete payload fixtures and discriminating assertions
package signal_test

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/google/uuid"
)

func TestPinWire(t *testing.T) {
	t.Parallel()

	author := signal.Recipient{ACI: selfACI}
	// The protocol uses a raw 16-byte ACI, without a service-ID type prefix.
	rawAuthor := []byte{0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x41, 0x11, 0x81, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11}

	for _, duration := range []uint32{1, 60, 4294967295, 0} {
		pin := &signal.OutgoingPin{TargetAuthor: author, TargetTimestamp: 42, DurationSeconds: duration, Forever: duration == 0}

		msg, err := signal.DataMessage(signal.SendRequest{Timestamp: 43, Pin: pin}, nil, []byte{1})
		if err != nil {
			t.Fatal(err)
		}

		wire := msg.GetPinMessage()
		if wire == nil || !bytes.Equal(wire.GetTargetAuthorAciBinary(), rawAuthor) || wire.GetTargetSentTimestamp() != 42 || msg.RequiredProtocolVersion != nil || msg.GetTimestamp() != 43 {
			t.Fatalf("pin = %v", msg)
		}

		if duration == 0 {
			mode, ok := wire.GetPinDuration().(*signalpb.DataMessage_PinMessage_PinDurationForever)
			if !ok || !mode.PinDurationForever {
				t.Fatalf("forever = %v", wire)
			}
		} else {
			mode, ok := wire.GetPinDuration().(*signalpb.DataMessage_PinMessage_PinDurationSeconds)
			if !ok || mode.PinDurationSeconds != duration {
				t.Fatalf("seconds = %v", wire)
			}
		}

		pin.TargetTimestamp = 100
		pin.DurationSeconds = 99

		if wire.GetTargetSentTimestamp() != 42 || wire.GetPinDurationSeconds() != duration {
			t.Fatal("wire aliases pin request")
		}
	}

	unpin := &signal.OutgoingUnpin{TargetAuthor: author, TargetTimestamp: 42}

	msg, err := signal.DataMessage(signal.SendRequest{Timestamp: 44, Unpin: unpin}, nil, nil)
	if err != nil || msg.GetUnpinMessage() == nil || !bytes.Equal(msg.GetUnpinMessage().GetTargetAuthorAciBinary(), rawAuthor) || msg.GetUnpinMessage().GetTargetSentTimestamp() != 42 || msg.RequiredProtocolVersion != nil {
		t.Fatalf("unpin = %v, %v", msg, err)
	}
}

func TestPinFreshRecipientMessages(t *testing.T) {
	t.Parallel()
	client := openInboxClient(t, seedAccount(t))
	signal.ConnectOffline(t.Context(), client)

	author := signal.Recipient{ACI: selfACI}
	for _, req := range []signal.SendRequest{
		{Recipients: []signal.Recipient{author}, Pin: &signal.OutgoingPin{TargetAuthor: author, TargetTimestamp: 42, Forever: true}},
		{GroupID: pinTestGroupID, Unpin: &signal.OutgoingUnpin{TargetAuthor: author, TargetTimestamp: 42}},
	} {
		build, err := signal.BuildMessage(t.Context(), client, req)
		if err != nil {
			t.Fatal(err)
		}

		first, second := build(), build()
		if first.GetPinMessage() == nil {
			if first.GetUnpinMessage() == nil || second.GetUnpinMessage() == nil {
				t.Fatalf("missing control: %v", first)
			}

			first.UnpinMessage.TargetAuthorAciBinary[0] = 0
			*first.UnpinMessage.TargetSentTimestamp = 100

			if second.GetUnpinMessage().GetTargetAuthorAciBinary()[0] != 0x11 || second.GetUnpinMessage().GetTargetSentTimestamp() != 42 {
				t.Fatal("recipient unpin payloads alias")
			}

			continue
		}

		first.PinMessage.TargetAuthorAciBinary[0] = 0

		duration, ok := first.GetPinMessage().GetPinDuration().(*signalpb.DataMessage_PinMessage_PinDurationForever)
		if !ok {
			t.Fatalf("duration = %v", first.GetPinMessage().GetPinDuration())
		}

		duration.PinDurationForever = false

		if second.GetPinMessage().GetTargetAuthorAciBinary()[0] != 0x11 || !second.GetPinMessage().GetPinDurationForever() {
			t.Fatal("recipient pin payloads alias")
		}
	}
}

func rawPinMessage() *signalpb.DataMessage {
	return &signalpb.DataMessage{Timestamp: new(uint64(43)), PinMessage: &signalpb.DataMessage_PinMessage{
		TargetAuthorAciBinary: []byte{0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x41, 0x11, 0x81, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11, 0x11}, TargetSentTimestamp: new(uint64(42)), PinDuration: &signalpb.DataMessage_PinMessage_PinDurationSeconds{PinDurationSeconds: 60},
	}}
}

func TestPinConversion(t *testing.T) {
	t.Parallel()

	sender := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	for _, chat := range []string{sender.String(), "pin-group"} {
		for _, sync := range []bool{false, true} {
			ownACI := ""
			if sync {
				ownACI = sender.String()
			}

			raw := rawPinMessage()
			raw.GroupV2, raw.ProfileKey, raw.ExpireTimer = &signalpb.GroupContextV2{}, []byte{1}, new(uint32(60))
			evt := signal.ConvertEvent(chatEvent(sender, chat, raw), ownACI)

			pin, ok := evt.(*signal.Pin)
			if !ok || pin.TargetAuthor.ACI != selfACI || pin.TargetTimestamp != 42 || pin.Timestamp != 43 || pin.DurationSeconds != 60 || pin.Forever || pin.Sync != sync || pin.Sender.ACI != sender.String() || pin.Chat.Key() != map[bool]string{true: "group:" + chat, false: chat}[chat == "pin-group"] {
				t.Fatalf("pin = %#v", evt)
			}

			raw.PinMessage.TargetAuthorAciBinary[0] = 0

			if pin.TargetAuthor.ACI != selfACI {
				t.Fatal("event aliases wire")
			}

			raw = rawPinMessage()
			raw.PinMessage.PinDuration = &signalpb.DataMessage_PinMessage_PinDurationForever{PinDurationForever: true}
			evt = signal.ConvertEvent(chatEvent(sender, chat, raw), ownACI)

			pin, ok = evt.(*signal.Pin)
			if !ok || !pin.Forever || pin.DurationSeconds != 0 {
				t.Fatalf("forever = %#v", evt)
			}

			raw = rawPinMessage()
			raw.UnpinMessage = &signalpb.DataMessage_UnpinMessage{TargetAuthorAciBinary: raw.GetPinMessage().GetTargetAuthorAciBinary(), TargetSentTimestamp: new(uint64(42))}
			raw.PinMessage = nil
			evt = signal.ConvertEvent(chatEvent(sender, chat, raw), ownACI)

			unpin, ok := evt.(*signal.Unpin)
			if !ok || unpin.TargetAuthor.ACI != selfACI || unpin.TargetTimestamp != 42 || unpin.Timestamp != 43 || unpin.Sync != sync {
				t.Fatalf("unpin = %#v", evt)
			}
		}
	}
}

func TestMalformedPinConversion(t *testing.T) {
	t.Parallel()

	sender := uuid.MustParse("22222222-2222-4222-8222-222222222222")

	for name, mutate := range map[string]func(*signalpb.DataMessage){
		"missing timestamp":  func(m *signalpb.DataMessage) { m.Timestamp = nil },
		"zero timestamp":     func(m *signalpb.DataMessage) { m.Timestamp = new(uint64(0)) },
		"missing target":     func(m *signalpb.DataMessage) { m.PinMessage.TargetSentTimestamp = nil },
		"zero target":        func(m *signalpb.DataMessage) { m.PinMessage.TargetSentTimestamp = new(uint64(0)) },
		"pin missing author": func(m *signalpb.DataMessage) { m.PinMessage.TargetAuthorAciBinary = nil },
		"short author":       func(m *signalpb.DataMessage) { m.PinMessage.TargetAuthorAciBinary = []byte{1} },
		"pin nil author":     func(m *signalpb.DataMessage) { m.PinMessage.TargetAuthorAciBinary = make([]byte, 16) },
		"service ID author": func(m *signalpb.DataMessage) {
			m.PinMessage.TargetAuthorAciBinary = append([]byte{0}, m.GetPinMessage().GetTargetAuthorAciBinary()...)
		},
		"missing duration": func(m *signalpb.DataMessage) { m.PinMessage.PinDuration = nil },
		"zero seconds": func(m *signalpb.DataMessage) {
			m.PinMessage.PinDuration = &signalpb.DataMessage_PinMessage_PinDurationSeconds{}
		},
		"false forever": func(m *signalpb.DataMessage) {
			m.PinMessage.PinDuration = &signalpb.DataMessage_PinMessage_PinDurationForever{}
		},
		"pin opposite":       func(m *signalpb.DataMessage) { m.UnpinMessage = &signalpb.DataMessage_UnpinMessage{} },
		"pin body":           func(m *signalpb.DataMessage) { m.Body = new("") },
		"pin attachments":    func(m *signalpb.DataMessage) { m.Attachments = []*signalpb.AttachmentPointer{{}} },
		"pin quote":          func(m *signalpb.DataMessage) { m.Quote = &signalpb.DataMessage_Quote{} },
		"pin mentions":       func(m *signalpb.DataMessage) { m.BodyRanges = []*signalpb.BodyRange{{}} },
		"pin sticker":        func(m *signalpb.DataMessage) { m.Sticker = &signalpb.DataMessage_Sticker{} },
		"pin reaction":       func(m *signalpb.DataMessage) { m.Reaction = &signalpb.DataMessage_Reaction{} },
		"pin delete":         func(m *signalpb.DataMessage) { m.Delete = &signalpb.DataMessage_Delete{} },
		"group call":         func(m *signalpb.DataMessage) { m.GroupCallUpdate = &signalpb.DataMessage_GroupCallUpdate{} },
		"admin delete":       func(m *signalpb.DataMessage) { m.AdminDelete = &signalpb.DataMessage_AdminDelete{} },
		"pin contact":        func(m *signalpb.DataMessage) { m.Contact = []*signalpb.DataMessage_Contact{{}} },
		"payment":            func(m *signalpb.DataMessage) { m.Payment = &signalpb.DataMessage_Payment{} },
		"gift":               func(m *signalpb.DataMessage) { m.GiftBadge = &signalpb.DataMessage_GiftBadge{} },
		"story":              func(m *signalpb.DataMessage) { m.StoryContext = &signalpb.DataMessage_StoryContext{} },
		"pin preview":        func(m *signalpb.DataMessage) { m.Preview = []*signalpb.Preview{{}} },
		"view once":          func(m *signalpb.DataMessage) { m.IsViewOnce = new(true) },
		"unknown flags":      func(m *signalpb.DataMessage) { m.Flags = new(uint32(8)) },
		"pin end session":    func(m *signalpb.DataMessage) { m.Flags = new(uint32(1)) },
		"pin timer update":   func(m *signalpb.DataMessage) { m.Flags = new(uint32(signalpb.DataMessage_EXPIRATION_TIMER_UPDATE)) },
		"pin profile update": func(m *signalpb.DataMessage) { m.Flags = new(uint32(signalpb.DataMessage_PROFILE_KEY_UPDATE)) },
		"pin poll create": func(m *signalpb.DataMessage) {
			m.PollCreate = &signalpb.DataMessage_PollCreate{Question: new("q"), Options: []string{"a", "b"}}
		},
		"pin poll vote":  func(m *signalpb.DataMessage) { m.PollVote = &signalpb.DataMessage_PollVote{} },
		"pin poll close": func(m *signalpb.DataMessage) { m.PollTerminate = &signalpb.DataMessage_PollTerminate{} },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			msg := rawPinMessage()
			mutate(msg)
			evt := signal.ConvertEvent(chatEvent(sender, sender.String(), msg), "")

			unsupported, ok := evt.(*signal.Unsupported)
			if !ok || unsupported.Type != "invalidPin" {
				t.Fatalf("malformed pin = %#v", evt)
			}
		})
	}

	for _, msg := range []*signalpb.DataMessage{
		{Timestamp: new(uint64(43)), UnpinMessage: &signalpb.DataMessage_UnpinMessage{}},
		{Timestamp: new(uint64(43)), UnpinMessage: &signalpb.DataMessage_UnpinMessage{TargetAuthorAciBinary: make([]byte, 16), TargetSentTimestamp: new(uint64(42))}},
	} {
		evt := signal.ConvertEvent(chatEvent(sender, sender.String(), msg), "")

		unsupported, ok := evt.(*signal.Unsupported)
		if !ok || unsupported.Type != "invalidPin" {
			t.Fatalf("malformed unpin = %#v", evt)
		}
	}

	for _, remove := range []bool{false, true} {
		raw := rawPinMessage()
		if remove {
			raw.UnpinMessage = &signalpb.DataMessage_UnpinMessage{TargetAuthorAciBinary: raw.GetPinMessage().GetTargetAuthorAciBinary(), TargetSentTimestamp: raw.GetPinMessage().TargetSentTimestamp}
			raw.PinMessage = nil
		}

		evt := signal.ConvertEvent(chatEvent(sender, sender.String(), &signalpb.EditMessage{DataMessage: raw}), "")

		unsupported, ok := evt.(*signal.Unsupported)
		if !ok || unsupported.Type != "invalidPin" || unsupported.Timestamp != 43 {
			t.Fatalf("edited pin = %#v", evt)
		}
	}
}

func TestPinInboxSurvivesReopen(t *testing.T) {
	t.Parallel()
	dataDir := seedAccount(t)
	client := openInboxClient(t, dataDir)
	env := signal.Envelope{Sender: signal.Recipient{ACI: selfACI}, Chat: signal.Chat{GroupID: "pin-group"}, Timestamp: 43}
	entries := make([]signal.InboxEntry, 0, 3)

	for _, evt := range []signal.Event{
		&signal.Pin{Envelope: env, OutgoingPin: signal.OutgoingPin{TargetAuthor: env.Sender, TargetTimestamp: 42, DurationSeconds: 60}},
		&signal.Pin{Envelope: env, OutgoingPin: signal.OutgoingPin{TargetAuthor: env.Sender, TargetTimestamp: 42, Forever: true}},
		&signal.Unpin{Envelope: env, OutgoingUnpin: signal.OutgoingUnpin{TargetAuthor: env.Sender, TargetTimestamp: 42}},
	} {
		entry, err := client.InboxAdd(t.Context(), signal.InboxEntry{Chat: env.Chat, Event: evt})
		if err != nil {
			t.Fatal(err)
		}

		entries = append(entries, entry)
	}

	err := client.Close()
	if err != nil {
		t.Fatal(err)
	}

	client = openInboxClient(t, dataDir)

	got, err := client.InboxList(t.Context(), signal.InboxQuery{})
	if err != nil || !reflect.DeepEqual(got, entries) {
		t.Fatalf("reopened = %#v, %v; want %#v", got, err, entries)
	}
}

func TestMalformedUnpinConversion(t *testing.T) {
	t.Parallel()

	sender := uuid.MustParse("22222222-2222-4222-8222-222222222222")

	for name, mutate := range map[string]func(*signalpb.DataMessage){
		"missing timestamp":  func(m *signalpb.DataMessage) { m.Timestamp = nil },
		"zero timestamp":     func(m *signalpb.DataMessage) { m.Timestamp = new(uint64(0)) },
		"missing target":     func(m *signalpb.DataMessage) { m.UnpinMessage.TargetSentTimestamp = nil },
		"zero target":        func(m *signalpb.DataMessage) { m.UnpinMessage.TargetSentTimestamp = new(uint64(0)) },
		"pin missing author": func(m *signalpb.DataMessage) { m.UnpinMessage.TargetAuthorAciBinary = nil },
		"short author":       func(m *signalpb.DataMessage) { m.UnpinMessage.TargetAuthorAciBinary = []byte{1} },
		"pin nil author":     func(m *signalpb.DataMessage) { m.UnpinMessage.TargetAuthorAciBinary = make([]byte, 16) },
		"pin opposite":       func(m *signalpb.DataMessage) { m.PinMessage = &signalpb.DataMessage_PinMessage{} },
		"pin body":           func(m *signalpb.DataMessage) { m.Body = new("mixed") },
		"pin attachments":    func(m *signalpb.DataMessage) { m.Attachments = []*signalpb.AttachmentPointer{{}} },
		"pin quote":          func(m *signalpb.DataMessage) { m.Quote = &signalpb.DataMessage_Quote{} },
		"pin mentions":       func(m *signalpb.DataMessage) { m.BodyRanges = []*signalpb.BodyRange{{}} },
		"pin sticker":        func(m *signalpb.DataMessage) { m.Sticker = &signalpb.DataMessage_Sticker{} },
		"pin reaction":       func(m *signalpb.DataMessage) { m.Reaction = &signalpb.DataMessage_Reaction{} },
		"pin delete":         func(m *signalpb.DataMessage) { m.Delete = &signalpb.DataMessage_Delete{} },
		"group call":         func(m *signalpb.DataMessage) { m.GroupCallUpdate = &signalpb.DataMessage_GroupCallUpdate{} },
		"admin delete":       func(m *signalpb.DataMessage) { m.AdminDelete = &signalpb.DataMessage_AdminDelete{} },
		"pin contact":        func(m *signalpb.DataMessage) { m.Contact = []*signalpb.DataMessage_Contact{{}} },
		"payment":            func(m *signalpb.DataMessage) { m.Payment = &signalpb.DataMessage_Payment{} },
		"gift":               func(m *signalpb.DataMessage) { m.GiftBadge = &signalpb.DataMessage_GiftBadge{} },
		"story":              func(m *signalpb.DataMessage) { m.StoryContext = &signalpb.DataMessage_StoryContext{} },
		"pin preview":        func(m *signalpb.DataMessage) { m.Preview = []*signalpb.Preview{{}} },
		"view once":          func(m *signalpb.DataMessage) { m.IsViewOnce = new(true) },
		"pin end session":    func(m *signalpb.DataMessage) { m.Flags = new(uint32(1)) },
		"pin timer update":   func(m *signalpb.DataMessage) { m.Flags = new(uint32(signalpb.DataMessage_EXPIRATION_TIMER_UPDATE)) },
		"pin profile update": func(m *signalpb.DataMessage) { m.Flags = new(uint32(signalpb.DataMessage_PROFILE_KEY_UPDATE)) },
		"unknown flags":      func(m *signalpb.DataMessage) { m.Flags = new(uint32(8)) },
		"pin poll create": func(m *signalpb.DataMessage) {
			m.PollCreate = &signalpb.DataMessage_PollCreate{Question: new("q"), Options: []string{"a", "b"}}
		},
		"pin poll vote":  func(m *signalpb.DataMessage) { m.PollVote = &signalpb.DataMessage_PollVote{} },
		"pin poll close": func(m *signalpb.DataMessage) { m.PollTerminate = &signalpb.DataMessage_PollTerminate{} },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			msg := rawPinMessage()
			msg.UnpinMessage = &signalpb.DataMessage_UnpinMessage{TargetAuthorAciBinary: msg.GetPinMessage().GetTargetAuthorAciBinary(), TargetSentTimestamp: msg.GetPinMessage().TargetSentTimestamp}
			msg.PinMessage = nil
			mutate(msg)
			evt := signal.ConvertEvent(chatEvent(sender, sender.String(), msg), "")

			unsupported, ok := evt.(*signal.Unsupported)
			if !ok || unsupported.Type != "invalidPin" {
				t.Fatalf("malformed unpin = %#v", evt)
			}
		})
	}
}
