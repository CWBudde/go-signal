package cmd_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const receiptTimestampFlag = "--timestamp"

func TestReceiptsSendViewed(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := sendFake()
			fake.Contacts = []signal.Contact{{
				Recipient: signal.Recipient{ACI: aliceACI, Number: aliceNumber}, ContactName: "Alice",
			}}
			fake.Incoming = []signal.Event{&signal.Message{Envelope: signal.Envelope{Timestamp: sentAt}}}

			out, err := runSend(t, fake, "", "-o", format, "receipts", "send-viewed", aliceNumber,
				receiptTimestampFlag, targetTS, receiptTimestampFlag, "1789999999001", receiptTimestampFlag, targetTS)
			if err != nil {
				t.Fatalf("send viewed: %v", err)
			}

			golden(t, "receipts_viewed_"+format, out)

			want := []signaltest.ReceiptCall{{
				Sender: signal.Recipient{ACI: aliceACI, Number: aliceNumber},
				Type:   signal.ReceiptViewed, Timestamps: []uint64{1789999999000, 1789999999001},
			}}
			if got := fake.Receipts(); !reflect.DeepEqual(got, want) {
				t.Errorf("receipts = %+v; want %+v", got, want)
			}

			if fake.Delivered() != 0 {
				t.Error("send-viewed consumed incoming events")
			}
		})
	}
}

func TestReceiptsSendViewedInvalid(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{aliceNumber},
		{receiptTimestampFlag, "1"},
		{aliceNumber, bobACI, receiptTimestampFlag, "1"},
		{aliceNumber, receiptTimestampFlag, "yesterday"},
		{aliceNumber, receiptTimestampFlag, "-1"},
		{aliceNumber, receiptTimestampFlag, "18446744073709551616"},
		{aliceNumber, receiptTimestampFlag, "0"},
	} {
		fake := sendFake()

		_, err := runSend(t, fake, "", append([]string{"receipts", "send-viewed"}, args...)...)
		if err == nil || len(fake.Connects()) != 0 || len(fake.Receipts()) != 0 {
			t.Errorf("args %v: error = %v, connects = %v, receipts = %+v", args, err, fake.Connects(), fake.Receipts())
		}
	}
}

func TestReceiptsSendViewedFailure(t *testing.T) {
	t.Parallel()

	for _, failure := range []error{errUnreachable, signal.ErrDeviceUnlinked} {
		fake := sendFake()
		fake.ReceiptErr = failure

		out, err := runSend(t, fake, "", "receipts", "send-viewed", aliceNumber, receiptTimestampFlag, targetTS)
		if !errors.Is(err, failure) || out != "" {
			t.Fatalf("output = %q, error = %v; want %v", out, err, failure)
		}

		if errors.Is(failure, signal.ErrDeviceUnlinked) && cmd.ExitCode(err) != cmd.ExitUnlinked {
			t.Error("remote unlink did not map to exit 3")
		}
	}
}
