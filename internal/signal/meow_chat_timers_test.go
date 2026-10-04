//go:build cgo || libsignal_go

//nolint:lll // explicit wire fixtures and callback assertions keep protocol fields together
package signal_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/events"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

const (
	timerBody        = "timer text"
	timerRichBody    = "rich replacement"
	timerContentType = "image/webp"
	timerFilename    = "timer-photo.webp"
)

func timerEvent(sender, chat string, msg signalpb.ChatEventContent) *events.ChatEvent {
	return &events.ChatEvent{Info: events.MessageInfo{Sender: uuid.MustParse(sender), ChatID: chat}, Event: msg}
}

func timerMessage(seconds, version uint32) *signalpb.DataMessage {
	return &signalpb.DataMessage{Body: new(timerBody), ExpireTimer: new(seconds), ExpireTimerVersion: new(version)}
}

func wantChatTimer(t *testing.T, dataDir, aci string, seconds, version uint32, found bool) {
	t.Helper()
	withStore(t, dataDir, func(_ *mstore.Device, data *store.Store) {
		got, ok, err := data.ChatTimer(t.Context(), aci)
		if err != nil || ok != found || (found && (got.Seconds != seconds || got.Version != version)) {
			t.Fatalf("timer %s = %+v, %v, %v; want %d/%d found=%v", aci, got, ok, err, seconds, version, found)
		}
	})
}

func mergeTimer(t *testing.T, dataDir, aci string, seconds, version uint32) {
	t.Helper()
	withStore(t, dataDir, func(_ *mstore.Device, data *store.Store) {
		err := data.MergeChatTimer(t.Context(), store.ChatTimerRecord{ACI: aci, Seconds: seconds, Version: version})
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestHandleChatTimers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, sender, chat, key string
		msg                     signalpb.ChatEventContent
		seconds, version        uint32
	}{
		{"sender", aliceUser, bobUser, aliceUser, timerMessage(30, 2), 30, 2},
		{"sync destination", seededACI, aliceUser, aliceUser, timerMessage(300, 4), 300, 4},
		{"own transcript", seededACI, seededACI, seededACI, timerMessage(60, 3), 60, 3},
		{"empty body present", aliceUser, aliceUser, aliceUser, &signalpb.DataMessage{Body: new(""), ExpireTimer: new(uint32(30)), ExpireTimerVersion: new(uint32(2))}, 30, 2},
		{"explicit update", aliceUser, aliceUser, aliceUser, &signalpb.DataMessage{Flags: new(uint32(signalpb.DataMessage_EXPIRATION_TIMER_UPDATE)), ExpireTimer: new(uint32(90)), ExpireTimerVersion: new(uint32(6))}, 90, 6},
		{"nested edit", aliceUser, aliceUser, aliceUser, &signalpb.EditMessage{TargetSentTimestamp: new(uint64(1)), DataMessage: timerMessage(45, 7)}, 45, 7},
		{"sync edit", seededACI, bobUser, bobUser, &signalpb.EditMessage{DataMessage: timerMessage(120, 8)}, 120, 8},
		{"versioned default disables", aliceUser, aliceUser, aliceUser, &signalpb.DataMessage{Body: new(timerBody), ExpireTimerVersion: new(uint32(5))}, 0, 5},
		{"explicit default disables", aliceUser, aliceUser, aliceUser, &signalpb.DataMessage{Flags: new(uint32(signalpb.DataMessage_EXPIRATION_TIMER_UPDATE))}, 0, 0},
		{"legacy initializes", aliceUser, aliceUser, aliceUser, &signalpb.DataMessage{Body: new(timerBody), ExpireTimer: new(uint32(15))}, 15, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dataDir := seedAccount(t)

			client := openOffline(t, dataDir, signal.SendOnly())
			if signal.Handle(client, timerEvent(test.sender, test.chat, test.msg)) {
				t.Fatal("send-only message acked")
			}

			wantChatTimer(t, dataDir, test.key, test.seconds, test.version, true)

			if test.key != seededACI {
				wantChatTimer(t, dataDir, seededACI, 0, 0, false)
			}
		})
	}
}

func TestHandleChatTimerHighWater(t *testing.T) {
	t.Parallel()
	dataDir := seedAccount(t)
	client := openOffline(t, dataDir, signal.SendOnly())

	updates := []struct{ seconds, version, wantSeconds, wantVersion uint32 }{
		{30, 2, 30, 2}, {30, 4, 30, 4}, {300, 3, 30, 4}, {120, 4, 30, 4}, {0, 5, 0, 5}, {60, 0, 0, 5},
	}
	for _, update := range updates {
		signal.Handle(client, timerEvent(aliceUser, aliceUser, timerMessage(update.seconds, update.version)))
		wantChatTimer(t, dataDir, aliceUser, update.wantSeconds, update.wantVersion, true)
	}
	// Neither missing metadata nor an unordered legacy update resets known state.
	signal.Handle(client, timerEvent(aliceUser, aliceUser, &signalpb.DataMessage{Body: new(timerBody)}))
	signal.Handle(client, timerEvent(aliceUser, aliceUser, &signalpb.DataMessage{Body: new(timerBody), ExpireTimer: new(uint32(90))}))
	wantChatTimer(t, dataDir, aliceUser, 0, 5, true)
	signal.Handle(client, timerEvent(bobUser, bobUser, &signalpb.DataMessage{Body: new("hi"), ExpireTimer: new(uint32(15))}))
	signal.Handle(client, timerEvent(bobUser, bobUser, &signalpb.DataMessage{Body: new("hi"), ExpireTimer: new(uint32(45))}))
	wantChatTimer(t, dataDir, bobUser, 15, 0, true)
}

func TestHandleChatTimerMalformed(t *testing.T) {
	t.Parallel()

	legacy := timerMessage(60, 9)
	legacy.ProtoReflect().SetUnknown(protowire.AppendBytes(protowire.AppendTag(nil, 3, protowire.BytesType), []byte{10, 0}))

	group := timerMessage(60, 9)
	group.GroupV2 = &signalpb.GroupContextV2{}

	tests := []struct {
		name, sender, chat string
		msg                signalpb.ChatEventContent
	}{
		{"group v2", aliceUser, aliceUser, group},
		{"legacy group", aliceUser, aliceUser, legacy},
		{"bodyless", aliceUser, aliceUser, &signalpb.DataMessage{ExpireTimer: new(uint32(60)), ExpireTimerVersion: new(uint32(9))}},
		{"bodyless reaction", aliceUser, aliceUser, &signalpb.DataMessage{Reaction: &signalpb.DataMessage_Reaction{}, ExpireTimer: new(uint32(60)), ExpireTimerVersion: new(uint32(9))}},
		{"attachment only", aliceUser, aliceUser, &signalpb.DataMessage{Attachments: []*signalpb.AttachmentPointer{{}}, ExpireTimer: new(uint32(60)), ExpireTimerVersion: new(uint32(9))}},
		{"missing metadata", aliceUser, aliceUser, &signalpb.DataMessage{Body: new("hi")}},
		{"missing nested edit", aliceUser, aliceUser, &signalpb.EditMessage{}},
		{"nil nested edit", aliceUser, aliceUser, (*signalpb.EditMessage)(nil)},
		{"nil sender", uuid.Nil.String(), aliceUser, timerMessage(60, 9)},
		{"missing destination", seededACI, "", timerMessage(60, 9)},
		{"invalid destination", seededACI, "invalid-destination", timerMessage(60, 9)},
		{"nil destination", seededACI, uuid.Nil.String(), timerMessage(60, 9)},
		{"pni destination", seededACI, "PNI:" + bobUser, timerMessage(60, 9)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dataDir := seedAccount(t)
			mergeTimer(t, dataDir, aliceUser, 30, 2)
			client := openOffline(t, dataDir, signal.SendOnly())
			signal.Handle(client, timerEvent(test.sender, test.chat, test.msg))
			wantChatTimer(t, dataDir, aliceUser, 30, 2, true)
			wantChatTimer(t, dataDir, seededACI, 0, 0, false)
		})
	}
}

// timerSQL uses the SQLite driver registered by the selected production backend.
func timerSQL(t *testing.T, dataDir string) *sql.DB {
	t.Helper()

	driver := "sqlite"

	for _, name := range sql.Drivers() {
		if name == "sqlite3" {
			driver = name
		}
	}

	database, err := sql.Open(driver, filepath.Join(dataDir, seededACI, "account.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	return database
}

func execTimerSQL(t *testing.T, db *sql.DB, query string) {
	t.Helper()

	_, err := db.ExecContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
}

func receiveTimer(t *testing.T, client signal.Client, raw events.SignalEvent) signal.Event {
	t.Helper()

	ack := make(chan bool, 1)
	go func() { ack <- signal.Handle(client, raw) }()

	select {
	case evt := <-client.Events():
		if !<-ack {
			t.Fatal("delivered timer event was not acked")
		}

		return evt
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
		return nil
	}
}

func TestHandleChatTimerUnsupported(t *testing.T) {
	t.Parallel()
	dataDir := seedAccount(t)
	client := openOffline(t, dataDir)
	evt := receiveTimer(t, client, timerEvent(aliceUser, aliceUser, &signalpb.DataMessage{Flags: new(uint32(signalpb.DataMessage_EXPIRATION_TIMER_UPDATE)), ExpireTimer: new(uint32(30)), ExpireTimerVersion: new(uint32(2))}))

	unsupported, ok := evt.(*signal.Unsupported)
	if !ok || unsupported.Type != "expirationTimerUpdate" {
		t.Fatalf("event = %+v", evt)
	}

	wantChatTimer(t, dataDir, aliceUser, 30, 2, true)
}

func contactTimer(aci string, seconds, version uint32) events.ContactTimer {
	return events.ContactTimer{ACI: uuid.MustParse(aci), ExpireTimer: new(seconds), ExpireTimerVersion: new(version)}
}

func TestHandleContactTimers(t *testing.T) {
	t.Parallel()
	dataDir := seedAccount(t)
	client := openOffline(t, dataDir)
	mergeTimer(t, dataDir, aliceUser, 30, 2)

	list := &events.ContactList{Timers: []events.ContactTimer{
		contactTimer(aliceUser, 0, 5), contactTimer(bobUser, 300, 4), contactTimer(uuid.Nil.String(), 15, 2),
		{ACI: uuid.MustParse(carolUser), ExpireTimer: new(uint32(60))},
		{ACI: uuid.MustParse(daveUser), ExpireTimerVersion: new(uint32(6))},
	}}
	if !signal.Handle(client, list) {
		t.Fatal("valid contact list not acked")
	}

	wantChatTimer(t, dataDir, aliceUser, 0, 5, true)
	wantChatTimer(t, dataDir, bobUser, 300, 4, true)
	wantChatTimer(t, dataDir, carolUser, 0, 0, false)
	wantChatTimer(t, dataDir, daveUser, 0, 0, false)

	list.IsFromDB = true

	list.Timers = []events.ContactTimer{contactTimer(aliceUser, 120, 9)}
	if !signal.Handle(client, list) {
		t.Fatal("storage contact list not acked")
	}

	wantChatTimer(t, dataDir, aliceUser, 0, 5, true)
}

func TestHandleContactTimersDuplicates(t *testing.T) {
	t.Parallel()
	dataDir := seedAccount(t)
	client := openOffline(t, dataDir)

	list := &events.ContactList{Timers: []events.ContactTimer{
		contactTimer(aliceUser, 30, 2), contactTimer(aliceUser, 30, 4), contactTimer(aliceUser, 300, 3),
		contactTimer(aliceUser, 120, 4), contactTimer(bobUser, 15, 0), contactTimer(bobUser, 60, 0),
	}}
	for range 2 {
		if !signal.Handle(client, list) {
			t.Fatal("contact list not acked")
		}
	}

	wantChatTimer(t, dataDir, aliceUser, 30, 4, true)
	wantChatTimer(t, dataDir, bobUser, 15, 0, true)
}

func TestHandleChatTimerPersistenceFailure(t *testing.T) {
	t.Parallel()
	dataDir := seedAccount(t)
	client := openOffline(t, dataDir)
	database := timerSQL(t, dataDir)
	execTimerSQL(t, database, `CREATE TRIGGER fail_timer BEFORE INSERT ON gosignal_chat_timers BEGIN SELECT RAISE(ABORT, 'test timer write failure'); END`)

	raw := timerEvent(aliceUser, aliceUser, timerMessage(30, 2))
	// If persistence is skipped, delivery becomes observable before the handler returns.
	done := make(chan bool, 1)
	go func() { done <- signal.Handle(client, raw) }()

	select {
	case ack := <-done:
		if ack {
			t.Fatal("failed persistence acked")
		}
	case evt := <-client.Events():
		t.Fatalf("failed persistence emitted %+v", evt)
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}

	if signal.Acked(client) {
		t.Fatal("failed persistence marked delivered")
	}

	wantChatTimer(t, dataDir, aliceUser, 0, 0, false)
	execTimerSQL(t, database, `DROP TRIGGER fail_timer`)
	receiveTimer(t, client, raw)
	wantChatTimer(t, dataDir, aliceUser, 30, 2, true)
}

func TestHandleContactTimerPersistenceFailure(t *testing.T) {
	t.Parallel()
	dataDir := seedAccount(t)
	client := openOffline(t, dataDir)

	arrived, handle, stop := signal.ContactListHook(client)
	defer stop()

	database := timerSQL(t, dataDir)
	execTimerSQL(t, database, `CREATE TRIGGER fail_timer BEFORE INSERT ON gosignal_chat_timers WHEN NEW.aci='bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb' BEGIN SELECT RAISE(ABORT, 'test contact timer write failure'); END`)

	list := &events.ContactList{Timers: []events.ContactTimer{contactTimer(aliceUser, 30, 2), contactTimer(bobUser, 300, 4)}}
	if handle(list) {
		t.Fatal("failed contact persistence acked")
	}

	select {
	case n := <-arrived:
		t.Fatalf("failed persistence completed contact sync: %d", n)
	default:
	}

	if signal.Acked(client) {
		t.Fatal("failed contact persistence marked delivered")
	}

	wantChatTimer(t, dataDir, aliceUser, 0, 0, false)
	wantChatTimer(t, dataDir, bobUser, 0, 0, false)
	execTimerSQL(t, database, `DROP TRIGGER fail_timer`)

	if !handle(list) {
		t.Fatal("contact replay not acked")
	}

	select {
	case <-arrived:
	default:
		t.Fatal("contact replay did not complete sync")
	}

	wantChatTimer(t, dataDir, aliceUser, 30, 2, true)
	wantChatTimer(t, dataDir, bobUser, 300, 4, true)
}

func TestHandleChatTimerSendOnly(t *testing.T) {
	t.Parallel()
	dataDir := seedAccount(t)

	client := openOffline(t, dataDir, signal.SendOnly())
	if signal.Handle(client, timerEvent(aliceUser, aliceUser, timerMessage(30, 2))) {
		t.Fatal("send-only message acked")
	}

	wantChatTimer(t, dataDir, aliceUser, 30, 2, true)

	if !signal.Handle(client, &events.ContactList{Timers: []events.ContactTimer{contactTimer(bobUser, 300, 4)}}) {
		t.Fatal("send-only contacts not acked")
	}

	wantChatTimer(t, dataDir, bobUser, 300, 4, true)

	if signal.Acked(client) {
		t.Fatal("send-only learned timers counted as delivered")
	}
}

func TestHandleChatTimerClose(t *testing.T) {
	t.Parallel()

	tests := map[string]events.SignalEvent{
		"message persistence": timerEvent(aliceUser, aliceUser, timerMessage(30, 2)),
		"contact persistence": &events.ContactList{Timers: []events.ContactTimer{contactTimer(aliceUser, 30, 2)}},
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkTimerHandlerClose(t, raw)
		})
	}
}

//nolint:cyclop,funlen // checks persistence, delivery, sync completion and Close ordering
func checkTimerHandlerClose(t *testing.T, raw events.SignalEvent) {
	t.Helper()
	dataDir := seedAccount(t)
	client := openOffline(t, dataDir, signal.SendOnly())

	arrived, _, stop := signal.ContactListHook(client)
	defer stop()

	entered := make(chan struct{})

	persistCtx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closing := signal.TimerPersistenceContext(client, func(context.Context) context.Context {
		close(entered)
		<-persistCtx.Done()

		return persistCtx
	})

	handled := make(chan bool, 1)
	go func() { handled <- signal.Handle(client, raw) }()

	select {
	case <-entered:
	case <-handled:
		t.Fatal("handler returned before timer persistence hook")
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}

	closed := make(chan error, 1)
	go func() { closed <- client.Close() }()

	select {
	case <-closing:
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}

	select {
	case err := <-closed:
		t.Fatalf("Close returned during persistence: %v", err)
	default:
	}

	select {
	case evt := <-client.Events():
		t.Fatalf("blocked persistence emitted %+v", evt)
	case n := <-arrived:
		t.Fatalf("blocked persistence completed sync: %d", n)
	default:
	}

	if signal.Acked(client) {
		t.Fatal("blocked persistence acked")
	}

	cancel()

	if <-handled {
		t.Fatal("cancelled persistence acked")
	}

	err := <-closed
	if err != nil {
		t.Fatal(err)
	}

	select {
	case n := <-arrived:
		t.Fatalf("cancelled persistence completed sync: %d", n)
	default:
	}

	wantChatTimer(t, dataDir, aliceUser, 0, 0, false)
}

func checkWireTimer(t *testing.T, msg *signalpb.DataMessage, seconds, version uint32) {
	t.Helper()

	if msg == nil || msg.ExpireTimer == nil || msg.ExpireTimerVersion == nil || msg.GetExpireTimer() != seconds || msg.GetExpireTimerVersion() != version {
		t.Fatalf("wire timer = %v; want present %d/%d", msg, seconds, version)
	}
}

func directTimerRequest(t *testing.T, client signal.Client, req signal.SendRequest,
	send func(context.Context, libsignalgo.ServiceID, *signalpb.Content) signalmeow.SendMessageResult,
) signal.SendResult {
	t.Helper()

	fresh, err := signal.BuildMessage(t.Context(), client, req)
	if err != nil {
		t.Fatal(err)
	}

	return signal.SendDirectRecipients(t.Context(), client, req, fresh, send)
}

//nolint:cyclop // checks each recipient wire/result and note-to-self error semantics
func TestSendDirectTimers(t *testing.T) {
	t.Parallel()
	dataDir := seedAccount(t)
	client := openOffline(t, dataDir)
	mergeTimer(t, dataDir, aliceUser, 30, 2)
	mergeTimer(t, dataDir, bobUser, 300, 4)
	mergeTimer(t, dataDir, carolUser, 0, 5)
	mergeTimer(t, dataDir, seededACI, 60, 3)
	want := map[string][2]uint32{aliceUser: {30, 2}, bobUser: {300, 4}, carolUser: {0, 5}, seededACI: {60, 3}, daveUser: {0, 1}}
	req := signal.SendRequest{Body: timerBody, Timestamp: 1234, Recipients: []signal.Recipient{{ACI: aliceUser}, {ACI: bobUser}, {ACI: carolUser}, {ACI: seededACI}, {ACI: daveUser}}}
	wires := make(map[string]*signalpb.DataMessage)

	got := directTimerRequest(t, client, req, func(_ context.Context, recipientID libsignalgo.ServiceID, content *signalpb.Content) signalmeow.SendMessageResult {
		pair, ok := want[recipientID.String()]
		if !ok {
			t.Fatalf("unexpected destination: %s", recipientID)
		}

		msg := content.GetDataMessage()
		checkWireTimer(t, msg, pair[0], pair[1])

		if msg.GetTimestamp() != 1234 || msg.GetBody() != timerBody {
			t.Fatalf("wire content = %v", msg)
		}

		wires[recipientID.String()] = msg

		return signalmeow.SendMessageResult{WasSuccessful: true, SuccessfulSendResult: signalmeow.SuccessfulSendResult{Unidentified: true}}
	})
	if got.Timestamp != 1234 || len(got.Results) != 5 {
		t.Fatalf("result = %+v", got)
	}

	for i, res := range got.Results {
		if res.Recipient != req.Recipients[i] || res.Err != nil || !res.Unidentified {
			t.Fatalf("recipient result = %+v", res)
		}
	}

	*wires[aliceUser].ExpireTimer = 999
	checkWireTimer(t, wires[bobUser], 300, 4)
	wantChatTimer(t, dataDir, daveUser, 0, 0, false)
	// Note-to-self retains the sync-specific failure conversion.
	self := directTimerRequest(t, client, signal.SendRequest{Body: "self timer", Timestamp: 7, Recipients: []signal.Recipient{{ACI: seededACI}}}, func(_ context.Context, _ libsignalgo.ServiceID, content *signalpb.Content) signalmeow.SendMessageResult {
		checkWireTimer(t, content.GetDataMessage(), 60, 3)
		return signalmeow.SendMessageResult{}
	})
	if len(self.Results) != 1 || !errors.Is(self.Results[0].Err, signal.ErrSyncFailed) {
		t.Fatalf("self result = %+v", self)
	}
}

func TestSendDirectTimerRestart(t *testing.T) {
	t.Parallel()
	dataDir := seedAccount(t)
	first := openOffline(t, dataDir, signal.SendOnly())
	signal.Handle(first, timerEvent(aliceUser, aliceUser, timerMessage(30, 2)))

	err := first.Close()
	if err != nil {
		t.Fatal(err)
	}

	second := openOffline(t, dataDir)
	called := false

	got := directTimerRequest(t, second, signal.SendRequest{Body: "after restart", Recipients: []signal.Recipient{{ACI: aliceUser}}, Timestamp: 42}, func(_ context.Context, recipientID libsignalgo.ServiceID, content *signalpb.Content) signalmeow.SendMessageResult {
		called = true

		if recipientID.String() != aliceUser {
			t.Fatal(recipientID)
		}

		checkWireTimer(t, content.GetDataMessage(), 30, 2)

		return signalmeow.SendMessageResult{WasSuccessful: true}
	})
	if !called || got.Timestamp != 42 || len(got.Results) != 1 || got.Results[0].Err != nil {
		t.Fatalf("result = %+v, called=%v", got, called)
	}
}

//nolint:cyclop // checks rich wire fields and per-recipient isolation independently
func TestSendDirectTimerRichContent(t *testing.T) {
	t.Parallel()
	dataDir := seedAccount(t)
	client := openOffline(t, dataDir)
	mergeTimer(t, dataDir, aliceUser, 30, 2)
	mergeTimer(t, dataDir, bobUser, 300, 4)

	template := &signalpb.DataMessage{
		Body: new(timerRichBody), Timestamp: new(uint64(5678)), ProfileKey: []byte{1, 2, 3},
		Attachments: []*signalpb.AttachmentPointer{{FileName: new(timerFilename), ContentType: new(timerContentType)}},
		Quote:       &signalpb.DataMessage_Quote{Id: new(uint64(4567)), Text: new("quoted text")},
		BodyRanges:  []*signalpb.BodyRange{{Start: new(uint32(0)), Length: new(uint32(3))}},
		Reaction:    &signalpb.DataMessage_Reaction{Emoji: new("😀"), TargetSentTimestamp: new(uint64(4567))},
		Delete:      &signalpb.DataMessage_Delete{TargetSentTimestamp: new(uint64(2345))},
	}

	for _, editTarget := range []uint64{0, 1234} {
		t.Run(map[bool]string{true: "nested edit wire", false: "ordinary wire"}[editTarget != 0], func(t *testing.T) {
			t.Parallel()

			req := signal.SendRequest{Timestamp: 5678, EditTarget: editTarget, Recipients: []signal.Recipient{{ACI: aliceUser}, {ACI: bobUser}}}
			calls := 0

			got := signal.SendDirectRecipients(t.Context(), client, req, func() *signalpb.DataMessage { return proto.CloneOf(template) }, func(_ context.Context, recipientID libsignalgo.ServiceID, content *signalpb.Content) signalmeow.SendMessageResult {
				calls++

				msg := content.GetDataMessage()
				if editTarget != 0 {
					if content.GetEditMessage().GetTargetSentTimestamp() != 1234 || content.GetDataMessage() != nil {
						t.Fatalf("edit envelope = %v", content)
					}

					msg = content.GetEditMessage().GetDataMessage()
				}

				pair := map[string][2]uint32{aliceUser: {30, 2}, bobUser: {300, 4}}[recipientID.String()]
				checkWireTimer(t, msg, pair[0], pair[1])

				if msg.GetBody() != timerRichBody || msg.GetTimestamp() != 5678 || !bytes.Equal(msg.GetProfileKey(), []byte{1, 2, 3}) ||
					len(msg.GetAttachments()) != 1 || msg.GetAttachments()[0].GetFileName() != timerFilename || msg.GetAttachments()[0].GetContentType() != timerContentType ||
					msg.GetQuote().GetId() != 4567 || msg.GetQuote().GetText() != "quoted text" || len(msg.GetBodyRanges()) != 1 || msg.GetBodyRanges()[0].GetLength() != 3 ||
					msg.GetReaction().GetEmoji() != "😀" || msg.GetReaction().GetTargetSentTimestamp() != 4567 || msg.GetDelete().GetTargetSentTimestamp() != 2345 {
					t.Fatalf("rich content = %v", msg)
				}
				// Transport mutation of one recipient must not contaminate the next.
				msg.Body = new("mutated")
				msg.Attachments[0].FileName = new("mutated.jpg")

				return signalmeow.SendMessageResult{WasSuccessful: true}
			})
			if calls != 2 || len(got.Results) != 2 || got.Results[0].Err != nil || got.Results[1].Err != nil {
				t.Fatalf("result = %+v, calls=%d", got, calls)
			}

			if template.ExpireTimer != nil || template.ExpireTimerVersion != nil || template.GetBody() != timerRichBody || template.GetAttachments()[0].GetFileName() != timerFilename {
				t.Fatalf("template mutated: %v", template)
			}
		})
	}
}

func TestSendDirectTimerControl(t *testing.T) {
	t.Parallel()
	dataDir := seedAccount(t)
	client := openOffline(t, dataDir)
	mergeTimer(t, dataDir, aliceUser, 30, 2)
	// A bodyless control still inherits the direct timer on outgoing messages.
	req := signal.SendRequest{DeleteTarget: 111, Timestamp: 5678, Recipients: []signal.Recipient{{ACI: aliceUser}}}
	directTimerRequest(t, client, req, func(_ context.Context, _ libsignalgo.ServiceID, content *signalpb.Content) signalmeow.SendMessageResult {
		msg := content.GetDataMessage()
		checkWireTimer(t, msg, 30, 2)

		if msg.Body != nil || msg.GetDelete().GetTargetSentTimestamp() != 111 {
			t.Fatalf("delete = %v", msg)
		}

		return signalmeow.SendMessageResult{WasSuccessful: true}
	})
}

//nolint:cyclop // checks partial results alongside skipped transmission on lookup failure
func TestSendDirectTimerPartialFailure(t *testing.T) {
	t.Parallel()
	dataDir := seedAccount(t)
	client := openOffline(t, dataDir)
	mergeTimer(t, dataDir, aliceUser, 30, 2)
	mergeTimer(t, dataDir, bobUser, 300, 4)
	database := timerSQL(t, dataDir)
	execTimerSQL(t, database, `ALTER TABLE gosignal_chat_timers RENAME TO timer_rows`)

	execTimerSQL(t, database, `CREATE VIEW gosignal_chat_timers AS SELECT aci, CASE WHEN aci='aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa' THEN abs(-9223372036854775808) ELSE seconds END AS seconds, version FROM timer_rows`)
	defer func() {
		execTimerSQL(t, database, `DROP VIEW gosignal_chat_timers`)
		execTimerSQL(t, database, `ALTER TABLE timer_rows RENAME TO gosignal_chat_timers`)
	}()

	calls := 0
	req := signal.SendRequest{Body: timerBody, Timestamp: 1234, Recipients: []signal.Recipient{{ACI: aliceUser}, {ACI: bobUser}, {PNI: carolPNI}}}

	fresh, err := signal.BuildMessage(t.Context(), client, req)
	if err != nil {
		t.Fatal(err)
	}

	clones := 0
	clone := func() *signalpb.DataMessage { clones++; return fresh() }

	got := signal.SendDirectRecipients(t.Context(), client, req, clone, func(_ context.Context, recipientID libsignalgo.ServiceID, content *signalpb.Content) signalmeow.SendMessageResult {
		calls++

		if recipientID.String() != bobUser {
			t.Fatalf("transmitted to failed timer recipient %s", recipientID)
		}

		checkWireTimer(t, content.GetDataMessage(), 300, 4)

		if content.GetDataMessage().GetBody() != timerBody || content.GetDataMessage().GetTimestamp() != 1234 {
			t.Fatalf("content = %v", content)
		}

		return signalmeow.SendMessageResult{WasSuccessful: true, SuccessfulSendResult: signalmeow.SuccessfulSendResult{Unidentified: true}}
	})
	if calls != 1 || clones != 1 || got.Timestamp != 1234 || len(got.Results) != 3 || got.Results[0].Recipient.ACI != aliceUser || got.Results[0].Err == nil ||
		got.Results[1].Recipient.ACI != bobUser || got.Results[1].Err != nil || !got.Results[1].Unidentified || !errors.Is(got.Results[2].Err, signal.ErrUnresolvable) {
		t.Fatalf("result = %+v, calls=%d", got, calls)
	}
}
