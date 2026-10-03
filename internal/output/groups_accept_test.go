package output_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
)

func TestPrinterGroupAccept(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		heading string
		changed bool
	}{
		{"Invitation accepted", true},
		{"Already a member", false},
	} {
		t.Run(test.heading, func(t *testing.T) {
			t.Parallel()

			result := signal.GroupAcceptResult{
				ID: "id", Title: "Title\n\t\"quoted\"\x1b", Revision: 8,
				Changed: test.changed, Accepted: test.changed, Verified: true,
			}

			var plain, encoded bytes.Buffer

			err := output.New(&plain, output.Plain, time.UTC).GroupAccept(result)
			if err != nil {
				t.Fatal(err)
			}

			want := test.heading + "\nID: id\nTitle: \"Title\\n\\t\\\"quoted\\\"\\x1b\"\nRevision: 8\n"
			if plain.String() != want {
				t.Fatalf("plain = %q, want %q", plain.String(), want)
			}

			err = output.New(&encoded, output.JSON, time.UTC).GroupAccept(result)
			if err != nil {
				t.Fatal(err)
			}

			var doc map[string]any

			err = json.Unmarshal(encoded.Bytes(), &doc)
			if err != nil {
				t.Fatal(err)
			}

			wantDoc := map[string]any{"version": float64(1), "groupAccept": map[string]any{
				"id": "id", "title": result.Title, "revision": float64(8),
				"changed": test.changed, "accepted": test.changed, "verified": true,
			}}
			if !reflect.DeepEqual(doc, wantDoc) {
				t.Fatalf("JSON = %s", encoded.String())
			}
		})
	}
}
