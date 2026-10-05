package cmd_test

import (
	"strings"
	"testing"
)

func TestPollVoteAutomaticCommand(t *testing.T) {
	t.Parallel()

	fake := sendFake()
	for _, want := range []string{`"voteCount":1`, `"voteCount":2`} {
		out, err := runSend(t, fake, "", "-o", formatJSON, pollCmdName, pollVoteVerb, groupFlag, groupID,
			targetFlg, aliceACI+":"+targetTS, pollOptionFlag, "0")
		if err != nil || !strings.Contains(out, want) {
			t.Fatalf("automatic output = %s, %v", out, err)
		}
	}
}

func TestPollVoteExplicitZeroCommand(t *testing.T) {
	t.Parallel()

	fake := sendFake()

	_, err := runSend(t, fake, "", pollCmdName, pollVoteVerb, groupFlag, groupID,
		targetFlg, aliceACI+":"+targetTS, pollOptionFlag, "0", pollCountFlag, "0")
	if err == nil || len(fake.Opened()) != 0 {
		t.Fatalf("explicit zero preflight = %v", err)
	}
}

func TestPollVoteAutomaticOutput(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatJSON, formatPlain} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := sendFake()

			out, err := runSend(t, fake, "", "-o", format, pollCmdName, pollVoteVerb, pollRecipientFlag, aliceNumber,
				targetFlg, aliceACI+":"+targetTS, pollOptionFlag, "0")
			if err != nil {
				t.Fatal(err)
			}

			golden(t, "poll_direct_vote_"+format, out)

			out, err = runSend(t, fake, "", "-o", format, pollCmdName, pollVoteVerb, pollRecipientFlag, aliceNumber,
				targetFlg, aliceACI+":"+targetTS, directPollClearFlag)
			if err != nil {
				t.Fatal(err)
			}

			golden(t, "poll_direct_vote_clear_"+format, out)
		})
	}
}
