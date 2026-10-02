package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
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

func TestGroupPermissionsJSON(t *testing.T) { //nolint:cyclop // checks presence, values and secret exclusion
	t.Parallel()

	for _, test := range []struct {
		name  string
		group signal.Group
		known bool
	}{
		{"members allowed", signal.Group{MembersCanEditAttributes: true, MembersCanAddMembers: true}, true},
		{"admins only", signal.Group{}, true},
		{"inaccessible", signal.Group{Err: signal.ErrNotAMember}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			group := test.group
			group.MasterKey = "secret"

			encoded, err := json.Marshal(output.NewGroupJSON(group, app.Names{}))
			if err != nil {
				t.Fatal(err)
			}

			var fields map[string]json.RawMessage

			err = json.Unmarshal(encoded, &fields)
			if err != nil {
				t.Fatal(err)
			}

			for _, permission := range []struct {
				name string
				want bool
			}{
				{"membersCanEditAttributes", group.MembersCanEditAttributes},
				{"membersCanAddMembers", group.MembersCanAddMembers},
			} {
				value, present := fields[permission.name]
				if present != test.known {
					t.Errorf("%s present = %v, want %v: %s", permission.name, present, test.known, encoded)
				}

				if !present {
					continue
				}

				var allowed bool

				err = json.Unmarshal(value, &allowed)
				if err != nil || allowed != permission.want {
					t.Errorf("%s = %s, error %v, want %v", permission.name, value, err, permission.want)
				}
			}

			if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "masterKey") {
				t.Error("group JSON exposed its master key")
			}
		})
	}
}

func TestGroupPermissionsPlain(t *testing.T) {
	t.Parallel()

	for _, allowed := range []bool{false, true} {
		t.Run(map[bool]string{false: "admins", true: "members"}[allowed], func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			err := output.New(&out, output.Plain, time.UTC).Group(signal.Group{
				MembersCanEditAttributes: allowed, MembersCanAddMembers: allowed,
			})
			if err != nil {
				t.Fatal(err)
			}

			who := "only admins"
			if allowed {
				who = "all members"
			}

			for _, label := range []string{"Who can edit details:", "Who can add members:"} {
				found := false

				for line := range strings.SplitSeq(out.String(), "\n") {
					if strings.HasPrefix(line, label) && strings.TrimSpace(strings.TrimPrefix(line, label)) == who {
						found = true
					}
				}

				if !found {
					t.Errorf("permission line missing: %s", out.String())
				}
			}
		})
	}
}
