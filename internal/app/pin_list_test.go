package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/google/uuid"
)

func retainedPin(sender string, sent, target uint64, seconds uint32, forever bool) *signal.Pin {
	return &signal.Pin{
		Envelope: signal.Envelope{Sender: signal.Recipient{ACI: sender}, Chat: groupChat(), Timestamp: sent},
		OutgoingPin: signal.OutgoingPin{
			TargetAuthor: aliceUser(), TargetTimestamp: target, DurationSeconds: seconds, Forever: forever,
		},
	}
}

func retainedUnpin(sender string, sent, target uint64) *signal.Unpin {
	return &signal.Unpin{
		Envelope:      signal.Envelope{Sender: signal.Recipient{ACI: sender}, Chat: groupChat(), Timestamp: sent},
		OutgoingUnpin: signal.OutgoingUnpin{TargetAuthor: aliceUser(), TargetTimestamp: target},
	}
}

func pinSnapshotEntry(id int64, received time.Time, event signal.Event) signal.InboxEntry {
	return signal.InboxEntry{ID: id, ReceivedAt: received, Chat: groupChat(), Event: event}
}

func TestPinListPreflightCanonicalOfflineChat(t *testing.T) {
	t.Parallel()

	for _, chat := range []string{
		app.SelfRecipient, aliceNumber, bobUsername, uuid.Nil.String(), strings.ToUpper(aliceACI),
		" " + aliceACI, strings.TrimSuffix(app.GroupPrefix+groupID, "="), "group:broken-pin-value",
	} {
		req := app.PinListRequest{Chat: chat}
		if req.Check() == nil {
			t.Fatalf("noncanonical chat accepted: %q", chat)
		}
	}

	for _, chat := range []string{aliceACI, app.GroupPrefix + groupID} {
		for _, limit := range []int{0, 1, 1000, 10000} {
			err := (app.PinListRequest{Chat: chat, ScanLimit: limit}).Check()
			if err != nil {
				t.Fatalf("valid chat %s limit %d: %v", chat, limit, err)
			}
		}
	}

	for _, limit := range []int{-1, 10001} {
		if (app.PinListRequest{Chat: aliceACI, ScanLimit: limit}).Check() == nil {
			t.Fatalf("unbounded scan %d accepted", limit)
		}
	}

	a, client := pinSender(t, directory())
	client.inbox = func(context.Context, signal.InboxQuery) ([]signal.InboxEntry, error) {
		t.Fatal("invalid request queried the inbox")

		return nil, nil
	}

	_, err := a.PinList(t.Context(), app.PinListRequest{Chat: app.SelfRecipient})
	if err == nil {
		t.Fatal("invalid offline chat accepted")
	}
}

//nolint:funlen,cyclop,gocyclo // A literal timeline covers interacting arrival, identity and expiry rules.
func TestPinListIDOrderDuplicatesExpiryAndDeletes(t *testing.T) {
	t.Parallel()

	fake := directory()
	a, client := pinSender(t, fake)
	received := time.UnixMilli(sentAt).Add(-time.Minute)
	duplicate := retainedPin(bobACI, 900, 100, 60, false)
	conflicting := retainedPin(bobACI, 900, 100, 600, false)
	deleteBefore := &signal.Delete{
		Envelope: signal.Envelope{Sender: aliceUser(), Chat: groupChat(), Timestamp: 1}, TargetTimestamp: 200,
	}
	deleteAfter := &signal.Delete{
		Envelope: signal.Envelope{Sender: aliceUser(), Chat: groupChat(), Timestamp: 2}, TargetTimestamp: 100,
	}
	sortedTarget := retainedPin(carolACI, 10, 300, 0, true)
	sortedTarget.TargetAuthor = bobUser()
	entries := []signal.InboxEntry{
		pinSnapshotEntry(12, received, sortedTarget),
		pinSnapshotEntry(11, received, retainedPin(carolACI, 9, 200, 0, true)),
		pinSnapshotEntry(10, received, deleteAfter),
		pinSnapshotEntry(9, received, retainedUnpin(carolACI, 8, 400)),
		pinSnapshotEntry(8, received, retainedUnpin(carolACI, 7, 200)),
		pinSnapshotEntry(7, received, deleteBefore),
		pinSnapshotEntry(6, received, retainedPin(carolACI, 6, 200, 0, true)),
		pinSnapshotEntry(5, received, retainedPin(carolACI, 5, 100, 60, false)),
		pinSnapshotEntry(4, received.Add(50*time.Second), conflicting),
		pinSnapshotEntry(3, received.Add(40*time.Second), duplicate),
		pinSnapshotEntry(2, received, duplicate),
		pinSnapshotEntry(1, received, retainedUnpin(bobACI, 1000, 100)),
	}
	queryCount := 0
	client.inbox = func(_ context.Context, query signal.InboxQuery) ([]signal.InboxEntry, error) {
		queryCount++

		if query != (signal.InboxQuery{Chat: app.GroupPrefix + groupID, Newest: true, Limit: 1001}) {
			t.Fatalf("query %+v", query)
		}

		return entries, nil
	}

	got, err := a.PinList(t.Context(), app.PinListRequest{Chat: app.GroupPrefix + groupID})
	if err != nil {
		t.Fatal(err)
	}

	if got.Completeness != pollCompletenessUnknown || got.Scanned != 12 || got.FirstEntryID != 1 ||
		got.LastEntryID != 12 || got.Truncated ||
		got.Conflicts != 1 || got.IgnoredInvalid != 0 || queryCount != 1 || len(got.Observations) != 4 {
		t.Fatalf("state %+v; queries %d", got, queryCount)
	}

	first := got.Observations[0]
	if first.TargetAuthor.ACI != aliceACI || first.TargetTimestamp != 100 || first.Timestamp != 5 || first.EntryID != 5 ||
		first.Operation != pinTestOperation || !first.ExpiryReached ||
		!first.ExpiresAt.Equal(time.UnixMilli(sentAt)) || !first.TargetDeleted {
		t.Fatalf("ID-order and expiry %+v", first)
	}

	second := got.Observations[1]
	if second.TargetTimestamp != 200 || second.EntryID != 11 || !second.Forever || !second.TargetDeleted ||
		!second.ExpiresAt.IsZero() || second.ExpiryReached {
		t.Fatalf("forever/deletion %+v", second)
	}

	third := got.Observations[2]
	if third.TargetTimestamp != 400 || third.Operation != unpinTestOperation || !third.ExpiresAt.IsZero() ||
		third.ExpiryReached {
		t.Fatalf("unpin %+v", third)
	}

	if got.Observations[3].TargetAuthor.ACI != bobACI || len(fake.Connects()) != 0 {
		t.Fatalf("sort/offline %+v", got)
	}
}

func TestPinListDuplicateDoesNotExtendFirstReceipt(t *testing.T) {
	t.Parallel()
	a, client := pinSender(t, directory())
	received := time.UnixMilli(sentAt).Add(-time.Minute)
	first := retainedPin(bobACI, 100, 10, 60, false)
	duplicate := retainedPin(bobACI, 100, 10, 60, false)
	duplicate.Sync = true
	conflict := retainedUnpin(bobACI, 100, 10)
	client.inbox = func(context.Context, signal.InboxQuery) ([]signal.InboxEntry, error) {
		return []signal.InboxEntry{
			pinSnapshotEntry(1, received, first),
			pinSnapshotEntry(2, received.Add(time.Minute), duplicate),
			pinSnapshotEntry(3, received.Add(time.Minute), conflict),
		}, nil
	}

	got, err := a.PinList(t.Context(), app.PinListRequest{Chat: app.GroupPrefix + groupID})
	if err != nil || len(got.Observations) != 1 {
		t.Fatalf("state %+v: %v", got, err)
	}

	obs := got.Observations[0]
	if obs.EntryID != 1 || !obs.ReceivedAt.Equal(received) || !obs.ExpiryReached || got.Conflicts != 1 ||
		obs.Operation != pinTestOperation {
		t.Fatalf("duplicate receipt %+v / %+v", obs, got)
	}
}

func TestPinListInvalidControlsAndMismatchedChats(t *testing.T) {
	t.Parallel()
	a, client := pinSender(t, directory())
	received := time.UnixMilli(sentAt)
	badSender := retainedPin(uuid.Nil.String(), 1, 100, 1, false)
	noncanonicalSender := retainedPin(strings.ToUpper(bobACI), 2, 100, 1, false)
	badTarget := retainedPin(bobACI, 3, 100, 1, false)
	badTarget.TargetAuthor.ACI = uuid.Nil.String()
	noncanonicalTarget := retainedPin(bobACI, 4, 100, 1, false)
	noncanonicalTarget.TargetAuthor.ACI = strings.ToUpper(aliceACI)
	wrongChat := retainedPin(bobACI, 5, 100, 1, false)
	wrongChat.Chat = aliceChat()
	invalidUnpin := retainedUnpin(bobACI, 6, 100)
	invalidUnpin.TargetAuthor = signal.Recipient{}
	valid := retainedPin(bobACI, 3, 100, 1, false)
	events := []signal.Event{
		badSender, noncanonicalSender, badTarget, noncanonicalTarget,
		retainedPin(bobACI, 0, 100, 1, false),
		retainedPin(bobACI, 7, 0, 1, false),
		retainedPin(bobACI, 8, 100, 0, false),
		retainedPin(bobACI, 9, 100, 1, true),
		invalidUnpin,
		&signal.Unsupported{Envelope: signal.Envelope{Chat: groupChat()}, Type: "invalidPin"},
		wrongChat,
		valid,
		&signal.Unsupported{Envelope: signal.Envelope{Chat: groupChat()}, Type: "call"},
	}

	entries := make([]signal.InboxEntry, 0, len(events)+1)
	for i, event := range events {
		entries = append(entries, pinSnapshotEntry(int64(i+1), received, event))
	}

	entries = append(entries, pinSnapshotEntry(14, time.Time{}, retainedPin(bobACI, 10, 100, 1, false)))
	client.inbox = func(context.Context, signal.InboxQuery) ([]signal.InboxEntry, error) { return entries, nil }

	got, err := a.PinList(t.Context(), app.PinListRequest{Chat: app.GroupPrefix + groupID})
	if err != nil || got.IgnoredInvalid != 11 || got.Conflicts != 0 || len(got.Observations) != 1 ||
		got.Observations[0].EntryID != 12 {
		t.Fatalf("invalids %+v: %v", got, err)
	}
}

//nolint:cyclop // Independent assertions cover bounded, missing and failed snapshots.
func TestPinListTruncatedMissingAndCancelledSnapshots(t *testing.T) {
	t.Parallel()
	a, client := pinSender(t, directory())
	received := time.UnixMilli(sentAt)
	client.inbox = func(_ context.Context, query signal.InboxQuery) ([]signal.InboxEntry, error) {
		if query.Limit != 3 || !query.Newest {
			t.Fatalf("bounded query %+v", query)
		}

		return []signal.InboxEntry{
			pinSnapshotEntry(50, received, retainedPin(bobACI, 1000, 100, 0, true)),
			pinSnapshotEntry(70, received, retainedUnpin(bobACI, 1001, 100)),
			pinSnapshotEntry(60, received, retainedPin(bobACI, 1002, 200, 0, true)),
		}, nil
	}

	got, err := a.PinList(t.Context(), app.PinListRequest{Chat: app.GroupPrefix + groupID, ScanLimit: 2})
	if err != nil || !got.Truncated || got.Scanned != 2 || got.FirstEntryID != 60 || got.LastEntryID != 70 ||
		len(got.Observations) != 2 || got.Observations[0].Operation != unpinTestOperation {
		t.Fatalf("bounds %+v: %v", got, err)
	}

	client.inbox = func(context.Context, signal.InboxQuery) ([]signal.InboxEntry, error) { return nil, nil }

	got, err = a.PinList(t.Context(), app.PinListRequest{Chat: aliceACI})
	if err != nil || got.Scanned != 0 || got.FirstEntryID != 0 || got.LastEntryID != 0 ||
		len(got.Observations) != 0 || got.Completeness != pollCompletenessUnknown {
		t.Fatalf("missing %+v: %v", got, err)
	}

	client.inbox = func(context.Context, signal.InboxQuery) ([]signal.InboxEntry, error) { return nil, errBoom }

	_, err = a.PinList(t.Context(), app.PinListRequest{Chat: aliceACI})
	if !errors.Is(err, errBoom) {
		t.Fatalf("store failure %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	client.inbox = func(context.Context, signal.InboxQuery) ([]signal.InboxEntry, error) {
		t.Fatal("cancelled snapshot queried the inbox")

		return nil, nil
	}

	_, err = a.PinList(ctx, app.PinListRequest{Chat: aliceACI})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled %v", err)
	}
}

func TestPinListUserChatAndSingleClockCapture(t *testing.T) {
	t.Parallel()

	fake := directory()
	_, client := pinSender(t, fake)
	clockCalls := 0
	a := app.New(client, app.WithClock(func() time.Time {
		clockCalls++

		return time.UnixMilli(sentAt)
	}))
	control := retainedPin(bobACI, 1, 100, 1, false)
	control.Chat = aliceChat()
	control.TargetAuthor.Number = aliceNumber
	client.inbox = func(context.Context, signal.InboxQuery) ([]signal.InboxEntry, error) {
		return []signal.InboxEntry{{
			ID: 1, Chat: aliceChat(), Event: control, ReceivedAt: time.UnixMilli(sentAt).Add(-time.Second),
		}}, nil
	}

	got, err := a.PinList(t.Context(), app.PinListRequest{Chat: aliceACI})
	if err != nil || len(got.Observations) != 1 || !got.Observations[0].ExpiryReached || clockCalls != 1 ||
		len(fake.Connects()) != 0 {
		t.Fatalf("user observation %+v: %v; clock calls %d", got, err, clockCalls)
	}
}

//nolint:cyclop // Independent checks cover persistence, unread state and receipts.
func TestPinControlsStoredWithoutUnreadOrReadReceipts(t *testing.T) {
	t.Parallel()

	fake := directory()
	a := sender(t, fake)
	pin := retainedPin(bobACI, 100, 10, 1, false)
	unpin := retainedUnpin(bobACI, 101, 10)
	inbox := runInbox(t, a, app.InboxOptions{}, pin, unpin)

	page, err := inbox.List(t.Context(), app.MessagesRequest{Chat: app.GroupPrefix + groupID})
	if err != nil || len(page.Entries) != 2 || page.Entries[0].Unread || page.Entries[1].Unread ||
		!page.Entries[0].Time.Equal(time.UnixMilli(100)) {
		t.Fatalf("stored controls %+v: %v", page, err)
	}

	receipts := a.ReadReceipts()
	if receipts.Add(pin) || receipts.Add(unpin) || receipts.Pending() {
		t.Fatal("control eligible for automatic read receipts")
	}

	err = receipts.Flush(t.Context())
	if err != nil || len(fake.Receipts()) != 0 {
		t.Fatalf("control receipts %+v: %v", fake.Receipts(), err)
	}

	if len(app.EventRecipients(pin)) != 3 || len(app.EventRecipients(unpin)) != 3 {
		t.Fatal("target and sender names missing")
	}
}

func TestPinListIgnoresEditsAndUnsupportedAdminDeletes(t *testing.T) {
	t.Parallel()
	a, client := pinSender(t, directory())
	received := time.UnixMilli(sentAt)
	client.inbox = func(context.Context, signal.InboxQuery) ([]signal.InboxEntry, error) {
		return []signal.InboxEntry{
			pinSnapshotEntry(1, received, retainedPin(bobACI, 1, 100, 0, true)),
			pinSnapshotEntry(2, received, &signal.Edit{
				Envelope:        signal.Envelope{Sender: aliceUser(), Chat: groupChat(), Timestamp: 2},
				TargetTimestamp: 100, Body: "edited",
			}),
			pinSnapshotEntry(3, received, &signal.Unsupported{
				Envelope: signal.Envelope{Sender: aliceUser(), Chat: groupChat(), Timestamp: 3},
				Type:     "adminDelete",
			}),
			pinSnapshotEntry(4, received, &signal.Delete{
				Envelope:        signal.Envelope{Sender: bobUser(), Chat: groupChat(), Timestamp: 4},
				TargetTimestamp: 100,
			}),
		}, nil
	}

	got, err := a.PinList(t.Context(), app.PinListRequest{Chat: app.GroupPrefix + groupID})
	if err != nil || len(got.Observations) != 1 || got.Observations[0].TargetDeleted || got.IgnoredInvalid != 0 {
		t.Fatalf("unsupported changes affected target %+v: %v", got, err)
	}
}

func TestPinListCancellationDuringQuery(t *testing.T) {
	t.Parallel()
	a, client := pinSender(t, directory())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	client.inbox = func(context.Context, signal.InboxQuery) ([]signal.InboxEntry, error) {
		cancel()

		return nil, nil
	}

	_, err := a.PinList(ctx, app.PinListRequest{Chat: aliceACI})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("query cancellation %v", err)
	}
}
