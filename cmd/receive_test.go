package cmd_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func testAccount() *signal.Account {
	return &signal.Account{
		Number:   "+15550100",
		ACI:      "11111111-1111-1111-1111-111111111111",
		PNI:      "22222222-2222-2222-2222-222222222222",
		DeviceID: 2,
	}
}

// run executes the root command against fake and returns its stdout. An empty config file keeps
// the user's config out without t.Setenv, so tests can run in parallel.
func run(t *testing.T, fake *signaltest.Fake, args ...string) (string, error) {
	t.Helper()

	cfgFile := filepath.Join(t.TempDir(), "config.yaml")

	err := os.WriteFile(cfgFile, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	root := cmd.NewRootCmd(cmd.WithClientFactory(fake.Factory), cmd.WithLocation(time.UTC))
	root.SetOut(&out)
	root.SetArgs(append([]string{"--config", cfgFile, "--data-dir", t.TempDir()}, args...))

	err = root.Execute()

	if !fake.AllClosed() {
		t.Error("client was not closed")
	}

	return out.String(), err
}

func TestLink(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{LinkAs: *testAccount()}

	out, err := run(t, fake, "link", "--name", "test-device")
	if err != nil {
		t.Fatalf("link: %v", err)
	}

	if !strings.HasPrefix(out, signaltest.LinkURI+"\n") {
		t.Errorf("output does not start with the link URI: %q", out)
	}

	want := "Linked +15550100 (ACI 11111111-1111-1111-1111-111111111111, device 2)\n"
	if !strings.HasSuffix(out, want) {
		t.Errorf("output does not end with %q: %q", want, out)
	}

	if len(fake.Linked) != 1 || fake.Linked[0] != *testAccount() {
		t.Errorf("account not stored: %+v", fake.Linked)
	}
}

func TestLinkError(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{LinkErr: signal.ErrNotImplemented}

	_, err := run(t, fake, "link")
	if !errors.Is(err, signal.ErrNotImplemented) {
		t.Fatalf("got %v, want the Link error", err)
	}
}

func TestReceiveStopsAtFirstMessage(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{
		Linked: []signal.Account{*testAccount()},
		Incoming: []signal.Event{
			&signal.Connection{State: signal.StateConnected},
			&signal.Message{Body: "first"},
			&signal.Message{Body: "second"},
		},
	}

	out, err := run(t, fake, "receive", "--timeout", "5s")
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	if !strings.Contains(out, "*signal.Connection") || !strings.Contains(out, "first") {
		t.Errorf("missing events in output: %q", out)
	}

	if strings.Contains(out, "second") {
		t.Errorf("receive did not stop after the first message: %q", out)
	}
}

func TestReceiveTimeout(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}}

	_, err := run(t, fake, "receive", "--timeout", "10ms")
	if err != nil {
		t.Fatalf("a timeout should not be an error: %v", err)
	}
}

func TestReceiveLoggedOut(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{
		Linked:   []signal.Account{*testAccount()},
		Incoming: []signal.Event{&signal.Connection{State: signal.StateLoggedOut}},
	}

	_, err := run(t, fake, "receive", "--timeout", "5s")
	if !errors.Is(err, signal.ErrDeviceUnlinked) {
		t.Fatalf("got %v, want ErrDeviceUnlinked", err)
	}
}

func TestReceiveAccountInUse(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}, InUse: true}

	_, err := run(t, fake, "receive", "--timeout", "5s")
	if !errors.Is(err, signal.ErrAccountInUse) {
		t.Fatalf("got %v, want ErrAccountInUse", err)
	}
}

func TestReceiveNotLinked(t *testing.T) {
	t.Parallel()

	_, err := run(t, &signaltest.Fake{}, "receive", "--timeout", "5s")
	if !errors.Is(err, signal.ErrNotLinked) {
		t.Fatalf("got %v, want ErrNotLinked", err)
	}
}

func TestReceivePassesGlobalFlags(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}}

	_, err := run(t, fake, "receive", "-a", "+15550199", "--timeout", "5s")
	if !errors.Is(err, signal.ErrAccountNotFound) {
		t.Fatalf("got %v, want ErrAccountNotFound", err)
	}

	opened := fake.Opened()
	if len(opened) != 1 || opened[0].Account != "+15550199" || opened[0].DataDir == "" {
		t.Errorf("unexpected client options: %+v", opened)
	}
}
