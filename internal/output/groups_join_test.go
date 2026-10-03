package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
)

func TestGroupJoinOutput(t *testing.T) { //nolint:cyclop,funlen // exact additive schema and escaped plain fields
	t.Parallel()

	for _, test := range []struct {
		name    string
		status  signal.GroupJoinStatus
		changed bool
	}{
		{"Joined group", signal.GroupJoinMember, true},
		{"Join requested", signal.GroupJoinRequesting, true},
		{"Already a member", signal.GroupJoinMember, false},
		{"Already requested", signal.GroupJoinRequesting, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			result := signal.GroupJoinResult{
				ID: "id", Title: "Title\n\t\"quoted\"\x1b", Revision: 8, Status: test.status,
				Changed: test.changed, Accepted: test.changed, Verified: true,
			}

			var plain, encoded bytes.Buffer

			err := output.New(&plain, output.Plain, time.UTC).GroupJoin(result)
			if err != nil {
				t.Fatal(err)
			}

			want := test.name + "\nID: id\nTitle: \"Title\\n\\t\\\"quoted\\\"\\x1b\"\nRevision: 8\n"
			if plain.String() != want {
				t.Fatalf("plain = %q, want %q", plain.String(), want)
			}

			err = output.New(&encoded, output.JSON, time.UTC).GroupJoin(result)
			if err != nil {
				t.Fatal(err)
			}

			var doc struct {
				Version int            `json:"version"`
				Join    map[string]any `json:"groupJoin"`
			}

			err = json.Unmarshal(encoded.Bytes(), &doc)
			if err != nil {
				t.Fatal(err)
			}

			if doc.Version != 1 || len(doc.Join) != 7 || doc.Join["id"] != "id" ||
				doc.Join["title"] != result.Title || doc.Join["revision"] != float64(8) ||
				doc.Join["status"] != string(test.status) || doc.Join["changed"] != test.changed ||
				doc.Join["accepted"] != test.changed || doc.Join["verified"] != true {
				t.Fatalf("JSON = %s", encoded.String())
			}

			for _, secret := range []string{"password", "masterKey", "credential", "link"} {
				if strings.Contains(encoded.String(), secret) {
					t.Fatalf("secret field: %s", encoded.String())
				}
			}
		})
	}
}
