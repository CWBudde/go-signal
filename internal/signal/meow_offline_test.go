//go:build cgo

package signal_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/events"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "go.mau.fi/mautrix-signal/pkg/signalmeow/store"
)

var errTest = errors.New("test error")

const (
	groupID    = "group-a"
	groupTitle = "Climbing"
)

// openOffline opens the seeded account in dataDir as if connected (see signal.ConnectOffline).
func openOffline(t *testing.T, dataDir string, opts ...signal.ConnectOption) signal.Client {
	t.Helper()

	client, err := signal.Open(t.Context(), signal.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	signal.ConnectOffline(t.Context(), client, opts...)

	return client
}

func receiptEvent(typ signalpb.ReceiptMessage_Type) *events.Receipt {
	return &events.Receipt{
		Sender:  uuid.MustParse(aliceUser),
		Content: &signalpb.ReceiptMessage{Type: typ.Enum(), Timestamp: []uint64{1, 2}},
	}
}

func TestHandleDeliversAndAcks(t *testing.T) {
	t.Parallel()

	client := openOffline(t, seedAccount(t))

	got := make(chan signal.Event, 1)

	go func() { got <- <-client.Events() }()

	if !signal.Handle(client, receiptEvent(signalpb.ReceiptMessage_READ)) {
		t.Fatal("delivered event not acked")
	}

	rcpt, ok := (<-got).(*signal.Receipt)
	if !ok || rcpt.Type != signal.ReceiptRead || rcpt.Sender.ACI != aliceUser {
		t.Errorf("event = %+v, want a read receipt from alice", rcpt)
	}

	if !signal.Acked(client) {
		t.Error("client doesn't know it acked an event")
	}
}

func TestHandleIgnoresStoreUpdates(t *testing.T) {
	t.Parallel()

	client := openOffline(t, seedAccount(t))

	// Nothing reads Events: an event that is handed out would block.
	if !signal.Handle(client, &events.ACIFound{}) {
		t.Error("ignored event not acked")
	}

	if signal.Acked(client) {
		t.Error("an ignored event counts as delivered")
	}
}

func TestHandleSendOnly(t *testing.T) {
	t.Parallel()

	client := openOffline(t, seedAccount(t), signal.SendOnly())

	// Messages stay on the server for the next run.
	if signal.Handle(client, receiptEvent(signalpb.ReceiptMessage_DELIVERY)) {
		t.Error("send-only client acked a receipt")
	}

	err := signal.LostOr(client, errTest)
	if !errors.Is(err, errTest) || errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Errorf("lostOr before a logout = %v, want only the test error", err)
	}

	// A logout is recorded instead of delivered, and becomes the cause of later failures.
	if !signal.Handle(client, &events.LoggedOut{}) {
		t.Error("send-only client didn't ack the logout")
	}

	err = signal.LostOr(client, errTest)
	if !errors.Is(err, signal.ErrDeviceUnlinked) || !errors.Is(err, errTest) {
		t.Errorf("lostOr after a logout = %v, want it to wrap ErrDeviceUnlinked and the test error", err)
	}
}

func TestHandleLoggedOutMarksUnlinked(t *testing.T) {
	t.Parallel()

	dataDir := seedAccount(t)
	client := openOffline(t, dataDir)

	got := make(chan signal.Event, 1)

	go func() { got <- <-client.Events() }()

	if !signal.Handle(client, &events.LoggedOut{Error: errTest}) {
		t.Fatal("logout not acked")
	}

	conn, ok := (<-got).(*signal.Connection)
	if !ok || conn.State != signal.StateLoggedOut {
		t.Fatalf("event = %+v, want a logout", conn)
	}

	// The server's error is replaced by one that names the account.
	if !errors.Is(conn.Err, signal.ErrDeviceUnlinked) || errors.Is(conn.Err, errTest) {
		t.Errorf("logout error = %v, want ErrDeviceUnlinked without the server's error", conn.Err)
	}

	dir, err := store.OpenDir(dataDir, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	accounts, err := dir.Accounts()
	if err != nil {
		t.Fatal(err)
	}

	if len(accounts) != 1 || accounts[0].UnlinkedAt.IsZero() {
		t.Errorf("accounts.json = %+v, want the account marked unlinked", accounts)
	}
}

func TestHandleAfterClose(t *testing.T) {
	t.Parallel()

	client := openOffline(t, seedAccount(t))

	err := client.Close()
	if err != nil {
		t.Fatal(err)
	}

	if signal.Handle(client, receiptEvent(signalpb.ReceiptMessage_READ)) {
		t.Error("closed client acked an event")
	}
}

func TestConvertReceiptTypes(t *testing.T) {
	t.Parallel()

	for typ, want := range map[signalpb.ReceiptMessage_Type]signal.ReceiptType{
		signalpb.ReceiptMessage_DELIVERY: signal.ReceiptDelivery,
		signalpb.ReceiptMessage_READ:     signal.ReceiptRead,
		signalpb.ReceiptMessage_VIEWED:   signal.ReceiptViewed,
		signalpb.ReceiptMessage_Type(42): 0,
	} {
		evt, ok := signal.ConvertEvent(receiptEvent(typ), seededACI).(*signal.Receipt)
		if !ok {
			t.Fatalf("%v: not a receipt", typ)
		}

		if evt.Type != want {
			t.Errorf("%v: type %v, want %v", typ, evt.Type, want)
		}
	}
}

func TestGroupTitleCache(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	client := openOffline(t, seedAccount(t))
	leftAt := time.UnixMilli(1_700_000_000_000)

	signal.CacheGroup(ctx, client, signal.Group{ID: groupID, Title: groupTitle, Revision: 3, LeftAt: leftAt})

	got := signal.UnavailableGroup(ctx, client, groupID, errTest)
	if got.ID != groupID || got.Title != groupTitle || !got.LeftAt.Equal(leftAt) || !errors.Is(got.Err, errTest) {
		t.Errorf("unavailable cached group = %+v", got)
	}

	// An empty title (the group couldn't be decrypted) keeps the one known before.
	signal.CacheGroup(ctx, client, signal.Group{ID: groupID, Revision: 4})

	if got := signal.UnavailableGroup(ctx, client, groupID, errTest); got.Title != groupTitle {
		t.Errorf("title after an update without one = %q, want the cached one", got.Title)
	}

	got = signal.UnavailableGroup(ctx, client, "group-b", errTest)
	if got.ID != "group-b" || got.Title != "" || !errors.Is(got.Err, errTest) {
		t.Errorf("unavailable unknown group = %+v", got)
	}
}

func TestStoredMasterKey(t *testing.T) {
	t.Parallel()

	dataDir := seedAccount(t)
	client := openOffline(t, dataDir)

	key, err := signal.StoredMasterKey(t.Context(), client)
	if err != nil || key != nil {
		t.Fatalf("master key before sync = %x, %v; want none", key, err)
	}

	want := bytes.Repeat([]byte{7}, 32)

	withStore(t, dataDir, func(device *mstore.Device, data *store.Store) {
		device.MasterKey = want

		err := data.Devices.PutDevice(t.Context(), &device.DeviceData)
		if err != nil {
			t.Fatal(err)
		}
	})

	// signalmeow stores the key from its receive loop, so it is read from the database.
	key, err = signal.StoredMasterKey(t.Context(), client)
	if err != nil || !bytes.Equal(key, want) {
		t.Errorf("master key = %x, %v; want %x", key, err, want)
	}
}

func TestOwnProfileKey(t *testing.T) {
	t.Parallel()

	dataDir := seedAccount(t)
	client := openOffline(t, dataDir)

	if key := signal.OwnProfileKey(t.Context(), client); key != nil {
		t.Errorf("profile key before linking stored one = %x, want none", key)
	}

	var want libsignalgo.ProfileKey
	copy(want[:], bytes.Repeat([]byte{9}, len(want)))

	withStore(t, dataDir, func(device *mstore.Device, _ *store.Store) {
		err := device.RecipientStore.StoreProfileKey(t.Context(), uuid.MustParse(seededACI), want)
		if err != nil {
			t.Fatal(err)
		}
	})

	if key := signal.OwnProfileKey(t.Context(), client); !bytes.Equal(key, want[:]) {
		t.Errorf("profile key = %x, want %x", key, want[:])
	}
}

func TestParseE164(t *testing.T) {
	t.Parallel()

	got, err := signal.ParseE164("+15550100")
	if err != nil || got != 15550100 {
		t.Errorf("ParseE164(+15550100) = %d, %v", got, err)
	}

	for _, bad := range []string{"15550100", "+", "+1555abc", "+99999999999999999999999"} {
		_, err := signal.ParseE164(bad)
		if !errors.Is(err, signal.ErrInvalidNumber) {
			t.Errorf("ParseE164(%q) = %v, want ErrInvalidNumber", bad, err)
		}
	}
}

func TestSyncErrors(t *testing.T) {
	t.Parallel()

	errA, errB := errors.New("contacts missing"), context.DeadlineExceeded //nolint:err113 // test data
	err := signal.SyncErrors(errA, errB)

	if got := err.Error(); got != "contacts missing; context deadline exceeded" {
		t.Errorf("Error() = %q", got)
	}

	if !errors.Is(err, errA) || !errors.Is(err, errB) {
		t.Error("the reasons don't unwrap")
	}

	if strings.Contains(signal.SyncErrors().Error(), ";") {
		t.Error("no reasons joined with a separator")
	}
}
