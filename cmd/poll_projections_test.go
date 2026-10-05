package cmd_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestPollShowDurable(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := sendFake()

			client, err := fake.Factory(t.Context(), signal.Options{})
			if err != nil {
				t.Fatal(err)
			}

			for _, event := range pollCommandEvents() {
				_, err = client.InboxAdd(t.Context(), signal.InboxEntry{Chat: signal.Chat{GroupID: groupID}, Event: event})
				if err != nil {
					t.Fatal(err)
				}
			}

			_, err = client.InboxPrune(t.Context(), time.Now().Add(time.Hour), 0)
			if err != nil {
				t.Fatal(err)
			}

			err = client.Close()
			if err != nil {
				t.Fatal(err)
			}

			fake.ConnectErr = errUnreachable

			out, err := runSend(t, fake, "", "-o", format, pollCmdName, showCmd, groupFlag, groupID,
				targetFlg, aliceACI+":"+strconv.FormatUint(at(1), 10), "--durable")
			if err != nil {
				t.Fatal(err)
			}

			if len(fake.Connects()) != 0 || len(fake.Sent()) != 0 {
				t.Fatal("durable view connected/sent")
			}

			golden(t, "poll_show_durable_"+format, out)
		})
	}
}

func TestDurablePollScanLimitRejectedBeforeOpen(t *testing.T) {
	t.Parallel()

	fake := sendFake()

	_, err := runSend(t, fake, "", pollCmdName, showCmd, groupFlag, groupID, targetFlg, aliceACI+":"+targetTS,
		"--durable", "--scan-limit", "1000")
	if err == nil || len(fake.Opened()) != 0 {
		t.Fatalf("combined view opened client: %v, %+v", err, fake.Opened())
	}
}
