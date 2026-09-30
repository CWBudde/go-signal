package cmd_test

import (
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	pinsCmdName     = "pins"
	pinAddVerb      = "add"
	pinRemoveVerb   = "remove"
	pinDurationFlag = "--duration"
	pinForeverFlag  = "--forever"
	pinChatFlag     = "--chat"
	pinScanFlag     = "--scan-limit"
)

func pinCommandFake() *signaltest.Fake {
	fake := sendFake()
	fake.GroupInfo = map[string]signal.Group{groupID: {ID: groupID, Members: []signal.GroupMember{
		{Recipient: signal.Recipient{ACI: testAccount().ACI}, Role: signal.GroupRoleAdmin},
		{Recipient: signal.Recipient{ACI: aliceACI}, Role: signal.GroupRoleMember},
		{Recipient: signal.Recipient{ACI: carolACI}, Role: signal.GroupRoleMember},
	}}}

	return fake
}

func TestPinCommands(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, verb string
		flags      []string
		forever    bool
		seconds    uint32
	}{
		{"finite", pinAddVerb, []string{pinDurationFlag, "86400"}, false, 86400},
		{"forever", pinAddVerb, []string{pinForeverFlag}, true, 0},
		{"remove", pinRemoveVerb, nil, false, 0},
	} {
		for _, format := range []string{formatPlain, formatJSON} {
			t.Run(test.name+"_"+format, func(t *testing.T) {
				t.Parallel()

				fake := pinCommandFake()
				args := []string{
					"-o", format, pinsCmdName, test.verb, aliceNumber, app.SelfRecipient,
					groupFlag, groupID, targetFlg, aliceNumber + ":" + targetTS,
				}

				out, err := runSend(t, fake, "", append(args, test.flags...)...)
				if err != nil {
					t.Fatal(err)
				}

				golden(t, "pins_"+test.name+"_"+format, out)

				sent := fake.Sent()
				if len(sent) != 2 || sent[1].GroupID != groupID || sent[0].Timestamp != sentAt || len(sent[0].Recipients) != 2 {
					t.Fatalf("wrong pin sends: %+v", sent)
				}

				for _, req := range sent {
					checkPinCommandRequest(t, req, test.verb, test.seconds, test.forever)
				}
			})
		}
	}
}

func checkPinCommandRequest(t *testing.T, req signal.SendRequest, verb string, seconds uint32, forever bool) {
	t.Helper()

	author := signal.Recipient{ACI: aliceACI, Number: aliceNumber}
	if verb == pinAddVerb {
		want := &signal.OutgoingPin{
			TargetAuthor: author, TargetTimestamp: 1789999999000,
			DurationSeconds: seconds, Forever: forever,
		}
		if !reflect.DeepEqual(req.Pin, want) {
			t.Fatalf("pin = %+v; want %+v", req.Pin, want)
		}
	} else {
		want := &signal.OutgoingUnpin{TargetAuthor: author, TargetTimestamp: 1789999999000}
		if !reflect.DeepEqual(req.Unpin, want) {
			t.Fatalf("unpin = %+v; want %+v", req.Unpin, want)
		}
	}
}

func TestPinRepeatedDuration(t *testing.T) {
	t.Parallel()

	fake := pinCommandFake()

	out, err := runSend(t, fake, "", "-o", formatJSON, pinsCmdName, pinAddVerb, aliceNumber, targetFlg, selfTarget,
		pinDurationFlag, "1", pinDurationFlag, "4294967295")
	if err != nil {
		t.Fatal(err)
	}

	sent := fake.Sent()
	if len(sent) != 1 || sent[0].Pin == nil || sent[0].Pin.DurationSeconds != math.MaxUint32 ||
		sent[0].Pin.TargetAuthor.ACI != testAccount().ACI || !strings.Contains(out, `"durationSeconds":4294967295`) {
		t.Fatalf("last scalar duration/self author not preserved: %s %+v", out, sent)
	}
}

func TestPinPreflight(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{pinAddVerb, aliceNumber, targetFlg, selfTarget},
		{pinAddVerb, aliceNumber, targetFlg, selfTarget, pinForeverFlag + "=false"},
		{pinAddVerb, aliceNumber, targetFlg, selfTarget, pinDurationFlag, "0"},
		{pinAddVerb, aliceNumber, targetFlg, selfTarget, pinDurationFlag, "-1"},
		{pinAddVerb, aliceNumber, targetFlg, selfTarget, pinDurationFlag, "4294967296"},
		{pinAddVerb, aliceNumber, targetFlg, selfTarget, pinDurationFlag, "1", pinForeverFlag},
		{pinAddVerb, aliceNumber, pinDurationFlag, "1"},
		{pinAddVerb, targetFlg, selfTarget, pinDurationFlag, "1"},
		{pinAddVerb, aliceNumber, targetFlg, "00000000-0000-0000-0000-000000000000:" + targetTS, pinForeverFlag},
		{pinRemoveVerb, aliceNumber, targetFlg, "self:0"},
		{pinRemoveVerb, aliceNumber, targetFlg, selfTarget, pinForeverFlag},
		{listCmd, pinChatFlag, aliceNumber},
		{listCmd, pinChatFlag, app.SelfRecipient},
		{listCmd, pinChatFlag, "00000000-0000-0000-0000-000000000000"},
		{listCmd, pinChatFlag, aliceACI, pinScanFlag, "10001"},
		{listCmd, pinChatFlag, aliceACI, pinScanFlag, "-1"},
		{listCmd, pinChatFlag, aliceACI, "pin-extra-positional"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()

			fake := &signaltest.Fake{OpenErr: errUnreachable}

			_, err := runSend(t, fake, "", append([]string{pinsCmdName}, args...)...)
			if err == nil || errors.Is(err, errUnreachable) {
				t.Fatalf("invalid arguments opened client: %v", err)
			}
		})
	}
}

func TestPinPartialFailure(t *testing.T) {
	t.Parallel()

	fake := pinCommandFake()
	fake.SendFailures = map[string]error{carolACI: errUnreachable}

	out, err := runSend(t, fake, "", "-o", formatJSON, pinsCmdName, pinAddVerb, groupFlag, groupID,
		targetFlg, selfTarget, pinForeverFlag)
	if !errors.Is(err, app.ErrSendFailed) || !strings.Contains(out, `"success":false`) ||
		!strings.Contains(out, `"success":true`) || len(fake.Sent()) != 1 {
		t.Fatalf("partial results/retries: %s %v", out, err)
	}

	golden(t, "pins_partial_json", out)
}

func pinCommandEvents() []signal.Event {
	author := signal.Recipient{ACI: aliceACI}
	chat := signal.Chat{GroupID: groupID}

	return []signal.Event{
		&signal.Pin{
			Envelope:    signal.Envelope{Sender: author, Chat: chat, Timestamp: at(2)},
			OutgoingPin: signal.OutgoingPin{TargetAuthor: author, TargetTimestamp: at(1), DurationSeconds: 86400},
		},
		&signal.Pin{
			Envelope: signal.Envelope{
				Sender: signal.Recipient{ACI: testAccount().ACI},
				Chat:   chat, Timestamp: at(3), Sync: true,
			},
			OutgoingPin: signal.OutgoingPin{TargetAuthor: author, TargetTimestamp: at(1) + 1, Forever: true},
		},
		&signal.Unpin{
			Envelope:      signal.Envelope{Sender: author, Chat: chat, Timestamp: at(4)},
			OutgoingUnpin: signal.OutgoingUnpin{TargetAuthor: author, TargetTimestamp: at(1)},
		},
	}
}

func TestReceivePins(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			golden(t, "receive_pins_"+format, receiveAll(t, pinCommandEvents(), "-o", format))
		})
	}
}

func TestPinListOffline(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := pinCommandFake()
			fake.ConnectErr = errUnreachable

			out, err := runSend(t, fake, "", "-o", format, pinsCmdName, listCmd, pinChatFlag, app.GroupPrefix+groupID)
			if err != nil {
				t.Fatal(err)
			}

			golden(t, "pins_list_empty_"+format, out)

			seedPinInbox(t, fake)

			out, err = runSend(t, fake, "", "-o", format, pinsCmdName, listCmd, pinChatFlag, app.GroupPrefix+groupID)
			if err != nil {
				t.Fatal(err)
			}

			golden(t, "pins_list_retained_"+format, out)

			out, err = runSend(t, fake, "", "-o", format, pinsCmdName, listCmd, pinChatFlag, app.GroupPrefix+groupID,
				pinScanFlag, strconv.Itoa(1))
			if err != nil {
				t.Fatal(err)
			}

			golden(t, "pins_list_truncated_"+format, out)

			if len(fake.Connects()) != 0 || len(fake.Sent()) != 0 {
				t.Fatal("offline list connected or sent")
			}
		})
	}
}

func seedPinInbox(t *testing.T, fake *signaltest.Fake) {
	t.Helper()

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		err := client.Close()
		if err != nil {
			t.Error(err)
		}
	}()

	for i, event := range pinCommandEvents() {
		received := time.UnixMilli(sentAt + int64(i))

		_, err := client.InboxAdd(t.Context(), signal.InboxEntry{
			Chat:  signal.Chat{GroupID: groupID},
			Event: event, ReceivedAt: received, Time: received,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
