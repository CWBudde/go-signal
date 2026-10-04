//go:build cgo || libsignal_go

package signal_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
)

// stopReceiptSessions records which identities the real backend tries to encrypt for and
// fails before it can fetch keys or transmit. A disabled peer receipt must never reach it.
type stopReceiptSessions struct {
	mstore.SessionStore

	identities []string
}

func (s *stopReceiptSessions) AllSessionsForServiceID(
	_ context.Context, recipient libsignalgo.ServiceID,
) ([]mstore.SessionAddressTuple, error) {
	s.identities = append(s.identities, recipient.String())

	return nil, errNoNetwork
}

func TestPhoneReadReceiptSetting(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		setting   *signalpb.AccountRecord
		typ       signal.ReceiptType
		want      []string
		wantError bool
	}{
		{
			"disabled read sync only", &signalpb.AccountRecord{ReadReceipts: false},
			signal.ReceiptRead,
			[]string{seededACI},
			false,
		},
		{
			"disabled populated record", &signalpb.AccountRecord{ReadReceipts: false, TypingIndicators: true},
			signal.ReceiptRead,
			[]string{seededACI},
			false,
		},
		{
			"enabled attempts peer", &signalpb.AccountRecord{ReadReceipts: true},
			signal.ReceiptRead,
			[]string{aliceUser},
			true,
		},
		{"unknown keeps backend default", nil, signal.ReceiptRead, []string{aliceUser}, true},
		{"delivery independent of setting", &signalpb.AccountRecord{}, signal.ReceiptDelivery, []string{aliceUser}, true},
		{
			"explicit viewed independent of setting", &signalpb.AccountRecord{},
			signal.ReceiptViewed,
			[]string{aliceUser},
			true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dir := seedAccount(t)
			seedContacts(t, dir)
			seedReceiptSetting(t, dir, test.setting)

			// Open after persistence, and repeat after Close to cover a new CLI process.
			for range 2 {
				checkReceiptAttempt(t, dir, test.typ, test.want, test.wantError)
			}
		})
	}
}

func checkReceiptAttempt(t *testing.T, dir string, typ signal.ReceiptType, want []string, wantError bool) {
	t.Helper()

	client := openOffline(t, dir)
	sessions := &stopReceiptSessions{}
	restore := signal.WithReceiptBackend(client, sessions)
	err := client.SendReceipt(t.Context(), signal.Recipient{ACI: aliceUser}, typ, []uint64{1000, 1001})

	restore()

	if wantError != errors.Is(err, errNoNetwork) {
		t.Errorf("SendReceipt error = %v, want session failure = %v", err, wantError)
	}

	if !wantError && err != nil {
		t.Errorf("disabled receipt: %v", err)
	}

	if !reflect.DeepEqual(sessions.identities, want) {
		t.Errorf("encryption targets = %v, want %v", sessions.identities, want)
	}

	err = client.Close()
	if err != nil {
		t.Fatal(err)
	}
}

func seedReceiptSetting(t *testing.T, dir string, setting *signalpb.AccountRecord) {
	t.Helper()

	withStore(t, dir, func(device *mstore.Device, _ *store.Store) {
		device.AccountRecord = setting

		err := device.DeviceStore.PutDevice(t.Context(), &device.DeviceData)
		if err != nil {
			t.Fatal(err)
		}
	})
}

// This catches policy bypasses in the receive batching and MCP/daemon inbox paths, and checks
// that suppressing a peer receipt does not prevent marking a stored message read.
func TestDisabledReadReceiptsThroughApp(t *testing.T) {
	t.Parallel()

	dir := seedAccount(t)
	seedContacts(t, dir)
	seedReceiptSetting(t, dir, &signalpb.AccountRecord{})
	client := openOffline(t, dir)
	sessions := &stopReceiptSessions{}
	t.Cleanup(signal.WithReceiptBackend(client, sessions))
	a := app.New(client)
	msg := &signal.Message{Envelope: signal.Envelope{
		Sender: signal.Recipient{ACI: aliceUser}, Chat: signal.Chat{Recipient: signal.Recipient{ACI: aliceUser}},
		Timestamp: 1000,
	}}

	receipts := a.ReadReceipts()
	if !receipts.Add(msg) {
		t.Fatal("incoming message not queued")
	}

	err := receipts.Flush(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.InboxAdd(t.Context(), signal.InboxEntry{
		Event: msg, Chat: msg.Chat, Unread: true, ReceivedAt: time.UnixMilli(2000), Time: time.UnixMilli(1000),
	})
	if err != nil {
		t.Fatal(err)
	}

	marked, err := a.Inbox(app.InboxOptions{}).MarkRead(t.Context(), app.MarkReadRequest{})
	if err != nil || marked != (app.MarkReadResult{Messages: 1, Senders: 1}) {
		t.Fatalf("MarkRead = %+v, %v", marked, err)
	}

	entries, err := client.InboxList(t.Context(), signal.InboxQuery{Unread: true})
	if err != nil || len(entries) != 0 {
		t.Errorf("unread after mark-read = %+v, %v", entries, err)
	}

	if want := []string{seededACI, seededACI}; !reflect.DeepEqual(sessions.identities, want) {
		t.Errorf("encryption targets = %v, want %v", sessions.identities, want)
	}
}
