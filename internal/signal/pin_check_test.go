//nolint:cyclop,goconst,lll // complete payload fixtures and discriminating assertions
package signal_test

import (
	"errors"
	"math"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestPinPayloadCheck(t *testing.T) {
	t.Parallel()

	valid := signal.OutgoingPin{TargetAuthor: signal.Recipient{ACI: selfACI}, TargetTimestamp: 123, DurationSeconds: 86400}

	for _, test := range []struct {
		name   string
		change func(*signal.OutgoingPin)
		bad    bool
	}{
		{"finite", func(*signal.OutgoingPin) {}, false},
		{"forever", func(p *signal.OutgoingPin) { p.DurationSeconds = 0; p.Forever = true }, false},
		{"maximum seconds", func(p *signal.OutgoingPin) { p.DurationSeconds = math.MaxUint32 }, false},
		{"missing duration", func(p *signal.OutgoingPin) { p.DurationSeconds = 0 }, true},
		{"both duration modes", func(p *signal.OutgoingPin) { p.Forever = true }, true},
		{"pin missing author", func(p *signal.OutgoingPin) { p.TargetAuthor.ACI = "" }, true},
		{"bad author", func(p *signal.OutgoingPin) { p.TargetAuthor.ACI = "invalid-pin-ACI" }, true},
		{"pin nil author", func(p *signal.OutgoingPin) { p.TargetAuthor.ACI = nilACITestValue }, true},
		{"zero target", func(p *signal.OutgoingPin) { p.TargetTimestamp = 0 }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			p := valid
			test.change(&p)

			err := p.Check()
			if test.bad && !errors.Is(err, signal.ErrInvalidPin) {
				t.Fatalf("Check = %v, want ErrInvalidPin", err)
			}

			if !test.bad && err != nil {
				t.Fatal(err)
			}
		})
	}

	for _, test := range []struct {
		name      string
		author    string
		timestamp uint64
		bad       bool
	}{
		{"pin valid", valid.TargetAuthor.ACI, 123, false},
		{"zero target", valid.TargetAuthor.ACI, 0, true},
		{"pin nil author", nilACITestValue, 123, true},
		{"bad author", "invalid-pin-ACI", 123, true},
	} {
		t.Run("unpin "+test.name, func(t *testing.T) {
			t.Parallel()

			err := (signal.OutgoingUnpin{TargetAuthor: signal.Recipient{ACI: test.author}, TargetTimestamp: test.timestamp}).Check()
			if test.bad && !errors.Is(err, signal.ErrInvalidPin) {
				t.Fatalf("Check = %v, want ErrInvalidPin", err)
			}

			if !test.bad && err != nil {
				t.Fatal(err)
			}
		})
	}
}
