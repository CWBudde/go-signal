package signal_test

import (
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

const styleTestBody = "abcde"

func TestCheckTextStyles(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		body  string
		style signal.TextStyle
	}{
		{"unsupported", styleTestBody, signal.TextStyle{Length: 1, Style: "rainbow"}},
		{"empty range", styleTestBody, signal.TextStyle{Style: signal.StyleBold}},
		{"outside body", styleTestBody, signal.TextStyle{Start: 4, Length: 2, Style: signal.StyleBold}},
		{"wrapped end", styleTestBody, signal.TextStyle{Start: 4294967295, Length: 2, Style: signal.StyleBold}},
		{"surrogate start", "😀", signal.TextStyle{Start: 1, Length: 1, Style: signal.StyleBold}},
		{"surrogate end", "😀", signal.TextStyle{Length: 1, Style: signal.StyleBold}},
		{"malformed body", "\xff", signal.TextStyle{Length: 1, Style: signal.StyleBold}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			req := signal.SendRequest{GroupID: "test-group", Body: test.body, Styles: []signal.TextStyle{test.style}}

			err := req.Check()
			if !errors.Is(err, signal.ErrInvalidStyle) {
				t.Errorf("Check = %v, want ErrInvalidStyle", err)
			}
		})
	}
}
