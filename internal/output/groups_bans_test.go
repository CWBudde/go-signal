package output_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	bannedPNI    = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	banMasterKey = "ban-master-key"
)

func TestGroupBansJSON(t *testing.T) {
	t.Parallel()

	bannedAt := time.Date(2026, 10, 2, 20, 0, 0, 123000000, time.FixedZone("fixture", 3600))
	group := signal.Group{MasterKey: banMasterKey, Banned: []signal.BannedMember{
		{Recipient: signal.Recipient{ACI: aliceACI}, BannedAt: bannedAt},
		{Recipient: signal.Recipient{PNI: bannedPNI}},
	}}

	data, err := json.Marshal(output.NewGroupJSON(group, app.Names{}))
	if err != nil {
		t.Fatal(err)
	}

	var doc struct{ Banned []map[string]string }

	err = json.Unmarshal(data, &doc)
	if err != nil {
		t.Fatal(err)
	}

	want := []map[string]string{
		{"aci": aliceACI, "bannedAt": "2026-10-02T19:00:00.123Z"},
		{"pni": bannedPNI},
	}
	if !reflect.DeepEqual(doc.Banned, want) {
		t.Fatalf("banned = %s", data)
	}

	if strings.Contains(string(data), banMasterKey) {
		t.Fatal("master key exposed")
	}
}

func TestGroupBansInaccessibleJSON(t *testing.T) {
	t.Parallel()

	group := signal.Group{MasterKey: banMasterKey, Err: signal.ErrNotAMember, Banned: []signal.BannedMember{
		{Recipient: signal.Recipient{ACI: aliceACI}},
	}}

	data, err := json.Marshal(output.NewGroupJSON(group, app.Names{}))
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(data), `"banned"`) || strings.Contains(string(data), banMasterKey) {
		t.Fatalf("inaccessible group = %s", data)
	}
}

func TestGroupBansEmptyJSON(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(output.NewGroupJSON(signal.Group{}, app.Names{}))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(data), `"banned":[]`) {
		t.Fatalf("empty bans = %s", data)
	}
}

func TestGroupBansPlain(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	group := signal.Group{MasterKey: banMasterKey, Banned: []signal.BannedMember{
		{
			Recipient: signal.Recipient{ACI: aliceACI},
			BannedAt:  time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC),
		},
		{Recipient: signal.Recipient{PNI: bannedPNI}},
	}}

	err := output.New(&out, output.Plain, time.UTC).Group(group)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"Banned (2):", aliceACI, "2026-10-02 20:00:00", bannedPNI} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, out.String())
		}
	}

	if strings.Contains(out.String(), banMasterKey) {
		t.Fatal("master key exposed")
	}
}
