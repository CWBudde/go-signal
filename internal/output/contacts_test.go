package output_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
)

const ownACI = "11111111-1111-1111-1111-111111111111"

// namedPrinter renders with alice's name set to one with control characters.
func namedPrinter(format output.Format) (*output.Printer, *bytes.Buffer) {
	var out bytes.Buffer

	printer := output.New(&out, format, time.UTC)
	printer.SetNames(app.NewNames(ownACI, []signal.Contact{
		{Recipient: signal.Recipient{ACI: aliceACI}, ProfileName: "Alice\x1b[31m\nEvil"},
	}))

	return printer, &out
}

func TestEventNames(t *testing.T) {
	t.Parallel()

	msg := incoming("hi")
	msg.Quote = &signal.Quote{Author: signal.Recipient{ACI: ownACI}, Timestamp: sentAt}

	printer, out := namedPrinter(output.Plain)

	err := printer.Event(msg)
	if err != nil {
		t.Fatal(err)
	}

	want := `[2026-09-20 12:30:00 UTC] Alice\u001b[31m\nEvil → me: [quote me 2026-09-20 12:30:00 UTC] hi` + "\n"
	if out.String() != want {
		t.Errorf("got  %q\nwant %q", out.String(), want)
	}

	printer, out = namedPrinter(output.JSON)

	err = printer.Event(msg)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), `"sender":{"aci":"`+aliceACI+`","name":"Alice\u001b[31m\nEvil"}`) ||
		!strings.Contains(out.String(), `"author":{"aci":"`+ownACI+`"}`) {
		t.Errorf("JSON without the names: %s", out.String())
	}
}

func TestContactEscapesNames(t *testing.T) {
	t.Parallel()

	printer, out := namedPrinter(output.Plain)

	err := printer.Contacts([]signal.Contact{{Recipient: signal.Recipient{ACI: aliceACI}, Nickname: "A\tB\x07"}})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), `A\tB\u0007`) {
		t.Errorf("name not escaped:\n%s", out.String())
	}
}
