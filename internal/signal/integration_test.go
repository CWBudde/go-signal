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
//	GOSIGNAL_IT_LINK      "1" to also link a new device by QR code, check its devices and sync, and
//	                      unlink it again; with GOSIGNAL_IT_DATA_DIR on the same account, the main
//	                      client also sends it an image that it must download byte for byte
//	GOSIGNAL_IT_MESSAGING "1" to send an image, a quoting reply with a mention, a reaction and a
//	                      remote delete to the peer (and the test group), and to block and unblock
//	                      the peer; check the phones for how they show up
//	GOSIGNAL_IT_REMOTE_UNLINK "1" to link a temporary device and wait for its removal on the phone
//	GOSIGNAL_IT_KEEP_UNLINKED "1" to keep the remotely unlinked data dir for CLI tests (its path is
//	                      logged as GOSIGNAL_IT_UNLINKED_DIR=<dir>; delete it yourself)
//	GOSIGNAL_IT_LEAVE_GROUP group ID or master key of a disposable group (created on the phone,
//	                      not the test group) to leave
//	GOSIGNAL_IT_TIMEOUT   how long to wait for delivery receipts (default 2m)
//	GOSIGNAL_IT_LOG       log level of the client's logs (default warn)

package signal_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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

// resolvePeer looks the peer up (in the store, else by contact discovery) for the tests that
// don't run stepCDSI.
func (env *liveEnv) resolvePeer(t *testing.T) {
	t.Helper()

	peers, err := env.client.Resolve(t.Context(), []signal.Recipient{{Number: env.peerNumber}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	env.peer = peers[0]
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
// test account's phone), checks its device list, that it can connect, send and sync, and
// unlinks it again.
func TestIntegrationLink(t *testing.T) { //nolint:paralleltest // needs the phone
	if os.Getenv("GOSIGNAL_IT_LINK") != "1" {
		t.Skip("GOSIGNAL_IT_LINK not set to 1")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	dataDir := t.TempDir()
	name := "go-signal integration " + backend()

	client, acc := linkTemporary(ctx, t, dataDir, name)

	defer cleanupLinkedDevice(t, client, dataDir)

	// Receiving (not SendOnly) so that the attachment check sees the main client's transcript.
	err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	events := collect(client.Events())

	t.Run("Devices", func(t *testing.T) { //nolint:paralleltest // one live device
		checkLinkedDevices(t, client, acc, name)
	})
	t.Run("Send", func(t *testing.T) { //nolint:paralleltest // one live device
		sendAndCheck(t, client, signal.SendRequest{
			Recipients: []signal.Recipient{{ACI: acc.ACI}},
			Body:       body("freshly linked"),
		})
	})
	t.Run("Sync", func(t *testing.T) { //nolint:paralleltest // one live device
		checkLinkedSync(t, client)
	})
	t.Run("Attachment", func(t *testing.T) { //nolint:paralleltest // one live device
		checkLinkedAttachment(t, client, events, acc)
	})
}

// linkTemporary opens dataDir and links a new device named name into it, showing the QR code
// on stderr.
func linkTemporary(ctx context.Context, t *testing.T, dataDir, name string) (signal.Client, signal.Account) {
	t.Helper()

	client, err := signal.Open(ctx, signal.Options{DataDir: dataDir, Logger: testLogger(t)})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	acc, err := client.Link(ctx, name, func(uri string) {
		fmt.Fprintln(os.Stderr, "Scan with Signal on the phone (Settings > Linked devices):")
		qrterminal.GenerateHalfBlock(uri, qrterminal.L, os.Stderr)
	})
	if err != nil {
		_ = client.Close()

		t.Fatalf("Link: %v", err)
	}

	t.Logf("linked %s (%s) as device %d %q", acc.Number, acc.ACI, acc.DeviceID, name)

	return client, acc
}

// primaryDeviceID is the phone's device ID; it usually has no name.
const primaryDeviceID = 1

func checkLinkedDevices(t *testing.T, client signal.Client, acc signal.Account, name string) {
	t.Helper()

	devices, err := client.Devices(t.Context())
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}

	current := false

	for _, dev := range devices {
		t.Logf("device %d %q, created %s, last seen %s, current %t",
			dev.ID, dev.Name, dev.Created, dev.LastSeen.Format(time.DateOnly), dev.Current)

		if dev.Created.IsZero() {
			t.Errorf("device %d: creation time missing or not decryptable", dev.ID)
		}

		// An undecryptable name comes back as its raw bytes.
		if dev.ID != primaryDeviceID && (dev.Name == "" || !utf8.ValidString(dev.Name)) {
			t.Errorf("device %d: name %q not decrypted", dev.ID, dev.Name)
		}

		if dev.Current {
			current = dev.ID == acc.DeviceID && dev.Name == name
		}
	}

	if !current {
		t.Errorf("Devices = %+v, want device %d %q as current", devices, acc.DeviceID, name)
	}
}

// checkLinkedSync syncs the fresh device and looks for the peer and the test group in what
// arrived.
func checkLinkedSync(t *testing.T, client signal.Client) {
	t.Helper()

	result, err := client.Sync(t.Context(), signal.SyncOptions{
		Progress: func(stage signal.SyncStage) { t.Logf("sync: %s", stage) },
	})
	if err != nil {
		t.Errorf("Sync: %v (missing %v)", err, result.Missing())
	}

	t.Logf("synced %d contacts, %d groups", result.Contacts, result.Groups)

	if number := os.Getenv("GOSIGNAL_IT_PEER"); number != "" {
		checkSyncedContact(t, client, number)
	}

	if ref := os.Getenv("GOSIGNAL_IT_GROUP"); ref != "" {
		checkSyncedGroup(t, client, ref)
	}
}

func checkSyncedContact(t *testing.T, client signal.Client, number string) {
	t.Helper()

	// Resolve finds the synced number in the store without contact discovery.
	peers, err := client.Resolve(t.Context(), []signal.Recipient{{Number: number}})
	if err != nil {
		t.Errorf("Resolve: %v", err)

		return
	}

	contacts, err := client.Contacts(t.Context())
	if err != nil {
		t.Errorf("Contacts: %v", err)

		return
	}

	if !slices.ContainsFunc(contacts, func(c signal.Contact) bool { return c.ACI == peers[0].ACI || c.Number == number }) {
		t.Errorf("peer %s (%s) not among the %d synced contacts", number, peers[0].ACI, len(contacts))
	}
}

func checkSyncedGroup(t *testing.T, client signal.Client, ref string) {
	t.Helper()

	// Group fails with ErrUnknownGroup unless the sync stored the master key.
	group, err := client.Group(t.Context(), ref)
	if err != nil {
		t.Errorf("Group: %v", err)

		return
	}

	groups, err := client.Groups(t.Context())
	if err != nil {
		t.Errorf("Groups: %v", err)

		return
	}

	if !slices.ContainsFunc(groups, func(g signal.Group) bool { return g.ID == group.ID && g.Err == nil }) {
		t.Errorf("test group %s not among the %d listed groups", group.ID, len(groups))
	}

	titles, err := client.GroupTitles(t.Context())
	if err != nil || titles[group.ID].Title != group.Title {
		t.Errorf("cached title = %q, %v; want %q", titles[group.ID].Title, err, group.Title)
	}
}

// checkLinkedAttachment sends an image as a note-to-self from the main test account
// (GOSIGNAL_IT_DATA_DIR) and checks that the fresh device downloads the same bytes from the
// sync transcript. The two data dirs have their own locks, so both clients can be connected.
func checkLinkedAttachment(t *testing.T, linked signal.Client, events *eventLog, acc signal.Account) {
	t.Helper()

	dataDir := os.Getenv("GOSIGNAL_IT_DATA_DIR")
	if dataDir == "" {
		t.Skip("GOSIGNAL_IT_DATA_DIR not set")
	}

	mainClient := openMainSender(t, dataDir, acc.ACI)
	data, width, height := testPNG(t)

	uploaded, err := mainClient.Upload(t.Context(), []signal.OutgoingAttachment{{
		Data: data, ContentType: pngType, Filename: "go-signal-link.png", Width: width, Height: height,
	}})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	sent := sendAndCheck(t, mainClient, signal.SendRequest{
		Recipients:  []signal.Recipient{{ACI: acc.ACI}},
		Body:        body("attachment for the freshly linked device"),
		Attachments: uploaded,
	})

	var transcript *signal.Message

	waitFor(t, events, envDuration(t, "GOSIGNAL_IT_TIMEOUT", defaultReceiptTimeout),
		fmt.Sprintf("sync transcript %d with an attachment", sent.Timestamp), func(evt signal.Event) bool {
			msg, ok := evt.(*signal.Message)
			if ok && msg.Sync && msg.Timestamp == sent.Timestamp && len(msg.Attachments) == 1 {
				transcript = msg
			}

			return transcript != nil
		})

	got, err := linked.Download(t.Context(), transcript.Attachments[0])
	if err != nil {
		t.Fatalf("Download: %v", err)
	}

	if !bytes.Equal(got, data) || transcript.Attachments[0].ContentType != pngType {
		t.Errorf("downloaded %d bytes of %s, want the %d bytes of image/png sent",
			len(got), transcript.Attachments[0].ContentType, len(data))
	}
}

// openMainSender opens the main test account, send-only, and skips unless it is the account
// with the ACI aci.
func openMainSender(t *testing.T, dataDir, aci string) signal.Client {
	t.Helper()

	main, err := signal.Open(t.Context(), signal.Options{
		DataDir: dataDir,
		Account: os.Getenv("GOSIGNAL_IT_ACCOUNT"),
		Logger:  testLogger(t),
	})
	if err != nil {
		t.Fatalf("Open %s: %v", dataDir, err)
	}

	t.Cleanup(func() {
		err := main.Close()
		if err != nil {
			t.Errorf("Close main client: %v", err)
		}
	})

	mainAcc, err := main.Account(t.Context())
	if err != nil {
		t.Fatalf("Account: %v", err)
	}

	if mainAcc.ACI != aci {
		t.Skipf("GOSIGNAL_IT_DATA_DIR holds %s, not the freshly linked account %s", mainAcc.ACI, aci)
	}

	err = main.Connect(t.Context(), signal.SendOnly())
	if err != nil {
		t.Fatalf("Connect main client: %v", err)
	}

	return main
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
		isDeliveryReceipt(sender, timestamp))
}

func isDeliveryReceipt(sender string, timestamp uint64) func(signal.Event) bool {
	return func(evt signal.Event) bool {
		r, ok := evt.(*signal.Receipt)

		return ok && r.Sender.ACI == sender && r.Type == signal.ReceiptDelivery &&
			slices.Contains(r.Timestamps, timestamp)
	}
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

	err := awaitEvent(events, timeout, match)
	if errors.Is(err, errEventsClosed) {
		t.Fatalf("events closed while waiting for %s", what)
	} else if err != nil {
		t.Fatalf("no %s within %s", what, timeout)
	}
}

var (
	errEventsClosed = errors.New("events closed")
	errNoEvent      = errors.New("no matching event")
)

// awaitEvent waits until events has one that matches, and fails with errNoEvent after timeout
// or with errEventsClosed when the client closed its events first.
func awaitEvent(events *eventLog, timeout time.Duration, match func(signal.Event) bool) error {
	deadline := time.After(timeout)

	for {
		events.mu.Lock()
		found := slices.ContainsFunc(events.events, match)
		changed := events.changed
		events.mu.Unlock()

		if found {
			return nil
		}

		select {
		case <-changed:
		case <-events.done:
			return errEventsClosed
		case <-deadline:
			return errNoEvent
		}
	}
}
