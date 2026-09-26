package mcp_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/mcp"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	doctorTool = "doctor"
	statusWarn = "warn"
)

var errNetwork = errors.New("network unreachable")

// checkOf returns the check with the name from a doctor result.
func checkOf(t *testing.T, doc output.DoctorJSON, name string) output.CheckJSON {
	t.Helper()

	for _, check := range doc.Checks {
		if check.Name == name {
			return check
		}
	}

	t.Fatalf("no %s check in %+v", name, doc.Checks)

	return output.CheckJSON{}
}

func TestDoctor(t *testing.T) {
	t.Parallel()

	allow, err := app.ParseAllowlist([]string{"self"})
	if err != nil {
		t.Fatal(err)
	}

	fake := &signaltest.Fake{Linked: []signal.Account{testAccount()}}
	session := connectWith(t, fake, mcp.Options{}, testClient{}, app.WithAllowlist(allow))

	var doc output.DoctorJSON

	txt := call(t, session, doctorTool, nil, &doc)

	if !doc.Healthy {
		t.Errorf("not healthy: %+v", doc.Checks)
	}

	names := make([]string, 0, len(doc.Checks))
	for _, check := range doc.Checks {
		names = append(names, check.Name+"="+check.Status)
	}

	want := "mcp=ok account=ok inbox=ok connection=ok download dir=ok policy=ok"
	if got := strings.Join(names, " "); got != want {
		t.Errorf("checks = %s, want %s", got, want)
	}

	if got := checkOf(t, doc, "mcp").Detail; !strings.HasPrefix(got, "go-signal "+testVersion+", up ") {
		t.Errorf("mcp detail = %q", got)
	}

	if got := checkOf(t, doc, "policy").Detail; got != "sends to 1 allowed recipient; no attachments" {
		t.Errorf("policy detail = %q", got)
	}

	if !strings.Contains(txt, "ok    connection") {
		t.Errorf("text output:\n%s", txt)
	}

	// Signal's server is only asked with checkServer.
	for _, check := range doc.Checks {
		if check.Name == "server" {
			t.Errorf("server checked without checkServer: %+v", check)
		}
	}
}

func TestDoctorCheckServer(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{testAccount()}, DevicesErr: errNetwork}
	session := connect(t, fake)

	var doc output.DoctorJSON

	call(t, session, doctorTool, map[string]any{"checkServer": true}, &doc)

	if doc.Healthy {
		t.Error("healthy although the server is unreachable")
	}

	server := checkOf(t, doc, "server")
	if server.Status != "fail" || !strings.Contains(server.Detail, errNetwork.Error()) || server.Hint == "" {
		t.Errorf("server check = %+v", server)
	}

	// An App without an allowlist sends to anyone.
	if got := checkOf(t, doc, "policy"); got.Status != statusWarn || got.Detail != "sends to anyone; no attachments" {
		t.Errorf("policy = %+v", got)
	}
}

func TestDoctorConnection(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{testAccount()}}
	session := connectWith(t, fake, mcp.Options{ReadOnly: true}, testClient{})

	tests := []struct {
		evt        signal.Event
		wantStatus string
		wantDetail string
	}{
		{&signal.Connection{State: signal.StateDisconnected, Err: errNetwork}, statusWarn, "disconnected since "},
		{&signal.Connection{State: signal.StateConnected}, "ok", "connected since "},
		{&signal.QueueEmpty{}, "ok", "connected since "},
	}

	for _, test := range tests {
		if !fake.Push(test.evt) {
			t.Fatalf("push %T: not delivered", test.evt)
		}

		// Push returns once the event is taken; the inbox records it right after.
		check := waitForCheck(t, session, "connection", func(check output.CheckJSON) bool {
			return check.Status == test.wantStatus && strings.HasPrefix(check.Detail, test.wantDetail)
		})

		if !strings.Contains(check.Detail, "; last event ") {
			t.Errorf("%T: detail %q has no last event", test.evt, check.Detail)
		}

		if test.wantStatus == statusWarn && (check.Hint == "" || !strings.Contains(check.Detail, errNetwork.Error())) {
			t.Errorf("%T: check %+v lacks the error or a hint", test.evt, check)
		}
	}

	var doc output.DoctorJSON

	call(t, session, doctorTool, nil, &doc)

	if got := checkOf(t, doc, "policy"); got.Status != "ok" || got.Detail != "read-only: no tool sends" {
		t.Errorf("policy = %+v", got)
	}
}

// waitForCheck calls the doctor tool until the check with the name satisfies ok.
func waitForCheck(
	t *testing.T, session *sdk.ClientSession, name string, ok func(output.CheckJSON) bool,
) output.CheckJSON {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for {
		var doc output.DoctorJSON

		call(t, session, doctorTool, nil, &doc)

		check := checkOf(t, doc, name)
		if ok(check) {
			return check
		}

		if time.Now().After(deadline) {
			t.Fatalf("%s check stays %+v", name, check)
		}

		time.Sleep(10 * time.Millisecond)
	}
}
