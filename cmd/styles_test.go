package cmd_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

func TestSendStyles(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := sendFake()

			out, err := runSend(t, fake, "😀 hello", "-o", format, sendCmd, aliceNumber,
				"--stdin", "--style", "3:5:BOLD", "--style", "3:2:italic")
			if err != nil {
				t.Fatal(err)
			}

			golden(t, "send_styled_"+format, out)

			want := []signal.TextStyle{
				{Start: 3, Length: 5, Style: signal.StyleBold},
				{Start: 3, Length: 2, Style: signal.StyleItalic},
			}

			sent := fake.Sent()
			if len(sent) != 1 || !reflect.DeepEqual(sent[0].Styles, want) {
				t.Errorf("sent %+v, want styles %+v", sent, want)
			}
		})
	}
}

func TestSendInvalidStyle(t *testing.T) {
	t.Parallel()

	fake := sendFake()

	_, err := runSend(t, fake, "", sendCmd, app.SelfRecipient, "-m", "😀", "--style", "1:1:bold")
	if !errors.Is(err, signal.ErrInvalidStyle) || len(fake.Connects()) != 0 || len(fake.Sent()) != 0 {
		t.Errorf("send = %v, connects %v, sent %+v", err, fake.Connects(), fake.Sent())
	}
}
