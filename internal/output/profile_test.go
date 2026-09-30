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

func renderProfile(t *testing.T, format output.Format, update bool, result signal.ProfileUpdateResult) string {
	t.Helper()

	var out bytes.Buffer

	printer := output.New(&out, format, time.UTC)

	var err error
	if update {
		err = printer.ProfileUpdate(result)
	} else {
		err = printer.Profile(result.Profile)
	}

	if err != nil {
		t.Fatal(err)
	}

	return out.String()
}

func TestProfileRenderingEscapesControls(t *testing.T) {
	t.Parallel()

	profile := signal.Profile{
		ACI: "aci\n", GivenName: "Alice\tMarie", FamilyName: "Smith\r",
		About: "line\n\x1b[31m\u202e", AboutEmoji: "\"\\", AvatarPath: "path\x00",
	}

	for _, update := range []bool{false, true} {
		out := renderProfile(t, output.Plain, update, signal.ProfileUpdateResult{
			Profile: profile, Changed: true, Accepted: true, Verified: true,
		})

		wantLines := 6
		if update {
			wantLines++
		}

		if strings.Count(out, "\n") != wantLines {
			t.Errorf("extra output lines: %q", out)
		}

		for _, want := range []string{
			`"aci\n"`, `"Alice\tMarie"`, `"Smith\r"`, `"line\n\x1b[31m\u202e"`, `"\"\\"`, `"path\x00"`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in %q", want, out)
			}
		}
	}
}

func TestProfileJSONEmptyTextFields(t *testing.T) {
	t.Parallel()

	for _, update := range []bool{false, true} {
		out := renderProfile(t, output.JSON, update, signal.ProfileUpdateResult{Verified: true})

		var doc struct {
			Version int               `json:"version"`
			Profile map[string]string `json:"profile"`
		}

		err := json.Unmarshal([]byte(out), &doc)
		if err != nil {
			t.Fatal(err)
		}

		if doc.Version != 1 || len(doc.Profile) != 5 {
			t.Errorf("version/profile = %d/%v", doc.Version, doc.Profile)
		}

		for _, field := range []string{"aci", "givenName", "familyName", "about", "aboutEmoji"} {
			value, ok := doc.Profile[field]
			if !ok || value != "" {
				t.Errorf("%s = %q, present %v", field, value, ok)
			}
		}
	}
}

func TestProfileJSONStatusFields(t *testing.T) {
	t.Parallel()

	for _, update := range []bool{false, true} {
		out := renderProfile(t, output.JSON, update, signal.ProfileUpdateResult{Verified: true})

		var doc map[string]json.RawMessage

		err := json.Unmarshal([]byte(out), &doc)
		if err != nil {
			t.Fatal(err)
		}

		for _, field := range []struct {
			name string
			want string
		}{
			{"changed", "false"}, {"accepted", "false"}, {"verified", "true"},
		} {
			value, present := doc[field.name]
			if present != update {
				t.Errorf("update %v: %s present = %v", update, field.name, present)
			}

			if update && string(value) != field.want {
				t.Errorf("%s = %s, want %s", field.name, value, field.want)
			}
		}
	}
}

func TestProfileJSONPublicShape(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(output.ProfileJSON{})
	if err != nil || string(encoded) != `{"aci":"","givenName":"","familyName":"","about":"","aboutEmoji":""}` {
		t.Errorf("empty public profile JSON = %s/%v", encoded, err)
	}
}

func TestProfileOutputWriterFailures(t *testing.T) {
	t.Parallel()

	for _, format := range []output.Format{output.Plain, output.JSON} {
		printer := output.New(profileFailWriter{}, format, time.UTC)

		err := printer.Profile(signal.Profile{})
		if !errors.Is(err, errProfileWriter) {
			t.Errorf("show writer error = %v", err)
		}

		err = printer.ProfileUpdate(signal.ProfileUpdateResult{Changed: true})
		if !errors.Is(err, errProfileWriter) {
			t.Errorf("update writer error = %v", err)
		}
	}
}

var errProfileWriter = errors.New("profile writer failed")

type profileFailWriter struct{}

func (profileFailWriter) Write([]byte) (int, error) { return 0, errProfileWriter }
