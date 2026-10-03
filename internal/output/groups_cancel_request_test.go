package output_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
)

// TestPrinterGroupCancelRequest catches unquoted titles, wrong headings or omitted JSON zero values.
func TestPrinterGroupCancelRequest(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		result signal.GroupCancelRequestResult
		plain  string
		json   string
	}{
		{
			name: "cancelled",
			result: signal.GroupCancelRequestResult{
				ID: "id", Title: "Request\n\t\"quoted\"\x1b", Revision: 8,
				Changed: true, Accepted: true, Verified: true,
			},
			plain: "Join request cancelled\nID: id\nTitle: \"Request\\n\\t\\\"quoted\\\"\\x1b\"\nRevision: 8\n",
			json: `{"version":1,"groupCancelRequest":{"id":"id","title":"Request\n\t\"quoted\"\u001b",` +
				`"revision":8,"changed":true,"accepted":true,"verified":true}}` + "\n",
		},
		{
			name:   "noop",
			result: signal.GroupCancelRequestResult{ID: "id", Verified: true},
			plain:  "No pending join request\nID: id\nTitle: \"\"\nRevision: 0\n",
			json: `{"version":1,"groupCancelRequest":{"id":"id","title":"","revision":0,` +
				`"changed":false,"accepted":false,"verified":true}}` + "\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			for _, format := range []output.Format{output.Plain, output.JSON} {
				var rendered bytes.Buffer

				err := output.New(&rendered, format, time.UTC).GroupCancelRequest(test.result)
				if err != nil {
					t.Fatal(err)
				}

				want := test.plain
				if format == output.JSON {
					want = test.json

					if !json.Valid(rendered.Bytes()) {
						t.Fatalf("invalid JSON: %s", rendered.String())
					}
				}

				if rendered.String() != want {
					t.Fatalf("%s output = %q; want %q", format, rendered.String(), want)
				}
			}
		})
	}
}
