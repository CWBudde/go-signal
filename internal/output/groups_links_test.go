package output_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
)

const linkOutputURL = "https://signal.group/#fixture"

// These cases catch accidentally exposing a stored URL when the link is inactive.
func TestGroupLinkOutput(t *testing.T) {
	t.Parallel()

	for _, state := range []signal.GroupLinkState{
		signal.GroupLinkDisabled, signal.GroupLinkEnabled, signal.GroupLinkApproval, signal.GroupLinkUnknown,
	} {
		for _, format := range []output.Format{output.Plain, output.JSON} {
			t.Run(string(state)+"/"+string(format), func(t *testing.T) {
				t.Parallel()

				var out bytes.Buffer

				link := signal.GroupLink{ID: "group-id", Revision: 7, State: state, URL: linkOutputURL}

				err := output.New(&out, format, time.UTC).GroupLink(link)
				if err != nil {
					t.Fatal(err)
				}

				active := state == signal.GroupLinkEnabled || state == signal.GroupLinkApproval
				if strings.Contains(out.String(), linkOutputURL) != active {
					t.Fatalf("state %s: URL output %q", state, out.String())
				}

				if format == output.JSON {
					assertGroupLinkJSON(t, out.Bytes(), state, active)
				} else if !strings.Contains(out.String(), "group-id") || !strings.Contains(out.String(), string(state)) ||
					!strings.Contains(out.String(), "7") {
					t.Fatalf("plain output %q", out.String())
				}
			})
		}
	}
}

func assertGroupLinkJSON(t *testing.T, data []byte, state signal.GroupLinkState, active bool) {
	t.Helper()

	var doc struct {
		Version int            `json:"version"`
		Link    map[string]any `json:"groupLink"`
	}

	err := json.Unmarshal(data, &doc)
	if err != nil {
		t.Fatal(err)
	}

	_, hasURL := doc.Link["url"]
	if doc.Version != 1 || doc.Link["id"] != "group-id" || doc.Link["revision"] != float64(7) ||
		doc.Link["state"] != string(state) || hasURL != active {
		t.Fatalf("document %s", data)
	}
}

func TestGroupLinkPlainEscapesControls(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	err := output.New(&out, output.Plain, time.UTC).GroupLink(signal.GroupLink{
		ID: "group\n\x1b", Revision: 1, State: signal.GroupLinkEnabled, URL: "url\n\x1b",
	})
	if err != nil || strings.Count(out.String(), "\n") != 4 ||
		!strings.Contains(out.String(), `"group\n\x1b"`) || !strings.Contains(out.String(), `"url\n\x1b"`) {
		t.Fatalf("plain output %q, %v", out.String(), err)
	}
}

func TestGroupLinkWriterFailure(t *testing.T) {
	t.Parallel()

	for _, format := range []output.Format{output.Plain, output.JSON} {
		err := output.New(profileFailWriter{}, format, time.UTC).GroupLink(signal.GroupLink{State: signal.GroupLinkDisabled})
		if !errors.Is(err, errProfileWriter) {
			t.Fatalf("format %s: %v", format, err)
		}
	}
}
