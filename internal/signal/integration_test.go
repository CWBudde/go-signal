//go:build integration && (cgo || libsignal_go)

// The integration suite runs against Signal's production servers with a dedicated test account.
// It is opt-in and never runs in CI; see docs/dev.md ("Integration tests") for the setup.
//
//	GOSIGNAL_IT_DATA_DIR  data dir with the linked test account (required; the suite skips without)
//	GOSIGNAL_IT_ACCOUNT   account in it (number or ACI); empty selects the first
//	GOSIGNAL_IT_PEER      number of a second Signal account whose phone is online (required)
//	GOSIGNAL_IT_GROUP     group ID or master key of a test group (optional)
//	GOSIGNAL_IT_CREATE_GROUP "1" to create a reusable two-member test group (one-time setup)
//	GOSIGNAL_IT_RENAME_GROUP "1" to rename the test group and restore its original title
//	GOSIGNAL_IT_EDIT "1" to send and edit fresh self/direct/group messages
//	GOSIGNAL_IT_LINK      "1" to also link a new device by QR code and unlink it again
//	GOSIGNAL_IT_TIMEOUT   how long to wait for delivery receipts (default 2m)
//	GOSIGNAL_IT_LOG       log level of the client's logs (default warn)

package signal_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/mdp/qrterminal/v3"
)

const defaultReceiptTimeout = 2 * time.Minute

var errSendFailed = errors.New("send failed")

// TestIntegration runs its steps in order on one connection of the test account.
func TestIntegration(t *testing.T) { //nolint:paralleltest // one account, one connection, steps in order
	env := connectLive(t)

	steps := []struct {
		name string
		run  func(*testing.T)
	}{
		// Receiving the queued messages first proves that decryption works, and makes sure the
		// receipts we wait for below aren't behind a long queue.
		{"Receive", env.stepReceive},
		{"CDSI", env.stepCDSI}, // resolves the peer for the steps below
		{"Profile", env.stepProfile},
		{"NoteToSelf", env.stepNoteToSelf},
		{"Direct", env.stepDirect},
		{"Group", env.stepGroup},
	}

	for _, step := range steps {
		if !t.Run(step.name, step.run) && env.peer.ACI == "" && step.name == "CDSI" {
			t.Fatal("peer not resolved")
		}
	}
}

// liveEnv is the connected test account of TestIntegration.
type liveEnv struct {
	client         signal.Client
	acc            signal.Account
	events         *eventLog
	peerNumber     string
	peer           signal.Recipient // set by stepCDSI
	receiptTimeout time.Duration
}

func connectLive(t *testing.T) *liveEnv {
	t.Helper()

	dataDir := os.Getenv("GOSIGNAL_IT_DATA_DIR")
	if dataDir == "" {
		t.Skip("GOSIGNAL_IT_DATA_DIR not set")
	}

	env := &liveEnv{
		peerNumber:     os.Getenv("GOSIGNAL_IT_PEER"),
		receiptTimeout: envDuration(t, "GOSIGNAL_IT_TIMEOUT", defaultReceiptTimeout),
	}
	if env.peerNumber == "" {
		t.Fatal("GOSIGNAL_IT_PEER not set")
	}

	started := uint64(time.Now().UnixMilli()) //nolint:gosec // positive
	ctx := t.Context()

	client, err := signal.Open(ctx, signal.Options{
		DataDir: dataDir,
		Account: os.Getenv("GOSIGNAL_IT_ACCOUNT"),
		Logger:  testLogger(t),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	env.client = client

	env.acc, err = client.Account(ctx)
	if err != nil {
		t.Fatalf("Account: %v", err)
	}

	t.Logf("backend %s, account %s (%s), device %d", backend(), env.acc.Number, env.acc.ACI, env.acc.DeviceID)

	err = client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	env.events = collect(client.Events())

	t.Cleanup(func() {
		err := client.Close()
		if err != nil {
			t.Errorf("Close: %v", err)
		}

		env.events.wait()

		for _, evt := range env.events.all() {
			failure, ok := evt.(*signal.DecryptionFailure)
			if ok && failure.Timestamp >= started {
				t.Errorf("decryption failure from %s at %d: %v", failure.Sender, failure.Timestamp, failure.Err)
			}
		}
	})

	return env
}

func (env *liveEnv) stepReceive(t *testing.T) { //nolint:thelper // a step of TestIntegration
	waitFor(t, env.events, env.receiptTimeout, "queue empty", func(evt signal.Event) bool {
		_, ok := evt.(*signal.QueueEmpty)

		return ok
	})
}

func (env *liveEnv) stepCDSI(t *testing.T) { //nolint:thelper // a step of TestIntegration
	aci, pni, err := signal.LookupPhone(t.Context(), env.client, env.peerNumber)
	if err != nil {
		t.Fatalf("LookupPhone: %v", err)
	}

	// CDSI only returns the ACI of users whose access key we can prove, and signalmeow sends no
	// ACI/access key pairs; the PNI shows that the lookup went through the enclave.
	if aci == "" && pni == "" {
		t.Fatalf("LookupPhone(%s) found nothing; is the number discoverable?", env.peerNumber)
	}

	t.Logf("%s: ACI %q, PNI %q", env.peerNumber, aci, pni)

	resolved, err := env.client.Resolve(t.Context(), []signal.Recipient{{Number: env.peerNumber}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if aci != "" && resolved[0].ACI != aci {
		t.Errorf("Resolve ACI = %s, CDSI said %s", resolved[0].ACI, aci)
	}

	if pni != "" && resolved[0].PNI != "" && resolved[0].PNI != pni {
		t.Errorf("Resolve PNI = %s, CDSI said %s", resolved[0].PNI, pni)
	}

	env.peer = resolved[0]
}

func (env *liveEnv) stepProfile(t *testing.T) { //nolint:thelper // a step of TestIntegration
	t.Run("Own", func(t *testing.T) {
		name, err := signal.FetchProfile(t.Context(), env.client, env.acc.ACI)
		if err != nil {
			t.Fatalf("FetchProfile: %v", err)
		}

		t.Logf("own profile name %q", name)
	})

	t.Run("Peer", func(t *testing.T) {
		name, err := signal.FetchProfile(t.Context(), env.client, env.peer.ACI)
		if err != nil {
			// Without the peer's profile key (no message from them yet) there is nothing
			// to fetch; that says nothing about the backend.
			t.Skipf("FetchProfile: %v (send a message from the peer to share its profile key)", err)
		}

		t.Logf("peer profile name %q", name)
	})
}

func (env *liveEnv) stepNoteToSelf(t *testing.T) { //nolint:thelper // a step of TestIntegration
	sendAndCheck(t, env.client, signal.SendRequest{
		Recipients: []signal.Recipient{{ACI: env.acc.ACI}},
		Body:       body("note to self"),
	})
}

func (env *liveEnv) stepDirect(t *testing.T) { //nolint:thelper // a step of TestIntegration
	result := sendAndCheck(t, env.client, signal.SendRequest{
		Recipients: []signal.Recipient{env.peer},
		Body:       body("1:1"),
	})

	waitForReceipt(t, env.events, env.receiptTimeout, env.peer.ACI, result.Timestamp)
}

func (env *liveEnv) stepGroup(t *testing.T) { //nolint:thelper // a step of TestIntegration
	ref := os.Getenv("GOSIGNAL_IT_GROUP")
	if ref == "" {
		t.Skip("GOSIGNAL_IT_GROUP not set")
	}

	group, err := env.client.Group(t.Context(), ref)
	if err != nil {
		t.Fatalf("Group: %v", err)
	}

	t.Logf("group %q, revision %d, %d members", group.Title, group.Revision, len(group.Members))

	if !slices.ContainsFunc(group.Members, func(m signal.GroupMember) bool { return m.Recipient.ACI == env.peer.ACI }) {
		t.Fatal("peer must be a full member of the test group to verify delivery")
	}

	result := sendAndCheck(t, env.client, signal.SendRequest{GroupID: group.ID, Body: body("group")})

	waitForReceipt(t, env.events, env.receiptTimeout, env.peer.ACI, result.Timestamp)
}

// TestIntegrationLink links a new device into a temporary data dir (scan the QR code with the
// test account's phone), checks that it can connect and send, and unlinks it again.
func TestIntegrationLink(t *testing.T) { //nolint:paralleltest // needs the phone
	if os.Getenv("GOSIGNAL_IT_LINK") != "1" {
		t.Skip("GOSIGNAL_IT_LINK not set to 1")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	dataDir := t.TempDir()

	client, err := signal.Open(ctx, signal.Options{DataDir: dataDir, Logger: testLogger(t)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	acc, err := client.Link(ctx, "go-signal integration "+backend(), func(uri string) {
		fmt.Fprintln(os.Stderr, "Scan with Signal on the phone (Settings > Linked devices):")
		qrterminal.GenerateHalfBlock(uri, qrterminal.L, os.Stderr)
	})
	if err != nil {
		_ = client.Close()

		t.Fatalf("Link: %v", err)
	}

	t.Logf("linked %s (%s) as device %d", acc.Number, acc.ACI, acc.DeviceID)

	defer cleanupLinkedDevice(t, client, dataDir)

	err = client.Connect(ctx, signal.SendOnly())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	devices, err := client.Devices(ctx)
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}

	if !slices.ContainsFunc(devices, func(d signal.Device) bool { return d.ID == acc.DeviceID && d.Current }) {
		t.Errorf("Devices = %+v, want device %d as current", devices, acc.DeviceID)
	}

	sendAndCheck(t, client, signal.SendRequest{
		Recipients: []signal.Recipient{{ACI: acc.ACI}},
		Body:       body("freshly linked"),
	})
}

func cleanupLinkedDevice(t *testing.T, client signal.Client, dataDir string) {
	t.Helper()

	// Unlink refuses a connected client; release the connection and reopen the temporary
	// account before removing this device from the server. Cleanup must outlive the test context.
	err := client.Close()
	if err != nil {
		t.Errorf("Close linked client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cleanup, err := signal.Open(ctx, signal.Options{DataDir: dataDir, Logger: testLogger(t)})
	if err != nil {
		t.Errorf("Open for unlink: %v", err)

		return
	}

	removed, err := cleanup.Unlink(ctx, signal.UnlinkOptions{})
	if err != nil {
		t.Errorf("Unlink: %v; remove the temporary integration device on the phone", err)
	} else {
		t.Logf("unlinked device %d", removed.DeviceID)
	}

	err = cleanup.Close()
	if err != nil {
		t.Errorf("Close unlink client: %v", err)
	}
}

func backend() string {
	if version := signal.LibsignalGoVersion(); version != "" {
		return signal.Backend + " " + version
	}

	return signal.Backend
}

func body(what string) string {
	return fmt.Sprintf("go-signal integration test (%s): %s, %s", backend(), what, time.Now().Format(time.RFC3339))
}

func sendAndCheck(t *testing.T, client signal.Client, req signal.SendRequest) signal.SendResult {
	t.Helper()

	result, err := client.Send(t.Context(), req)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if len(result.Results) == 0 {
		t.Fatalf("Send: no recipient results")
	}

	var errs []error

	for _, r := range result.Results {
		if r.Err != nil {
			errs = append(errs, fmt.Errorf("%w to %s: %w", errSendFailed, r.Recipient, r.Err))
		} else {
			t.Logf("sent %d to %s (sealed sender: %t)", result.Timestamp, r.Recipient, r.Unidentified)
		}
	}

	if len(errs) > 0 {
		t.Fatal(errors.Join(errs...))
	}

	return result
}

func waitForReceipt(t *testing.T, events *eventLog, timeout time.Duration, sender string, timestamp uint64) {
	t.Helper()

	waitFor(t, events, timeout, fmt.Sprintf("delivery receipt from %s for %d", sender, timestamp),
		func(evt signal.Event) bool {
			r, ok := evt.(*signal.Receipt)

			return ok && r.Sender.ACI == sender && r.Type == signal.ReceiptDelivery &&
				slices.Contains(r.Timestamps, timestamp)
		})
}

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()

	var level slog.Level

	err := level.UnmarshalText([]byte(os.Getenv("GOSIGNAL_IT_LOG")))
	if err != nil {
		level = slog.LevelWarn
	}

	return slog.New(slog.NewTextHandler(t.Output(), &slog.HandlerOptions{Level: level}))
}

func envDuration(t *testing.T, name string, def time.Duration) time.Duration {
	t.Helper()

	v := os.Getenv(name)
	if v == "" {
		return def
	}

	d, err := time.ParseDuration(v)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}

	return d
}

// eventLog reads a client's events until the channel closes, which acks them, and keeps them
// for the tests to look for.
type eventLog struct {
	mu      sync.Mutex
	events  []signal.Event
	changed chan struct{} // closed and replaced on every new event
	done    chan struct{}
}

func collect(events <-chan signal.Event) *eventLog {
	log := &eventLog{changed: make(chan struct{}), done: make(chan struct{})}

	go func() {
		defer close(log.done)

		for evt := range events {
			log.mu.Lock()
			log.events = append(log.events, evt)
			close(log.changed)
			log.changed = make(chan struct{})
			log.mu.Unlock()
		}
	}()

	return log
}

func (l *eventLog) all() []signal.Event {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Clone(l.events)
}

func (l *eventLog) wait() {
	<-l.done
}

func waitFor(t *testing.T, events *eventLog, timeout time.Duration, what string, match func(signal.Event) bool) {
	t.Helper()

	deadline := time.After(timeout)

	for {
		events.mu.Lock()
		found := slices.ContainsFunc(events.events, match)
		changed := events.changed
		events.mu.Unlock()

		if found {
			return
		}

		select {
		case <-changed:
		case <-events.done:
			t.Fatalf("events closed while waiting for %s", what)
		case <-deadline:
			t.Fatalf("no %s within %s", what, timeout)
		}
	}
}
