package output_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	pinOperation   = "pin"
	unpinOperation = "unpin"
	pinGroupID     = "pins-chat"
	pinUnknownJSON = `"unknown"`
)

func pinFields(t *testing.T, data []byte) map[string]json.RawMessage {
	t.Helper()

	var fields map[string]json.RawMessage

	err := json.Unmarshal(data, &fields)
	if err != nil {
		t.Fatal(err)
	}

	return fields
}

func requirePinField(t *testing.T, fields map[string]json.RawMessage, key, want string) {
	t.Helper()

	if got := string(fields[key]); got != want {
		t.Errorf("%s = %s, want %s", key, got, want)
	}
}

func TestPinSendJSON(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		operation string
		duration  uint32
		forever   bool
	}{
		{"finite", pinOperation, math.MaxUint32, false},
		{"unlimited", pinOperation, 0, true},
		{"remove", unpinOperation, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			res := app.PinSendResult{
				SendResult: app.SendResult{Timestamp: 20}, Operation: test.operation,
				TargetAuthor: signal.Recipient{ACI: aliceACI}, TargetTimestamp: math.MaxUint64,
				DurationSeconds: test.duration, Forever: test.forever,
			}

			err := output.New(&buf, output.JSON, nil).PinSend(res)
			if err != nil {
				t.Fatal(err)
			}

			doc := pinFields(t, buf.Bytes())
			requirePinField(t, doc, "version", "1")
			pin := pinFields(t, doc["pin"])
			requirePinField(t, pin, "operation", `"`+test.operation+`"`)
			requirePinField(t, pin, "targetAuthor", `{"aci":"`+aliceACI+`"}`)
			requirePinField(t, pin, "targetTimestamp", "18446744073709551615")
			requirePinField(t, pin, "timestamp", "20")
			requirePinField(t, pin, "results", "[]")

			checkPinDuration(t, pin, test.operation, test.duration, test.forever)
		})
	}
}

func checkPinDuration(
	t *testing.T, fields map[string]json.RawMessage, operation string, duration uint32, forever bool,
) {
	t.Helper()

	for _, key := range []string{"durationSeconds", "forever"} {
		if _, present := fields[key]; present != (operation == pinOperation) {
			t.Errorf("%s presence = %t for %s", key, present, operation)
		}
	}

	if operation != pinOperation {
		return
	}

	var gotDuration uint32

	var gotForever bool

	err := json.Unmarshal(fields["durationSeconds"], &gotDuration)
	if err != nil {
		t.Fatal(err)
	}

	err = json.Unmarshal(fields["forever"], &gotForever)
	if err != nil {
		t.Fatal(err)
	}

	if gotDuration != duration || gotForever != forever {
		t.Errorf("duration = %d, forever = %t; want %d, %t", gotDuration, gotForever, duration, forever)
	}
}

func TestPinPartialSend(t *testing.T) {
	t.Parallel()

	res := app.PinSendResult{
		Operation: pinOperation,
		SendResult: app.SendResult{Timestamp: 20, Results: []app.TargetResult{
			{Target: app.Target{GroupID: pinGroupID}, Members: []signal.RecipientResult{
				{Recipient: signal.Recipient{ACI: aliceACI}},
				{Recipient: signal.Recipient{ACI: "bob"}, Err: errStickerImage},
			}},
			{Target: app.Target{Recipient: signal.Recipient{ACI: aliceACI}}, Err: errStickerImage},
			{Target: app.Target{Self: true}},
		}},
	}

	for _, format := range []output.Format{output.JSON, output.Plain} {
		var buf bytes.Buffer

		err := output.New(&buf, format, nil).PinSend(res)
		if err != nil {
			t.Fatal(err)
		}

		if format == output.JSON {
			requirePollFields(t, buf.String(), `"success":false`, `"members":[`, `"type":"self"`, `"success":true`)
		} else {
			requirePollFields(t, buf.String(), "STATUS", "partial", "failed", "sent", "1 of 2 members failed")
		}
	}
}

//nolint:funlen // verifies complete envelopes and duration modes for direct/group and sync controls
func TestPinUnpinEvents(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		operation string
		duration  uint32
		forever   bool
		group     bool
		sync      bool
	}{
		{"finite-direct", pinOperation, math.MaxUint32, false, false, false},
		{"forever-group-sync", pinOperation, 0, true, true, true},
		{"unpin-direct-sync", unpinOperation, 0, false, false, true},
		{"unpin-group", unpinOperation, 0, false, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			env := incoming("").Envelope
			env.Sync = test.sync

			if test.group {
				env.Chat = signal.Chat{GroupID: pinGroupID}
			}

			var event signal.Event = &signal.Unpin{Envelope: env, OutgoingUnpin: signal.OutgoingUnpin{
				TargetAuthor: signal.Recipient{ACI: aliceACI}, TargetTimestamp: math.MaxUint64,
			}}
			if test.operation == pinOperation {
				event = &signal.Pin{Envelope: env, OutgoingPin: signal.OutgoingPin{
					TargetAuthor: signal.Recipient{ACI: aliceACI}, TargetTimestamp: math.MaxUint64,
					DurationSeconds: test.duration, Forever: test.forever,
				}}
			}

			fields := pinFields(t, []byte(renderEvent(t, output.JSON, event)))
			requirePinField(t, fields, "version", "1")
			requirePinField(t, fields, "type", `"`+test.operation+`"`)
			requirePinField(t, fields, "sender", `{"aci":"`+aliceACI+`"}`)
			requirePinField(t, fields, "targetAuthor", `{"aci":"`+aliceACI+`"}`)
			requirePinField(t, fields, "targetTimestamp", "18446744073709551615")
			requirePinField(t, fields, "timestamp", "1789907400000")
			requirePinField(t, fields, "time", `"2026-09-20T12:30:00Z"`)
			requirePinField(t, fields, "sync", strconv.FormatBool(test.sync))

			chat := `{"recipient":{"aci":"` + aliceACI + `"}}`
			if test.group {
				chat = `{"groupId":"` + pinGroupID + `"}`
			}

			requirePinField(t, fields, "chat", chat)
			checkPinDuration(t, fields, test.operation, test.duration, test.forever)
			plain := renderEvent(t, output.Plain, event)
			requirePollFields(t, plain, "[2026-09-20 14:30:00 CEST]", "["+test.operation, "18446744073709551615")

			if test.operation == pinOperation {
				want := "4294967295 seconds"
				if test.forever {
					want = "forever"
				}

				requirePollFields(t, plain, want)
			}
		})
	}
}

func TestPinStateEmpty(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := output.New(&buf, output.JSON, nil).PinState(app.PinState{})
	if err != nil {
		t.Fatal(err)
	}

	doc := pinFields(t, buf.Bytes())
	requirePinField(t, doc, "version", "1")
	state := pinFields(t, doc["pinState"])

	for key, want := range map[string]string{
		"observations": "[]", "completeness": pinUnknownJSON, "scanned": "0",
		"firstEntryId": "0", "lastEntryId": "0", "truncated": strconv.FormatBool(false),
		"ignoredInvalid": "0", "conflicts": "0",
	} {
		requirePinField(t, state, key, want)
	}

	buf.Reset()

	err = output.New(&buf, output.Plain, nil).PinState(app.PinState{})
	if err != nil {
		t.Fatal(err)
	}

	requirePollFields(t, buf.String(), "retained observations", "completeness: unknown",
		"Scanned 0 entries", "No retained observations")
}

//nolint:funlen // verifies snapshot metadata and conditional expiry fields for all retained modes
func TestPinStateRetainsExpiredDeletedUnpin(t *testing.T) {
	t.Parallel()

	received := time.Date(2026, time.September, 30, 12, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	state := app.PinState{
		Chat: signal.Chat{GroupID: pinGroupID}, Completeness: "complete", Scanned: 8, FirstEntryID: 3,
		LastEntryID: 10, Truncated: true, IgnoredInvalid: 2, Conflicts: 1,
		Observations: []app.PinObservation{
			{
				Operation: pinOperation, TargetAuthor: signal.Recipient{ACI: aliceACI}, TargetTimestamp: math.MaxUint64,
				Sender: signal.Recipient{ACI: aliceACI}, Timestamp: 20, EntryID: 4, ReceivedAt: received,
				DurationSeconds: 60, ExpiresAt: received.Add(time.Minute), ExpiryReached: true, TargetDeleted: true,
			},
			{Operation: pinOperation, Forever: true, TargetTimestamp: 12, ReceivedAt: received},
			{Operation: unpinOperation, TargetTimestamp: 13, ReceivedAt: received},
		},
	}

	var buf bytes.Buffer

	err := output.New(&buf, output.JSON, nil).PinState(state)
	if err != nil {
		t.Fatal(err)
	}

	doc := pinFields(t, buf.Bytes())
	fields := pinFields(t, doc["pinState"])

	for key, want := range map[string]string{
		"chat": `{"groupId":"` + pinGroupID + `"}`, "completeness": pinUnknownJSON, "scanned": "8",
		"firstEntryId": "3", "lastEntryId": "10", "truncated": strconv.FormatBool(true),
		"ignoredInvalid": "2", "conflicts": "1",
	} {
		requirePinField(t, fields, key, want)
	}

	var observations []map[string]json.RawMessage

	err = json.Unmarshal(fields["observations"], &observations)
	if err != nil {
		t.Fatal(err)
	}

	if len(observations) != 3 {
		t.Fatalf("got %d observations, want 3", len(observations))
	}

	for key, want := range map[string]string{
		"operation": `"pin"`, "targetAuthor": `{"aci":"` + aliceACI + `"}`,
		"targetTimestamp": "18446744073709551615", "sender": `{"aci":"` + aliceACI + `"}`,
		"timestamp": "20", "entryId": "4", "receivedAt": `"2026-09-30T10:00:00Z"`,
		"expiresAt": `"2026-09-30T10:01:00Z"`, "expiryReached": strconv.FormatBool(true),
		"targetDeleted": strconv.FormatBool(true),
	} {
		requirePinField(t, observations[0], key, want)
	}

	for i, observation := range state.Observations {
		checkPinDuration(t, observations[i], observation.Operation, observation.DurationSeconds, observation.Forever)

		if i == 0 {
			continue
		}

		for _, key := range []string{"expiresAt", "expiryReached"} {
			if _, present := observations[i][key]; present {
				t.Errorf("unexpected %s for observation %d", key, i)
			}
		}
	}
}

func TestPinStatePlainEscapesNamesAndExplainsExpiry(t *testing.T) {
	t.Parallel()

	printer, buf := namedPrinter(output.Plain)
	printer.SetNames(app.NewNames("", []signal.Contact{
		{Recipient: signal.Recipient{ACI: aliceACI}, ProfileName: "Alice\n\x1b"},
	}).WithGroups(map[string]string{pinGroupID: "Room\n\t\x1b"}))

	received := time.Date(2026, time.September, 30, 10, 0, 0, 0, time.UTC)
	state := app.PinState{
		Chat: signal.Chat{GroupID: pinGroupID}, Scanned: 8, FirstEntryID: 3, LastEntryID: 10,
		Truncated: true, IgnoredInvalid: 2, Conflicts: 1,
		Observations: []app.PinObservation{
			{
				Operation: pinOperation, TargetAuthor: signal.Recipient{ACI: aliceACI}, Sender: signal.Recipient{ACI: aliceACI},
				TargetTimestamp: 12, Timestamp: 20, EntryID: 4, ReceivedAt: received, DurationSeconds: 60,
				ExpiresAt: received.Add(time.Minute), ExpiryReached: true, TargetDeleted: true,
			},
			{Operation: pinOperation, Forever: true},
			{Operation: unpinOperation},
		},
	}

	err := printer.PinState(state)
	if err != nil {
		t.Fatal(err)
	}

	requirePollFields(t, buf.String(), "retained observations", "completeness: unknown", `group "Room\n\t\x1b"`,
		`Alice\n\u001b`, "Scanned 8 entries (IDs 3–10)", "truncated: true", "ignored invalid: 2", "conflicts: 1",
		"receipt-based", "expiry reached: true", "target deleted: true", "forever", unpinOperation)

	if strings.ContainsAny(buf.String(), "\x1b\t") {
		t.Errorf("unsafe plain output %q", buf.String())
	}

	buf.Reset()

	state.Chat = signal.Chat{Recipient: signal.Recipient{ACI: aliceACI}}

	err = printer.PinState(state)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(buf.String(), "group:") {
		t.Errorf("direct chat rendered as group: %s", buf.String())
	}
}

func TestPinWriterFailures(t *testing.T) {
	t.Parallel()

	for _, format := range []output.Format{output.JSON, output.Plain} {
		printer := output.New(profileFailWriter{}, format, nil)

		err := printer.PinSend(app.PinSendResult{})
		if !errors.Is(err, errProfileWriter) {
			t.Errorf("send writer error = %v", err)
		}

		err = printer.PinState(app.PinState{})
		if !errors.Is(err, errProfileWriter) {
			t.Errorf("state writer error = %v", err)
		}

		err = printer.Event(&signal.Pin{})
		if !errors.Is(err, errProfileWriter) {
			t.Errorf("event writer error = %v", err)
		}
	}
}
