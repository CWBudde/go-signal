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

// Omitting the conversion or printing stale paths for inaccessible groups breaks these cases.
func TestGroupAvatarJSON(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		group signal.Group
		want  string
	}{
		{"set", signal.Group{AvatarPath: "groups/avatar-123", MasterKey: "hidden-key"}, "groups/avatar-123"},
		{"cleared", signal.Group{}, ""},
		{"unavailable", signal.Group{AvatarPath: "stale-path", Err: signal.ErrNotAMember}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			err := output.New(&out, output.JSON, time.UTC).Groups([]signal.Group{test.group})
			if err != nil {
				t.Fatal(err)
			}

			var doc struct {
				Version int                          `json:"version"`
				Groups  []map[string]json.RawMessage `json:"groups"`
			}

			err = json.Unmarshal(out.Bytes(), &doc)
			if err != nil {
				t.Fatal(err)
			}

			value, present := doc.Groups[0]["avatarPath"]
			if present != (test.want != "") || doc.Version != 1 {
				t.Fatalf("avatar presence or schema version: %s", out.String())
			}

			if present && string(value) != `"groups/avatar-123"` {
				t.Fatalf("avatar path = %s", value)
			}

			if strings.Contains(out.String(), "hidden-key") {
				t.Fatal("group output exposed master key")
			}
		})
	}
}

// A raw control in the path must not create terminal output or another line.
func TestGroupAvatarPlain(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		group signal.Group
		want  string
	}{
		{"set", signal.Group{AvatarPath: "groups/avatar\n\x1b"}, `"groups/avatar\n\x1b"`},
		{"cleared", signal.Group{}, ""},
		{"unavailable", signal.Group{AvatarPath: "stale-path", Err: signal.ErrNotAMember}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			err := output.New(&out, output.Plain, time.UTC).Group(test.group)
			if err != nil {
				t.Fatal(err)
			}

			found := false

			for line := range strings.SplitSeq(out.String(), "\n") {
				if strings.HasPrefix(line, "Avatar path:") {
					found = true

					if strings.TrimSpace(strings.TrimPrefix(line, "Avatar path:")) != test.want {
						t.Fatalf("avatar line %q, want %q", line, test.want)
					}
				}
			}

			if found != (test.want != "") || strings.ContainsRune(out.String(), '\x1b') {
				t.Fatalf("avatar output %q", out.String())
			}
		})
	}
}
