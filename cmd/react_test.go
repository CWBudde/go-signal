package cmd_test

import (
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	reactCmd    = "react"
	deleteCmd   = "delete"
	targetFlg   = "--target"
	targetTS    = "1789999999000"
	thumbsUp    = "👍"
	groupFlag   = "--group"
	emojiFlag   = "--emoji"
	selfTarget  = app.SelfRecipient + ":" + targetTS
	formatJSON  = "json"
	formatPlain = "plain"
)

func TestReact(t *testing.T) {
	t.Parallel()

	args := []string{
		reactCmd, aliceNumber, app.SelfRecipient, groupFlag, groupID,
		targetFlg, aliceNumber + ":" + targetTS, emojiFlag, thumbsUp,
	}

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := sendFake()

			out, err := runSend(t, fake, "", append([]string{"-o", format}, args...)...)
			if err != nil {
				t.Fatalf("react: %v", err)
			}

			golden(t, "react_"+format, out)

			sent := fake.Sent()
			if len(sent) != 2 || sent[0].Reaction == nil || sent[1].GroupID != groupID ||
				sent[1].Reaction == nil || sent[1].Reaction.TargetAuthor.ACI != aliceACI {
				t.Errorf("sent %+v", sent)
			}
		})
	}
}

func TestReactRemove(t *testing.T) {
	t.Parallel()

	fake := sendFake()

	out, err := runSend(t, fake, "", "-o", formatJSON, reactCmd, "@bob.42",
		targetFlg, selfTarget, "-e", thumbsUp, "--remove")
	if err != nil {
		t.Fatalf("react: %v", err)
	}

	golden(t, "react_remove_json", out)

	sent := fake.Sent()
	if len(sent) != 1 || !sent[0].Reaction.Remove || sent[0].Reaction.TargetAuthor.ACI != testAccount().ACI {
		t.Errorf("sent %+v", sent)
	}
}

func TestReactPartialFailure(t *testing.T) {
	t.Parallel()

	fake := sendFake()
	fake.SendFailures = map[string]error{carolACI: errUnreachable}

	_, err := runSend(t, fake, "", reactCmd, groupFlag, groupID, targetFlg, selfTarget, emojiFlag, thumbsUp)
	if !errors.Is(err, app.ErrSendFailed) {
		t.Fatalf("got %v, want ErrSendFailed", err)
	}

	if code := cmd.ExitCode(err); code != cmd.ExitFailure {
		t.Errorf("exit code %d, want %d", code, cmd.ExitFailure)
	}
}

func TestReactInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want error
	}{
		{"bad emoji", []string{aliceNumber, targetFlg, selfTarget, emojiFlag, "yes"}, app.ErrInvalidEmoji},
		{"bad target", []string{aliceNumber, targetFlg, "1790000000000", emojiFlag, thumbsUp}, app.ErrInvalidTarget},
		{"no recipient", []string{targetFlg, selfTarget, emojiFlag, thumbsUp}, app.ErrNoRecipients},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := sendFake()

			_, err := runSend(t, fake, "", append([]string{reactCmd}, test.args...)...)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}

			if len(fake.Connects()) != 0 {
				t.Error("connected with an invalid request")
			}
		})
	}
}

func TestReactNeedsTargetAndEmoji(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{reactCmd, aliceNumber, emojiFlag, thumbsUp},
		{reactCmd, aliceNumber, targetFlg, selfTarget},
		{deleteCmd, aliceNumber},
	} {
		_, err := runSend(t, sendFake(), "", args...)
		if err == nil {
			t.Errorf("%v: no error for a missing flag", args)
		}
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()

	args := []string{deleteCmd, aliceNumber, app.SelfRecipient, groupFlag, groupID, targetFlg, targetTS}

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := sendFake()

			out, err := runSend(t, fake, "", append([]string{"-o", format}, args...)...)
			if err != nil {
				t.Fatalf("delete: %v", err)
			}

			golden(t, "delete_"+format, out)

			sent := fake.Sent()
			if len(sent) != 2 || sent[0].DeleteTarget != 1789999999000 || sent[1].GroupID != groupID {
				t.Errorf("sent %+v", sent)
			}
		})
	}
}

func TestDeleteUnlinked(t *testing.T) {
	t.Parallel()

	fake := sendFake()
	fake.Incoming = []signal.Event{&signal.Connection{State: signal.StateLoggedOut}}

	_, err := runSend(t, fake, "", deleteCmd, aliceNumber, targetFlg, targetTS)
	if code := cmd.ExitCode(err); code != cmd.ExitUnlinked {
		t.Errorf("got %v (exit code %d), want exit code %d", err, code, cmd.ExitUnlinked)
	}
}
