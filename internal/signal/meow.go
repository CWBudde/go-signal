//go:build cgo || libsignal_go

package signal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/events"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

const (
	// ackFlushTimeout bounds the keepalive round trip that flushes pending acks on Close.
	ackFlushTimeout = 2 * time.Second
	// sendDrainTimeout bounds how long Close waits for in-flight sends before it disconnects.
	sendDrainTimeout = 5 * time.Second
	// linkQRRefresh replaces an unscanned code before the server drops its provisioning socket.
	linkQRRefresh = 45 * time.Second
	// keepalivePath is the Signal server's no-op websocket request.
	keepalivePath = "/v1/keepalive"
)

var (
	errUnexpectedState = errors.New("unexpected provisioning state")
	errProvisioning    = errors.New("provisioning ended without device data")
)

// Open opens the data dir opts.DataDir and returns a signalmeow-backed Client. The account
// database is opened on first use. It is the default Factory.
func Open(_ context.Context, opts Options) (Client, error) {
	log := opts.logger()

	dir, err := store.OpenDir(opts.DataDir, log)
	if err != nil {
		return nil, fmt.Errorf("open data dir: %w", err)
	}

	return &meowClient{
		opts: opts,
		log:  log,
		zlog: NewZerologBridge(log),
		dir:  dir,
		// Unbuffered: an event is only acked once the consumer has taken it.
		events:       make(chan Event),
		done:         make(chan struct{}),
		drainTimeout: sendDrainTimeout,
	}, nil
}

type meowClient struct {
	opts Options
	log  *slog.Logger
	zlog zerolog.Logger
	dir  *store.Dir

	// data is the open database of the account dataACI; lock is held once connected or linked.
	data    *store.Store
	dataACI string
	lock    *store.Lock

	// connDevice, ownACI and account belong to the connected account; they are set before the
	// receive loops start.
	connDevice *mstore.Device
	ownACI     string
	account    Account
	// trust decides which identity keys connDevice may send to (see installTrust).
	trust *identityTrust

	// cli runs the current receive loops; the supervisor replaces it on a restart.
	cliMu sync.Mutex
	cli   *signalmeow.Client

	// cancelLoops ends the receive loops' context and stopSupervisor the supervisor's, which
	// closes supervised on exit. They are set by Connect.
	cancelLoops    context.CancelFunc
	stopSupervisor context.CancelFunc
	supervised     chan struct{}

	events chan Event
	done   chan struct{} // closed by Close; unblocks pending emits

	// sendOnly is set by Connect before the loops start (see SendOnly). Then lost holds the
	// error of a connection lost for good, which Send reports.
	sendOnly bool
	lostMu   sync.Mutex
	lost     error

	// contactWaiters are told when the phone's contact list has been stored (see Sync).
	contactsMu     sync.Mutex
	contactWaiters []chan int

	// uploads are the attachments Upload put on the CDN, by UploadedAttachment.ID.
	uploadsMu sync.Mutex
	uploads   map[string]*signalpb.AttachmentPointer

	// profileMu serializes own-profile reads and updates with cancellable waiting.
	profileMu profileMutex

	// identitySyncMu serializes outgoing verification flushes with cancellable waiting.
	identitySyncMu profileMutex

	// overridesMu serializes changes to the block overrides (see SetBlocked).
	overridesMu sync.Mutex

	// mu guards closing, so that no handler or send starts once Close waits for them.
	mu        sync.Mutex
	closing   bool
	handling  sync.WaitGroup
	sending   sync.WaitGroup
	closeOnce sync.Once
	// drainTimeout is sendDrainTimeout (tests shorten it).
	drainTimeout time.Duration
	acked        atomic.Bool

	// timerPersistenceContext is a scoped offline-test synchronization hook; nil in production.
	timerPersistenceContext func(context.Context) context.Context
}

func (c *meowClient) Link(ctx context.Context, deviceName string, onURI func(string)) (Account, error) {
	ctx = c.zlog.WithContext(ctx)

	links := c.dir.NewLinkStore(c.zlog)
	defer func() {
		err := links.Close()
		if err != nil {
			c.log.Warn("close link store", "error", err)
		}
	}()

	for resp := range signalmeow.PerformProvisioningWithQRRefresh(ctx, links, deviceName, false, linkQRRefresh) {
		if resp.Err != nil {
			return Account{}, fmt.Errorf("provisioning: %w", resp.Err)
		}

		switch resp.State {
		case signalmeow.StateProvisioningURLReceived:
			onURI(resp.ProvisioningURL)
		case signalmeow.StateProvisioningDataReceived:
			return c.finishLink(links, resp.ProvisioningData, deviceName)
		case signalmeow.StateProvisioningError:
			return Account{}, fmt.Errorf("%w: %v", errUnexpectedState, resp.State)
		default:
			return Account{}, fmt.Errorf("%w: %v", errUnexpectedState, resp.State)
		}
	}

	return Account{}, errProvisioning
}

func (c *meowClient) Account(ctx context.Context) (Account, error) {
	// The device name and the link/unlink dates are only recorded in accounts.json.
	entry, err := c.selectAccount()
	if err != nil {
		return Account{}, err
	}

	device, err := c.device(ctx)
	if err != nil && (!errors.Is(err, ErrNotLinked) || !entry.Unlinked()) {
		return Account{}, err
	}

	// signalmeow may have cleared the credentials of a logged-out device.
	acc := entry
	if device != nil {
		acc = accountFromDevice(&device.DeviceData)
		acc.DeviceName, acc.LinkedAt, acc.UnlinkedAt = entry.DeviceName, entry.LinkedAt, entry.UnlinkedAt
	}

	stamp, known, err := c.data.Meta(ctx, lastSyncKey)
	if err != nil {
		return Account{}, fmt.Errorf("load last sync: %w", err)
	}

	if known {
		acc.LastSync, err = time.Parse(time.RFC3339, stamp)
		if err != nil {
			return Account{}, fmt.Errorf("parse last sync: %w", err)
		}

		acc.LastSync = acc.LastSync.UTC()
	}

	return acc, nil
}

func (c *meowClient) CheckLock(context.Context) error {
	if c.lock != nil {
		return nil
	}

	acc, err := c.selectAccount()
	if err != nil {
		return err
	}

	err = c.dir.Probe(acc.ACI)
	if err != nil {
		return fmt.Errorf("%w: %s", err, acc.Number)
	}

	return nil
}

func (c *meowClient) Connect(ctx context.Context, opts ...ConnectOption) error {
	if !c.begin(&c.sending) {
		return ErrClosed
	}
	defer c.sending.Done()

	if c.cancelLoops != nil {
		return ErrAlreadyConnected
	}

	ctx = c.zlog.WithContext(ctx)

	acc, err := c.selectAccount()
	if err != nil {
		return err
	}

	// Fail fast instead of letting the server reject the websocket again.
	if acc.Unlinked() {
		return UnlinkedError(acc)
	}

	device, err := c.device(ctx)
	if err != nil {
		return err
	}

	if c.lock == nil {
		c.lock, err = c.dir.Lock(c.dataACI)
		if err != nil {
			return fmt.Errorf("%w: %s", err, device.Number)
		}
	}

	c.connDevice = device
	c.trust = installTrust(device, c.data, c.log, time.Now)
	c.ownACI = device.ACI.String()
	c.account = acc
	c.sendOnly = NewConnectOptions(opts...).SendOnly

	// A storage sync may have undone a block or unblock made here since the last connection.
	c.settleOverrides(ctx, device.RecipientStore, nil)

	// The loops outlive ctx: cancelling the command must not cut the websockets before Close
	// has drained them.
	loopCtx, cancelLoops := context.WithCancel(context.WithoutCancel(ctx))
	supervisorCtx, sup := c.prepareSupervisor(loopCtx)

	statuses, err := c.startLoops(loopCtx)
	if err != nil {
		c.stopSupervisor()
		cancelLoops()

		return fmt.Errorf("connect: %w", err)
	}

	c.cancelLoops = cancelLoops
	c.supervise(supervisorCtx, statuses, sup)

	err = c.syncPendingIdentityVerification(ctx)
	if err != nil {
		c.log.Warn("identity verification sync remains pending; retry account sync or reconnect", "error", err)
	}

	return nil
}

func (c *meowClient) Events() <-chan Event {
	return c.events
}

// Close shuts down gracefully: it lets in-flight sends finish, stops handing out events (an
// event the consumer hasn't taken is not acked, so the server delivers it again next time),
// flushes the acks of delivered events, and only then closes the websockets and the database.
// Sends get drainTimeout before the websockets close under them; the database stays open
// until every operation that uses it (see begin) has returned.
func (c *meowClient) Close() error {
	var err error

	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closing = true
		c.mu.Unlock()

		if !waitTimeout(&c.sending, c.drainTimeout) {
			c.log.Warn("closing with operations still in flight", "timeout", c.drainTimeout)
		}

		close(c.done)
		c.handling.Wait()

		if c.cancelLoops != nil {
			// No restarts from here on.
			c.stopSupervisor()
			<-c.supervised

			c.flushAcks()

			stopErr := c.stopLoops()
			if stopErr != nil {
				c.log.Debug("close", "error", stopErr)
			}

			c.cancelLoops()
		}

		close(c.events)

		// Operations still running use the store; with the websockets closed, sends fail fast.
		c.sending.Wait()

		err = c.release()
	})

	return err
}

// protocolClient installs internal handlers before any protocol work can start.
func (c *meowClient) protocolClient(device *mstore.Device) *signalmeow.Client {
	cli := signalmeow.NewClient(device, c.zlog, c.handle)
	cli.ReceiveStories = true
	cli.StorageUpdateHandler = c.reconcileStorageIdentities

	return cli
}

// startLoops starts receive loops on a new signalmeow client. A fresh client is needed for a
// restart: signalmeow only reports a status that differs from the last one it reported.
func (c *meowClient) startLoops(ctx context.Context) (<-chan loopStatus, error) {
	cli := c.protocolClient(c.connDevice)

	raw, err := cli.StartReceiveLoops(ctx)
	if err != nil {
		return nil, fmt.Errorf("start receive loops: %w", err)
	}

	c.cliMu.Lock()
	c.cli = cli
	c.cliMu.Unlock()

	statuses := make(chan loopStatus)

	go func() {
		defer close(statuses)

		for status := range raw {
			select {
			case statuses <- convertLoopStatus(status):
			case <-ctx.Done():
				return
			}
		}
	}()

	return statuses, nil
}

// stopLoops stops the receive loops of the current signalmeow client, if any.
func (c *meowClient) stopLoops() error {
	c.cliMu.Lock()
	cli := c.cli
	c.cliMu.Unlock()

	if cli == nil {
		return nil
	}

	err := cli.StopReceiveLoops()
	if err != nil {
		return fmt.Errorf("stop receive loops: %w", err)
	}

	return nil
}

// prepareSupervisor establishes cancellation before receive workers can deliver logout.
func (c *meowClient) prepareSupervisor(loopCtx context.Context) (context.Context, *supervisor) {
	ctx, stop := context.WithCancel(loopCtx)
	c.stopSupervisor = stop
	c.supervised = make(chan struct{})

	sup := &supervisor{
		log: c.log,
		policy: ReconnectPolicy{
			MaxAttempts:    defaultMaxAttempts,
			InitialBackoff: defaultInitialBackoff,
			MaxBackoff:     defaultMaxBackoff,
		},
		start: func() (<-chan loopStatus, error) {
			return c.startLoops(loopCtx)
		},
		stop: c.stopLoops,
		emit: c.emit,
		loggedOut: func(cause error) error {
			return c.markUnlinked(c.account, cause)
		},
	}

	return ctx, sup
}

// supervise starts the prepared supervisor without replacing an early logout's cancellation.
func (c *meowClient) supervise(ctx context.Context, statuses <-chan loopStatus, sup *supervisor) {
	go func() {
		defer close(c.supervised)

		sup.run(ctx, statuses)
	}()
}

// begin registers an in-flight handler or send on group. It reports false once Close has
// started.
func (c *meowClient) begin(group *sync.WaitGroup) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closing {
		return false
	}

	group.Add(1)

	return true
}

// flushAcks waits until the acks of delivered events have been written, all within
// ackFlushTimeout. signalmeow keeps processing a request after our handler returned: it clears
// the event from its decryption buffer and only then queues the ack, followed by the delivery
// receipt (see web.SimpleResponse.AfterQueued). So flushAcks first waits for the request being
// handled to queue its ack; requests are handled one at a time, and a later one cannot hand
// out an event once Close has started (see handle). Requests share the websocket's single
// writer, so by the time the response to a keepalive request sent next arrives, those acks are
// on the wire. Finally it gives the delivery receipt the rest of the time, since stopLoops
// cancels it.
func (c *meowClient) flushAcks() {
	c.cliMu.Lock()
	cli := c.cli
	c.cliMu.Unlock()

	if !c.acked.Load() || cli == nil || !cli.IsConnected() {
		return
	}

	ctx, cancel := context.WithTimeout(c.zlog.WithContext(context.Background()), ackFlushTimeout)
	defer cancel()

	socket := cli.AuthedWS
	flushed := make(chan error, 1)

	go func() {
		err := socket.WaitResponseQueued(ctx)
		if err != nil {
			flushed <- fmt.Errorf("wait for the ack: %w", err)

			return
		}

		_, err = socket.SendRequest(ctx, http.MethodGet, keepalivePath, nil, nil)
		if err != nil {
			flushed <- fmt.Errorf("keepalive: %w", err)

			return
		}

		err = socket.WaitRequestDone(ctx)
		if err != nil {
			err = fmt.Errorf("wait for the delivery receipt: %w", err)
		}

		flushed <- err
	}()

	// Keep the acknowledgement-flush deadline explicit at the facade boundary.
	select {
	case err := <-flushed:
		if err != nil {
			c.log.Debug("flush acks", "error", err)
		}
	case <-ctx.Done():
		c.log.Debug("flush acks: timed out", "timeout", ackFlushTimeout)
	}
}

// waitTimeout waits for wg and reports false if that takes longer than timeout.
func waitTimeout(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})

	go func() {
		wg.Wait()
		close(done)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// release closes the account database and releases the lock, if held.
func (c *meowClient) release() error {
	var errs []error

	if c.data != nil {
		errs = append(errs, c.data.Close())
	}

	if c.lock != nil {
		errs = append(errs, c.lock.Unlock())
	}

	c.data, c.lock, c.dataACI = nil, nil, ""

	return errors.Join(errs...)
}

// finishLink records the new account in accounts.json (next to any others) and keeps its
// database and lock.
func (c *meowClient) finishLink(links *store.LinkStore, data *mstore.DeviceData, deviceName string) (Account, error) {
	acc := accountFromDevice(data)
	acc.DeviceName = deviceName
	acc.LinkedAt = time.Now().UTC().Truncate(time.Second)

	err := c.dir.PutAccount(store.AccountEntry{
		Number:     acc.Number,
		ACI:        acc.ACI,
		PNI:        acc.PNI,
		DeviceID:   acc.DeviceID,
		DeviceName: acc.DeviceName,
		LinkedAt:   acc.LinkedAt,
	})
	if err != nil {
		return Account{}, fmt.Errorf("record account: %w", err)
	}

	err = c.release()
	if err != nil {
		return Account{}, err
	}

	c.data, c.lock = links.Take()
	c.dataACI = acc.ACI
	// Stay on the new account even when others are linked too.
	c.opts.Account = acc.ACI

	return acc, nil
}

// device returns the logged-in device of the selected account, opening its database if needed.
func (c *meowClient) device(ctx context.Context) (*mstore.Device, error) {
	acc, err := c.selectAccount()
	if err != nil {
		return nil, err
	}

	if c.dataACI != acc.ACI {
		err = c.release()
		if err != nil {
			return nil, err
		}

		c.data, err = c.dir.OpenAccount(ctx, acc.ACI, c.zlog)
		if err != nil {
			return nil, fmt.Errorf("open account: %w", err)
		}

		c.dataACI = acc.ACI
	}

	aci, err := uuid.Parse(acc.ACI)
	if err != nil {
		return nil, fmt.Errorf("accounts.json: invalid ACI %q: %w", acc.ACI, err)
	}

	device, err := c.data.DeviceByACI(ctx, aci)
	if err != nil {
		return nil, fmt.Errorf("load device: %w", err)
	}

	if device == nil || !device.IsDeviceLoggedIn() {
		return nil, fmt.Errorf("%w: %s has no device data", ErrNotLinked, acc.Number)
	}

	return device, nil
}

// selectAccount resolves opts.Account against accounts.json (see SelectAccount).
func (c *meowClient) selectAccount() (Account, error) {
	entries, err := c.dir.Accounts()
	if err != nil {
		return Account{}, fmt.Errorf("load accounts: %w", err)
	}

	accounts := make([]Account, 0, len(entries))
	for _, entry := range entries {
		accounts = append(accounts, Account{
			Number: entry.Number, ACI: entry.ACI, PNI: entry.PNI, DeviceID: entry.DeviceID,
			DeviceName: entry.DeviceName, LinkedAt: entry.LinkedAt, UnlinkedAt: entry.UnlinkedAt,
		})
	}

	return SelectAccount(accounts, c.opts.Account)
}

// handle is signalmeow's event handler. Its return value decides whether the envelope is acked,
// so it only returns true once the event has been handed to the consumer. Once Close has
// started it leaves envelopes for the next run. Send-only mode still consumes internal
// store updates, including verification, while leaving public events unread. Identity
// changes are reported before public events (see reportIdentityChanges).
func (c *meowClient) handle(raw events.SignalEvent) bool {
	if !c.begin(&c.handling) {
		return false
	}
	defer c.handling.Done()

	if update, ok := raw.(*events.IdentityVerification); ok {
		return c.handleIdentityVerification(update)
	}

	return c.handleReceivedEvent(raw)
}

func (c *meowClient) handleReceivedEvent(raw events.SignalEvent) bool {
	ctx, cancel := context.WithTimeout(c.zlog.WithContext(context.Background()), overrideSettleTimeout)
	err := c.learnChatTimers(ctx, raw)

	cancel()

	if err != nil {
		c.log.Warn("persist chat timers", "error", err)

		return false
	}

	if list, ok := raw.(*events.ContactList); ok {
		c.contactsStored(list)
	}

	evt := c.checkLoggedOut(convertEvent(raw, c.ownACI))
	if evt == nil {
		c.log.Debug("ignoring event", "type", fmt.Sprintf("%T", raw))

		return true
	}

	if _, isConn := evt.(*Connection); c.sendOnly && !isConn {
		c.log.Debug("send-only: leaving event on the server", "type", fmt.Sprintf("%T", evt))

		return false
	}

	ctx, cancel = context.WithTimeout(c.zlog.WithContext(context.Background()), overrideSettleTimeout)
	err = c.learnPollState(ctx, evt)

	cancel()

	if err != nil {
		c.log.Warn("persist poll state", "error", err)
		return false
	}

	if !c.reportIdentityChanges(evt) || !c.emit(evt) {
		return false
	}

	c.acked.Store(true)

	return true
}

// contactsStored follows up on contacts signalmeow has stored. Without IsFromDB they are the
// phone's contact list, which a running Sync waits for; with it they come from a storage sync,
// which may have undone a pending block or unblock. The event itself is dropped by handle and
// acked, in send-only mode too: there is nothing left to deliver.
func (c *meowClient) contactsStored(list *events.ContactList) {
	if !list.IsFromDB {
		c.contactListArrived(len(list.Contacts))

		return
	}

	ctx, cancel := context.WithTimeout(c.zlog.WithContext(context.Background()), overrideSettleTimeout)
	defer cancel()

	c.storageSynced(ctx, c.connDevice, list.Contacts)
}

// checkLoggedOut marks the connected account as unlinked when evt says the server logged the
// device out, and replaces the event's cause with UnlinkedError. Other events pass through.
// (Logouts seen by the websockets arrive through the supervisor instead.)
func (c *meowClient) checkLoggedOut(evt Event) Event {
	conn, ok := evt.(*Connection)
	if !ok || conn.State != StateLoggedOut {
		return evt
	}

	// The key checker is still a receive worker. Cancel without joining it, before
	// registry writes or unbuffered delivery can block; the external owner joins it.
	if c.stopSupervisor != nil {
		c.stopSupervisor()
	}

	return &Connection{State: StateLoggedOut, Err: c.markUnlinked(c.account, conn.Err)}
}

// markUnlinked records in accounts.json that the server no longer accepts acc's device and
// returns the error to report. The server's own error only goes to the debug log.
func (c *meowClient) markUnlinked(acc Account, cause error) error {
	c.log.Debug("server logged out this device", "account", acc.Number, "error", cause)

	err := c.dir.MarkUnlinked(acc.ACI, time.Now().UTC().Truncate(time.Second))
	if err != nil {
		c.log.Warn("mark account as unlinked", "account", acc.Number, "error", err)
	}

	return UnlinkedError(acc)
}

// emit delivers evt to the consumer. It reports false if the client was closed first. In
// send-only mode nobody reads Events, so it only records a connection lost for good (see
// connectionLost) and never blocks.
func (c *meowClient) emit(evt Event) bool {
	if c.sendOnly {
		c.noteConnection(evt)

		return true
	}

	select {
	case c.events <- evt:
		return true
	case <-c.done:
		return false
	}
}

func accountFromDevice(data *mstore.DeviceData) Account {
	return Account{
		Number:   data.Number,
		ACI:      data.ACI.String(),
		PNI:      data.PNI.String(),
		DeviceID: data.DeviceID,
	}
}
