package cmd_test

import (
	"errors"
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
	pollCmdName      = "polls"
	pollCreateVerb   = "create"
	pollVoteVerb     = "vote"
	pollCloseVerb    = "close"
	pollQuestionFlag = "--question"
	pollOptionFlag   = "--option"
	pollCountFlag    = "--vote-count"
	pollQuestionText = "When?"
	pollToday        = "Today"
	pollTomorrow     = "Tomorrow"
	pollFirst        = "one"
	pollSecond       = "two"
)

func TestPollCreateCommand(t *testing.T) {
	t.Parallel()

	fake := sendFake()

	out, err := runSend(t, fake, "", "-o", "json", pollCmdName, pollCreateVerb, groupFlag, groupID,
		pollQuestionFlag, pollQuestionText, pollOptionFlag, pollToday, pollOptionFlag, pollTomorrow)
	if err != nil {
		t.Fatalf("create poll: %v", err)
	}

	if !strings.Contains(out, `"operation":"create"`) || !strings.Contains(out, `"success":true`) {
		t.Fatalf("poll creation outcome: %s", out)
	}

	if len(fake.Sent()) != 1 || fake.Sent()[0].GroupID != groupID {
		t.Fatal("poll was not sent to the group")
	}

	if poll := fake.Sent()[0].PollCreate; poll == nil || poll.Question != pollQuestionText || !poll.AllowMultiple ||
		!reflect.DeepEqual(poll.Options, []string{pollToday, pollTomorrow}) {
		t.Fatal("poll creation data or multiple-choice default changed")
	}
}

func TestPollRepeatedCounter(t *testing.T) {
	t.Parallel()

	fake := sendFake()

	out, err := runSend(t, fake, "", "-o", formatJSON, pollCmdName, pollVoteVerb, groupFlag, groupID,
		targetFlg, aliceACI+":"+targetTS, pollOptionFlag, "0", pollCountFlag, "1", pollCountFlag, "2")
	if err != nil {
		t.Fatalf("vote with repeated counter: %v", err)
	}

	sent := fake.Sent()
	if len(sent) != 1 || sent[0].PollVote == nil {
		t.Fatalf("expected one poll vote: %+v", sent)
	}

	if sent[0].PollVote.VoteCount != 2 {
		t.Fatalf("repeated counter = %d, want 2", sent[0].PollVote.VoteCount)
	}

	if !strings.Contains(out, `"voteCount":2`) {
		t.Fatalf("last counter missing from JSON output: %s", out)
	}
}

func TestPollCommands(t *testing.T) {
	t.Parallel()

	for _, operation := range []struct {
		name string
		args []string
	}{
		{pollCreateVerb, []string{
			pollQuestionFlag, pollQuestionText, pollOptionFlag, pollToday,
			pollOptionFlag, pollTomorrow,
		}},
		{"create_single", []string{
			pollQuestionFlag, pollQuestionText, pollOptionFlag, pollToday,
			pollOptionFlag, pollTomorrow, "--single-choice",
		}},
		{pollVoteVerb, []string{
			targetFlg, aliceACI + ":" + targetTS, pollCountFlag, "1",
			pollOptionFlag, "0", pollOptionFlag, "1",
		}},
		{"vote_clear", []string{targetFlg, aliceACI + ":" + targetTS, pollCountFlag, "2", "--clear"}},
		{pollCloseVerb, []string{targetFlg, targetTS}},
	} {
		for _, format := range []string{formatPlain, formatJSON} {
			t.Run(operation.name+"_"+format, func(t *testing.T) {
				t.Parallel()

				fake := sendFake()
				verb, _, _ := strings.Cut(operation.name, "_")
				args := append([]string{"-o", format, pollCmdName, verb, groupFlag, groupID}, operation.args...)

				out, err := runSend(t, fake, "", args...)
				if err != nil {
					t.Fatalf("%s: %v", verb, err)
				}

				golden(t, "poll_"+operation.name+"_"+format, out)

				sent := fake.Sent()
				if len(sent) != 1 || sent[0].GroupID != groupID || sent[0].Timestamp != sentAt {
					t.Fatalf("wrong poll destination/timestamp: %+v", sent)
				}

				checkPollCommandPayload(t, operation.name, sent[0])
			})
		}
	}
}

func checkPollCommandPayload(t *testing.T, operation string, sent signal.SendRequest) {
	t.Helper()

	switch operation {
	case "create_single":
		want := &signal.Poll{Question: pollQuestionText, Options: []string{pollToday, pollTomorrow}, AllowMultiple: false}
		if !reflect.DeepEqual(sent.PollCreate, want) {
			t.Fatal("single-choice flag ignored")
		}
	case pollVoteVerb:
		want := &signal.OutgoingPollVote{
			TargetAuthor: signal.Recipient{ACI: aliceACI}, TargetTimestamp: 1789999999000,
			OptionIndexes: []uint32{0, 1}, VoteCount: 1,
		}
		if !reflect.DeepEqual(sent.PollVote, want) {
			t.Fatal("vote indexes/counter/author changed")
		}
	case "vote_clear":
		want := &signal.OutgoingPollVote{
			TargetAuthor: signal.Recipient{ACI: aliceACI}, TargetTimestamp: 1789999999000,
			OptionIndexes: []uint32{}, VoteCount: 2,
		}
		if !reflect.DeepEqual(sent.PollVote, want) {
			t.Fatal("explicit withdrawal was not sent")
		}
	case pollCloseVerb:
		if !reflect.DeepEqual(sent.PollClose, &signal.OutgoingPollClose{TargetTimestamp: 1789999999000}) {
			t.Fatal("closure target changed")
		}
	}
}

func TestPollPartialFailure(t *testing.T) {
	t.Parallel()

	fake := sendFake()
	fake.SendFailures = map[string]error{carolACI: errUnreachable}

	out, err := runSend(t, fake, "", "-o", formatJSON, pollCmdName, pollCloseVerb, groupFlag, groupID, targetFlg, targetTS)
	if !errors.Is(err, app.ErrSendFailed) || !strings.Contains(out, `"success":false`) ||
		!strings.Contains(out, `"success":true`) || len(fake.Sent()) != 1 {
		t.Fatalf("partial result/no-retry: %s, %v", out, err)
	}

	golden(t, "poll_partial_json", out)
}

func TestPollPreflight(t *testing.T) {
	t.Parallel()

	for _, args := range pollPreflightCases() {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()

			fake := &signaltest.Fake{OpenErr: errUnreachable}

			_, err := runSend(t, fake, "", append([]string{pollCmdName}, args...)...)
			if err == nil || errors.Is(err, errUnreachable) {
				t.Fatalf("invalid poll arguments opened unavailable account: %v", err)
			}
		})
	}
}

func pollPreflightCases() [][]string {
	return [][]string{
		{pollCreateVerb, groupFlag, groupID, pollQuestionFlag, "Q", pollOptionFlag, pollFirst},
		{
			pollCreateVerb, groupFlag, groupID, pollQuestionFlag, " ", pollOptionFlag, pollFirst,
			pollOptionFlag, pollSecond,
		},
		{
			pollCreateVerb, groupFlag, groupID, pollQuestionFlag, "Q", pollOptionFlag, pollFirst,
			pollOptionFlag, pollSecond, groupFlag, groupID,
		},
		{
			pollCreateVerb, groupFlag, aliceACI, pollQuestionFlag, "Q", pollOptionFlag, pollFirst,
			pollOptionFlag, pollSecond,
		},
		{
			pollCreateVerb, groupFlag, groupID, pollQuestionFlag, "Q", pollOptionFlag, pollFirst,
			pollOptionFlag, pollSecond, "--message", "mixed",
		},
		{pollCreateVerb, pollQuestionFlag, "Q", pollOptionFlag, pollFirst, pollOptionFlag, pollSecond},
		{pollVoteVerb, groupFlag, groupID, targetFlg, aliceACI + ":" + targetTS, pollOptionFlag, "0"},
		{
			pollVoteVerb, groupFlag, groupID, targetFlg, aliceACI + ":" + targetTS, pollCountFlag, "0",
			pollOptionFlag, "0",
		},
		{
			pollVoteVerb, groupFlag, groupID, targetFlg, aliceACI + ":" + targetTS, pollCountFlag,
			"4294967296", pollOptionFlag, "0",
		},
		{pollVoteVerb, groupFlag, groupID, targetFlg, aliceACI + ":" + targetTS, pollCountFlag, "1"},
		{
			pollVoteVerb, groupFlag, groupID, targetFlg, "00000000-0000-0000-0000-000000000000:" + targetTS,
			pollCountFlag, "1", pollOptionFlag, "0",
		},
		{
			pollVoteVerb, groupFlag, groupID, targetFlg, aliceACI + ":" + targetTS, pollCountFlag, "1",
			pollOptionFlag, "-1",
		},
		{
			pollVoteVerb, groupFlag, groupID, targetFlg, aliceACI + ":" + targetTS, pollCountFlag, "1",
			pollOptionFlag, "4294967296",
		},
		{
			pollVoteVerb, groupFlag, groupID, targetFlg, aliceACI + ":" + targetTS, pollCountFlag, "1",
			pollOptionFlag, "10",
		},
		{
			pollVoteVerb, groupFlag, groupID, targetFlg, aliceACI + ":" + targetTS, pollCountFlag, "1",
			pollOptionFlag, "0", pollOptionFlag, "0",
		},
		{
			pollVoteVerb, groupFlag, groupID, targetFlg, aliceACI + ":" + targetTS, pollCountFlag, "1",
			pollOptionFlag, "0", "--clear",
		},
		{pollCloseVerb, groupFlag, groupID, targetFlg, "0"},
		{pollCloseVerb, groupFlag, groupID, targetFlg, targetTS, aliceNumber},
		{showCmd, groupFlag, groupID, targetFlg, aliceNumber + ":" + targetTS},
		{showCmd, groupFlag, groupID, targetFlg, aliceACI + ":" + targetTS, "--scan-limit", "10001"},
	}
}

func TestPollShowOffline(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := sendFake()
			fake.ConnectErr = errUnreachable

			out, err := runSend(t, fake, "", "-o", format, pollCmdName, showCmd, groupFlag, groupID,
				targetFlg, aliceACI+":"+targetTS)
			if err != nil || len(fake.Connects()) != 0 || len(fake.Sent()) != 0 {
				t.Fatalf("local show connected: %s, %v", out, err)
			}

			golden(t, "poll_show_missing_"+format, out)
		})
	}
}

func TestReceivePolls(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			golden(t, "receive_polls_"+format, receiveAll(t, pollCommandEvents(), "-o", format))
		})
	}
}

func pollCommandEvents() []signal.Event {
	chat := signal.Chat{GroupID: groupID}
	author := signal.Recipient{ACI: aliceACI}

	return []signal.Event{
		&signal.Message{
			Envelope: signal.Envelope{Sender: author, Chat: chat, Timestamp: at(1)},
			Poll:     &signal.Poll{Question: pollQuestionText, Options: []string{pollToday, pollTomorrow}, AllowMultiple: true},
		},
		&signal.PollVote{
			Envelope: signal.Envelope{Sender: signal.Recipient{ACI: bobACI}, Chat: chat, Timestamp: at(2)},
			OutgoingPollVote: signal.OutgoingPollVote{
				TargetAuthor: author, TargetTimestamp: at(1),
				OptionIndexes: []uint32{0}, VoteCount: 1,
			},
		},
		&signal.PollClose{
			Envelope:          signal.Envelope{Sender: author, Chat: chat, Timestamp: at(3)},
			OutgoingPollClose: signal.OutgoingPollClose{TargetTimestamp: at(1)},
		},
	}
}

func TestPollShowRetainedInbox(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := sendFake()

			client, err := fake.Factory(t.Context(), signal.Options{})
			if err != nil {
				t.Fatal(err)
			}

			for _, evt := range pollCommandEvents() {
				_, err = client.InboxAdd(t.Context(), signal.InboxEntry{
					Chat:  signal.Chat{GroupID: groupID},
					Event: evt, ReceivedAt: time.UnixMilli(sentAt), Time: time.UnixMilli(sentAt),
				})
				if err != nil {
					t.Fatal(err)
				}
			}

			err = client.Close()
			if err != nil {
				t.Fatal(err)
			}

			fake.ConnectErr = errUnreachable

			out, err := runSend(t, fake, "", "-o", format, pollCmdName, showCmd, groupFlag, groupID,
				targetFlg, aliceACI+":"+strconv.FormatUint(at(1), 10))
			if err != nil {
				t.Fatal(err)
			}

			golden(t, "poll_show_retained_"+format, out)

			out, err = runSend(t, fake, "", "-o", format, pollCmdName, showCmd, groupFlag, groupID,
				targetFlg, aliceACI+":"+strconv.FormatUint(at(1), 10), "--scan-limit", "1")
			if err != nil || len(fake.Connects()) != 0 || len(fake.Sent()) != 0 {
				t.Fatalf("retained show connected: %s, %v", out, err)
			}

			golden(t, "poll_show_truncated_"+format, out)
		})
	}
}
