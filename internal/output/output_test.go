package output_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
)

func TestParseFormat(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"plain", "json"} {
		format, err := output.ParseFormat(value)
		if err != nil || string(format) != value {
			t.Errorf("%q: got %q, %v", value, format, err)
		}
	}

	for _, value := range []string{"", "JSON", "yaml"} {
		_, err := output.ParseFormat(value)
		if !errors.Is(err, output.ErrInvalidFormat) {
			t.Errorf("%q: got %v, want ErrInvalidFormat", value, err)
		}
	}
}

func TestPlainUsesLocationJSONUsesUTC(t *testing.T) {
	t.Parallel()

	berlin := time.FixedZone("CEST", 2*60*60)
	acc := signal.Account{Number: "+15550100", LinkedAt: time.Date(2026, 9, 20, 12, 30, 0, 0, time.UTC)}

	var plain, jsonOut bytes.Buffer

	err := output.New(&plain, output.Plain, berlin).Account(acc)
	if err != nil {
		t.Fatal(err)
	}

	err = output.New(&jsonOut, output.JSON, berlin).Account(acc)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(plain.String(), "2026-09-20 14:30:00 CEST") {
		t.Errorf("plain output not in the given zone:\n%s", plain.String())
	}

	if !strings.Contains(jsonOut.String(), `"linkedAt":"2026-09-20T12:30:00Z"`) {
		t.Errorf("json output not in UTC: %s", jsonOut.String())
	}
}
