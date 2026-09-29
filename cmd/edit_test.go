package cmd_test

import (
	"errors"
	"strconv"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
)

const editFlag = "--edit"

func TestSendEdit(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := sendFake()

			out, err := runSend(t, fake, "corrected\n", "-o", format, sendCmd, aliceNumber,
				app.SelfRecipient, "--group", groupID, editFlag, strconv.FormatUint(sentAt-1000, 10), "--stdin")
			if err != nil {
				t.Fatal(err)
			}

			golden(t, "edit_"+format, out)

			sent := fake.Sent()
			if len(sent) != 2 {
				t.Fatalf("sent = %+v", sent)
			}

			for _, req := range sent {
				if req.EditTarget != sentAt-1000 || req.Body != "corrected" || req.Timestamp != sentAt {
					t.Errorf("edit request = %+v", req)
				}
			}
		})
	}
}

func TestSendEditInvalidTimestamp(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"0", "-1", "abc", "18446744073709551616"} {
		fake := sendFake()

		_, err := runSend(t, fake, "", sendCmd, aliceNumber, editFlag, target, "-m", "corrected")
		if err == nil || len(fake.Sent()) != 0 || len(fake.Opened()) != 0 {
			t.Errorf("target %q: %v; client opened %d times", target, err, len(fake.Opened()))
		}
	}

	fake := sendFake()

	_, err := runSend(t, fake, "", sendCmd, aliceNumber, editFlag, strconv.FormatUint(sentAt, 10), "-m", "corrected")
	if !errors.Is(err, app.ErrInvalidEdit) || len(fake.Connects()) != 0 {
		t.Errorf("future edit: %v", err)
	}
}
