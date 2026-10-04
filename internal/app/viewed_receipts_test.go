package app_test

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func TestSendViewedReceipt(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		arg string
		aci string
	}{{aliceNumber, aliceACI}, {bobUsername, bobACI}, {carolACI, carolACI}} {
		t.Run(test.arg, func(t *testing.T) {
			t.Parallel()

			fake := directory()
			timestamps := []uint64{targetAt + 1, targetAt, targetAt + 1, math.MaxUint64}
			original := slices.Clone(timestamps)

			res, err := sender(t, fake).SendViewedReceipt(t.Context(), app.ViewedReceiptRequest{
				Sender: test.arg, Timestamps: timestamps,
			})
			if err != nil {
				t.Fatalf("send viewed receipt: %v", err)
			}

			wantTimestamps := []uint64{targetAt + 1, targetAt, math.MaxUint64}
			if res.Sender.ACI != test.aci || !slices.Equal(res.Timestamps, wantTimestamps) {
				t.Fatalf("result = %+v", res)
			}

			want := []signaltest.ReceiptCall{{
				Sender: res.Sender, Type: signal.ReceiptViewed, Timestamps: wantTimestamps,
			}}
			if got := fake.Receipts(); !reflect.DeepEqual(got, want) {
				t.Errorf("receipts = %+v; want %+v", got, want)
			}

			if !slices.Equal(timestamps, original) {
				t.Error("request timestamps were changed")
			}

			if !slices.Equal(fake.Connects(), []string{testAccount().ACI}) {
				t.Errorf("connections = %v; want selected account", fake.Connects())
			}
		})
	}
}

func TestSendViewedReceiptInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  app.ViewedReceiptRequest
		want error
	}{
		{"missing sender", app.ViewedReceiptRequest{Timestamps: []uint64{1}}, app.ErrInvalidRecipient},
		{
			"invalid sender",
			app.ViewedReceiptRequest{Sender: "not-a-recipient", Timestamps: []uint64{1}},
			app.ErrInvalidRecipient,
		},
		{
			"group sender",
			app.ViewedReceiptRequest{Sender: app.GroupPrefix + groupID, Timestamps: []uint64{1}},
			app.ErrInvalidRecipient,
		},
		{
			"self sender",
			app.ViewedReceiptRequest{Sender: app.SelfRecipient, Timestamps: []uint64{1}},
			app.ErrInvalidRecipient,
		},
		{"no timestamps", app.ViewedReceiptRequest{Sender: aliceNumber}, signal.ErrInvalidReceipt},
		{
			"zero timestamp",
			app.ViewedReceiptRequest{Sender: aliceNumber, Timestamps: []uint64{1, 0}},
			signal.ErrInvalidReceipt,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := directory()

			_, err := sender(t, fake).SendViewedReceipt(t.Context(), test.req)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v; want %v", err, test.want)
			}

			if len(fake.Connects()) != 0 || len(fake.Receipts()) != 0 {
				t.Fatal("connected or sent with invalid input")
			}
		})
	}
}

func TestSendViewedReceiptOwnSender(t *testing.T) {
	t.Parallel()

	for _, senderArg := range []string{testAccount().ACI, testAccount().Number, "@own.42"} {
		fake := directory()
		fake.Directory = append(fake.Directory, signal.Recipient{ACI: testAccount().ACI, Username: "own.42"})

		_, err := sender(t, fake).SendViewedReceipt(t.Context(), app.ViewedReceiptRequest{
			Sender: senderArg, Timestamps: []uint64{1},
		})
		if !errors.Is(err, app.ErrInvalidRecipient) || len(fake.Receipts()) != 0 {
			t.Errorf("own sender %s: receipts = %+v, error = %v", senderArg, fake.Receipts(), err)
		}
	}
}

func TestSendViewedReceiptFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(*signaltest.Fake)
		arg    string
		want   error
	}{
		{"connection failure", func(f *signaltest.Fake) { f.ConnectErr = errBoom }, aliceNumber, errBoom},
		{"resolution failure", func(*signaltest.Fake) {}, "+4915100000000", signal.ErrNotOnSignal},
		{"receipt", func(f *signaltest.Fake) { f.ReceiptErr = errBoom }, aliceNumber, errBoom},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := directory()
			test.change(fake)

			_, err := sender(t, fake).SendViewedReceipt(t.Context(), app.ViewedReceiptRequest{
				Sender: test.arg, Timestamps: []uint64{1},
			})
			if !errors.Is(err, test.want) || len(fake.Receipts()) != 0 {
				t.Fatalf("receipts = %+v, error = %v; want %v", fake.Receipts(), err, test.want)
			}
		})
	}
}

func TestSendViewedReceiptOwnSenderBeforeConnect(t *testing.T) {
	t.Parallel()

	for _, senderArg := range []string{testAccount().ACI, testAccount().Number} {
		fake := directory()
		fake.ConnectErr = errBoom

		_, err := sender(t, fake).SendViewedReceipt(t.Context(), app.ViewedReceiptRequest{
			Sender: senderArg, Timestamps: []uint64{1},
		})
		if !errors.Is(err, app.ErrInvalidRecipient) || len(fake.Connects()) != 0 {
			t.Errorf("own sender %s: connects = %v, error = %v", senderArg, fake.Connects(), err)
		}
	}
}

func TestSendViewedReceiptConnected(t *testing.T) {
	t.Parallel()

	fake := directory()

	_, err := connected(t, fake).SendViewedReceipt(t.Context(), app.ViewedReceiptRequest{
		Sender: aliceNumber, Timestamps: []uint64{1},
	})
	if err != nil || len(fake.Receipts()) != 1 {
		t.Fatalf("receipts = %+v, error = %v", fake.Receipts(), err)
	}
}

func TestSendViewedReceiptAllowlist(t *testing.T) {
	t.Parallel()

	fake := directory()

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	allow, err := app.ParseAllowlist(nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = app.New(client, app.WithAllowlist(allow)).SendViewedReceipt(t.Context(), app.ViewedReceiptRequest{
		Sender: aliceNumber, Timestamps: []uint64{1},
	})
	if !errors.Is(err, app.ErrRecipientNotAllowed) || len(fake.Receipts()) != 0 {
		t.Fatalf("receipts = %+v, error = %v", fake.Receipts(), err)
	}
}
