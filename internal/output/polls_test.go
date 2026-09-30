package output_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	pollCreateOperation = "create"
	pollVoteOperation   = "vote"
)

func requirePollFields(t *testing.T, got string, fields ...string) {
	t.Helper()

	for _, field := range fields {
		if !strings.Contains(got, field) {
			t.Errorf("missing %s in %s", field, got)
		}
	}
}

func TestPollEvents(t *testing.T) {
	t.Parallel()

	env := incoming("").Envelope
	poll := &signal.Poll{Question: "Lunch?\n\x1b", Options: []string{"A\t", "B"}, AllowMultiple: true}
	msg := incoming("")
	msg.Poll = poll

	tests := []struct {
		name   string
		event  signal.Event
		fields []string
	}{
		{"creation", msg, []string{
			`"type":"message"`,
			`"poll":{"question":"Lunch?\n\u001b","options":["A\t","B"],"allowMultiple":true}`,
		}},
		{pollVoteOperation, &signal.PollVote{Envelope: env, OutgoingPollVote: signal.OutgoingPollVote{
			TargetAuthor: signal.Recipient{ACI: aliceACI}, TargetTimestamp: 12, VoteCount: 2,
		}}, []string{
			`"type":"pollVote"`, `"targetTimestamp":12`, `"voteCount":2`, `"optionIndexes":[]`,
			`"targetAuthor":{"aci":"` + aliceACI + `"}`,
		}},
		{
			"close", &signal.PollClose{Envelope: env, OutgoingPollClose: signal.OutgoingPollClose{TargetTimestamp: 12}},
			[]string{`"type":"pollClose"`, `"targetTimestamp":12`},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := renderEvent(t, output.JSON, test.event)
			requirePollFields(t, got, test.fields...)
			requirePollFields(t, got, `"sync":false`, `"sender":{"aci":"`+aliceACI+`"}`)

			plain := renderEvent(t, output.Plain, test.event)
			if strings.Contains(plain, "unsupported") {
				t.Errorf("unsupported poll: %s", plain)
			}
		})
	}

	plain := renderEvent(t, output.Plain, msg)
	requirePollFields(t, plain, `Lunch?\n\u001b`, `0: A\t`, `1: B`)

	if strings.Count(plain, "\n") != 1 {
		t.Errorf("unsafe output %q", plain)
	}
}

func TestPollSend(t *testing.T) {
	t.Parallel()

	for _, operation := range []string{pollCreateOperation, pollVoteOperation, "close"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			res := app.PollSendResult{
				Operation: operation, TargetAuthor: signal.Recipient{ACI: aliceACI}, TargetTimestamp: 12,
				VoteCount: 3, SendResult: app.SendResult{Timestamp: 20},
			}
			if operation == pollCreateOperation {
				res.Poll = &signal.Poll{Question: "Q", Options: []string{"A", "B"}}
			}

			err := output.New(&buf, output.JSON, nil).PollSend(res)
			if err != nil {
				t.Fatal(err)
			}

			requirePollFields(t, buf.String(), `"version":1`, `"poll":{`, `"operation":"`+operation+`"`,
				`"timestamp":20`, `"results":[]`, `"targetTimestamp":12`)

			if operation == pollVoteOperation {
				requirePollFields(t, buf.String(), `"optionIndexes":[]`, `"voteCount":3`)
			}

			if operation == pollCreateOperation {
				requirePollFields(t, buf.String(), `"creation":{"question":"Q"`)
			}

			buf.Reset()

			err = output.New(&buf, output.Plain, nil).PollSend(res)
			if err != nil {
				t.Fatal(err)
			}

			requirePollFields(t, buf.String(), "STATUS")
		})
	}
}

func TestPollStateObservations(t *testing.T) {
	t.Parallel()

	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "known"}[known], func(t *testing.T) {
			t.Parallel()

			state := app.PollState{
				Author: signal.Recipient{ACI: aliceACI}, Timestamp: 12, Scanned: 7, FirstEntryID: 1, LastEntryID: 7,
			}
			if known {
				state.Creation = &signal.Poll{Question: "Q\n", Options: []string{"A", "B"}}
				state.Tally = []int{0, 1}
				state.ClosureObserved = true
				state.ClosedAt = 20
			}

			checkPollStateJSON(t, state, known)

			var buf bytes.Buffer

			err := output.New(&buf, output.Plain, nil).PollState(state)
			if err != nil {
				t.Fatal(err)
			}

			requirePollFields(t, buf.String(), "retained observations", "unknown")

			if !known {
				requirePollFields(t, buf.String(), "creation unavailable", "closure not observed")
			}
		})
	}
}

func checkPollStateJSON(t *testing.T, state app.PollState, known bool) {
	t.Helper()

	var buf bytes.Buffer

	err := output.New(&buf, output.JSON, nil).PollState(state)
	if err != nil {
		t.Fatal(err)
	}

	var doc struct {
		Version int
		State   map[string]json.RawMessage `json:"pollState"`
	}

	err = json.Unmarshal(buf.Bytes(), &doc)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Version != 1 || string(doc.State["votes"]) != "[]" || string(doc.State["completeness"]) != `"unknown"` {
		t.Error(buf.String())
	}

	for _, key := range []string{"creation", "tally", "closedAt"} {
		_, present := doc.State[key]
		if present != known {
			t.Errorf("%s presence %v", key, present)
		}
	}
}

func TestPollPartialSend(t *testing.T) {
	t.Parallel()

	res := app.PollSendResult{
		Operation: pollVoteOperation,
		SendResult: app.SendResult{Timestamp: 20, Results: []app.TargetResult{{
			Target: app.Target{GroupID: "group"}, Members: []signal.RecipientResult{
				{Recipient: signal.Recipient{ACI: aliceACI}}, {Recipient: signal.Recipient{ACI: "bob"}, Err: errStickerImage},
			},
		}}},
	}

	for _, format := range []output.Format{output.JSON, output.Plain} {
		var buf bytes.Buffer

		err := output.New(&buf, format, nil).PollSend(res)
		if err != nil {
			t.Fatal(err)
		}

		if format == output.JSON {
			requirePollFields(t, buf.String(), `"success":false`, `"members":[`)
		} else {
			requirePollFields(t, buf.String(), "partial")
		}
	}
}

func TestPollStateDeletedSuppressesTally(t *testing.T) {
	t.Parallel()

	state := app.PollState{
		Creation: &signal.Poll{Question: "Q", Options: []string{"A", "B"}}, Deleted: true, Tally: []int{1, 2},
		Votes: []app.PollStateVote{{Voter: signal.Recipient{ACI: aliceACI}, VoteCount: 3, Timestamp: 4}},
	}

	var buf bytes.Buffer

	err := output.New(&buf, output.JSON, nil).PollState(state)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(buf.String(), `"tally"`) {
		t.Error(buf.String())
	}

	requirePollFields(t, buf.String(), `"optionIndexes":[]`)
}

func TestPollWriterFailures(t *testing.T) {
	t.Parallel()

	for _, format := range []output.Format{output.JSON, output.Plain} {
		printer := output.New(profileFailWriter{}, format, nil)

		err := printer.PollSend(app.PollSendResult{})
		if !errors.Is(err, errProfileWriter) {
			t.Errorf("send writer error = %v", err)
		}

		err = printer.PollState(app.PollState{})
		if !errors.Is(err, errProfileWriter) {
			t.Errorf("state writer error = %v", err)
		}
	}
}
