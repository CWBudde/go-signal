package output_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
)

func TestGroupTimer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		timer time.Duration
		want  string
	}{
		{0, "off"},
		{30 * time.Second, "30s"},
		{90 * time.Second, "90s"},
		{5 * time.Minute, "5m"},
		{8 * time.Hour, "8h"},
		{36 * time.Hour, "36h"},
		{24 * time.Hour, "1d"},
		{28 * 24 * time.Hour, "4w"},
	}

	for _, test := range tests {
		var out bytes.Buffer

		err := output.New(&out, output.Plain, time.UTC).Group(signal.Group{ID: "id", Timer: test.timer})
		if err != nil {
			t.Fatal(err)
		}

		if want := "Disappearing messages: " + test.want + "\n"; !strings.Contains(out.String(), want) {
			t.Errorf("timer %v: output lacks %q:\n%s", test.timer, want, out.String())
		}
	}
}
