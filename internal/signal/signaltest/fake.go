// Package signaltest provides an in-memory signal.Client for tests. It needs no cgo.
package signaltest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

// LinkURI is the URI the fake passes to Link's onURI callback.
const LinkURI = "sgnl://linkdevice?uuid=fake&pub_key=fake"

// Fake is shared state behind the clients its Factory opens, so that e.g. a `link` followed by a
// `receive` in the same test see the same accounts. Like the real account lock, only one open
// client at a time can be connected per account (ErrAccountInUse). Set the exported fields before use.
//
// A remote unlink is simulated like the real client sees it: a signal.StateLoggedOut Connection
// in Incoming, or DevicesErr wrapping signal.ErrDeviceUnlinked, marks the account as unlinked in
// Linked (UnlinkedAt) and reports signal.UnlinkedError. Connect and Devices then fail with it
// right away, and Unlink skips the server as with LocalOnly.
type Fake struct {
	// Linked are the stored accounts, selected like the real client does (signal.SelectAccount).
	// Link adds LinkAs, replacing an entry with the same ACI.
	Linked []signal.Account
	// LinkAs is the account Link creates.
	LinkAs signal.Account
	// Incoming is delivered on Events after Connect, in order. Like the real client, Events is
	// unbuffered, so an event counts as delivered (Delivered) once the consumer has taken it.
	// A StateLoggedOut Connection marks the account as unlinked; its Err becomes UnlinkedError.
	// After Connect with signal.SendOnly nothing is delivered (the events stay "on the server"),
	// but a StateLoggedOut or StateFailed Connection makes Send fail with its error.
	Incoming []signal.Event
	// Devices is what Devices returns for any account.
	Devices []signal.Device
	// Directory are the users on Signal: Resolve finds them by Number or Username (with ACI set).
	// Like the real client, looking up a number needs Connect.
	Directory []signal.Recipient
	// Groups are the groups Send knows, by base64 group ID, with their members; our own ACI
	// among them is skipped like the real client does. Other groups fail with
	// signal.ErrUnknownGroup.
	Groups map[string][]signal.Recipient
	// SendFailures makes sending to the recipients (or group members) with these ACIs fail.
	SendFailures map[string]error
	// InUse simulates another process holding the account lock: Connect and Unlink fail.
	InUse bool
	// Attachments is the CDN for Download: content by RemoteAttachment.CDNKey. Other
	// attachments fail with signal.ErrAttachmentNotFound.
	Attachments map[string][]byte
	// DownloadErrs makes downloading the attachments with these CDN keys fail.
	DownloadErrs map[string]error

	// OpenErr, LinkErr, ConnectErr, UploadErr, SendErr and DevicesErr make the respective call
	// fail.
	OpenErr    error
	LinkErr    error
	ConnectErr error
	UploadErr  error
	SendErr    error
	DevicesErr error
	// UnlinkErr makes removing the device on the server fail; Unlink with LocalOnly ignores it.
	UnlinkErr error
	// ReceiptErr makes SendReceipt fail.
	ReceiptErr error
	// SyncResult and SyncErr are what Sync returns once connected, e.g. a partial result with an
	// error wrapping signal.ErrSyncIncomplete.
	SyncResult signal.SyncResult
	SyncErr    error

	// GroupInfo are the groups on the server with their full state, by ID: Groups lists them,
	// Group and LeaveGroup find them by ID (or master key, see GroupKeys), and GroupTitles
	// returns their titles (as if cached). Membership and Role are filled in for the connected
	// account like the real client does. Send knows their members too, unless Groups has an
	// entry for the same ID.
	GroupInfo map[string]signal.Group
	// GroupKeys maps base64 master keys to group IDs, as deriving the ID from a master key does.
	GroupKeys map[string]string
	// GroupErrs makes fetching these groups (by ID) fail. Groups lists a group failing with
	// signal.ErrNotAMember or signal.ErrUnknownGroup with Err set (also IDs missing from
	// GroupInfo); other errors fail the whole list.
	GroupErrs map[string]error
	// LeaveErr makes LeaveGroup fail after its checks.
	LeaveErr error
	// LeaveTime is what LeaveGroup records as Group.LeftAt; zero means now.
	LeaveTime time.Time

	mu        sync.Mutex
	opened    []signal.Options
	sent      []signal.SendRequest
	uploaded  []signal.OutgoingAttachment
	connects  []string
	unlinks   []UnlinkCall
	receipts  []ReceiptCall
	syncs     []string
	delivered int
	nextTS    uint64
	clients   []*client
	leaves    []LeaveCall
	left      map[string]time.Time // groups left with LeaveGroup, by ID
}

// Factory is a signal.Factory that opens clients on f.
func (f *Fake) Factory(_ context.Context, opts signal.Options) (signal.Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.opened = append(f.opened, opts)

	if f.OpenErr != nil {
		return nil, f.OpenErr
	}

	cli := &client{fake: f, opts: opts, events: make(chan signal.Event), done: make(chan struct{})}
	f.clients = append(f.clients, cli)

	return cli, nil
}

// Opened returns the options of every Factory call.
func (f *Fake) Opened() []signal.Options {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]signal.Options(nil), f.opened...)
}

// Sent returns every successful SendRequest.
func (f *Fake) Sent() []signal.SendRequest {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]signal.SendRequest(nil), f.sent...)
}

// Uploaded returns the attachments of all successful Upload calls, in order.
func (f *Fake) Uploaded() []signal.OutgoingAttachment {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]signal.OutgoingAttachment(nil), f.uploaded...)
}

// Connects returns the ACI of the account each successful Connect used.
func (f *Fake) Connects() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.connects...)
}

// Delivered returns the number of Incoming events the consumers have taken from Events.
func (f *Fake) Delivered() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.delivered
}

// ReceiptCall records a successful SendReceipt.
type ReceiptCall struct {
	Sender     signal.Recipient
	Type       signal.ReceiptType
	Timestamps []uint64
}

// Receipts returns every successful SendReceipt, in order.
func (f *Fake) Receipts() []ReceiptCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]ReceiptCall(nil), f.receipts...)
}

// Syncs returns the ACI of the account each Sync that got past the connection checks used.
func (f *Fake) Syncs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.syncs...)
}

// UnlinkCall records a successful Unlink.
type UnlinkCall struct {
	ACI       string
	LocalOnly bool
}

// Unlinks returns every successful Unlink.
func (f *Fake) Unlinks() []UnlinkCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]UnlinkCall(nil), f.unlinks...)
}

// AllClosed reports whether every opened client has been closed.
func (f *Fake) AllClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, cli := range f.clients {
		if !cli.closed {
			return false
		}
	}

	return true
}

// account mirrors the real client's selection; the caller holds f.mu.
func (f *Fake) account(opts signal.Options) (signal.Account, error) {
	acc, err := signal.SelectAccount(f.Linked, opts.Account)
	if err != nil {
		return signal.Account{}, fmt.Errorf("fake: %w", err)
	}

	return acc, nil
}

// markUnlinked mirrors the real client recording a logout in accounts.json and returns the
// error it reports; the caller holds f.mu.
func (f *Fake) markUnlinked(acc signal.Account) error {
	for i := range f.Linked {
		if f.Linked[i].ACI == acc.ACI && !f.Linked[i].Unlinked() {
			f.Linked[i].UnlinkedAt = time.Now().UTC().Truncate(time.Second)
		}
	}

	return unlinkedError(acc)
}

// unlinkedError is the error the real client reports for an unlinked acc.
func unlinkedError(acc signal.Account) error {
	return fmt.Errorf("%w (fake)", signal.UnlinkedError(acc))
}

// checkInUse mirrors the account lock; the caller holds f.mu.
func (f *Fake) checkInUse(aci string) error {
	if f.InUse {
		return fmt.Errorf("%w (fake)", signal.ErrAccountInUse)
	}

	for _, other := range f.clients {
		if other.connected == aci && !other.closed {
			return fmt.Errorf("%w (fake)", signal.ErrAccountInUse)
		}
	}

	return nil
}

type client struct {
	fake      *Fake
	opts      signal.Options
	events    chan signal.Event
	done      chan struct{} // closed by Close; stops the feeder
	fed       chan struct{} // closed when the feeder started by Connect exits
	connected string        // ACI of the connected account
	lost      error         // connection lost for good, in send-only mode
	uploads   []string      // IDs of the attachments Upload returned
	closed    bool
}

func (c *client) Link(_ context.Context, _ string, onURI func(string)) (signal.Account, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	if c.fake.LinkErr != nil {
		return signal.Account{}, c.fake.LinkErr
	}

	onURI(LinkURI)

	acc := c.fake.LinkAs
	c.fake.Linked = slices.DeleteFunc(c.fake.Linked, func(old signal.Account) bool { return old.ACI == acc.ACI })
	c.fake.Linked = append(c.fake.Linked, acc)
	// Like the real client, stay on the new account.
	c.opts.Account = acc.ACI

	return acc, nil
}

func (c *client) Account(context.Context) (signal.Account, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	return c.fake.account(c.opts)
}

func (c *client) Connect(_ context.Context, opts ...signal.ConnectOption) error {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	acc, err := c.fake.account(c.opts)
	if err != nil {
		return err
	}

	if acc.Unlinked() {
		return unlinkedError(acc)
	}

	if c.fake.ConnectErr != nil {
		return c.fake.ConnectErr
	}

	err = c.fake.checkInUse(acc.ACI)
	if err != nil {
		return err
	}

	c.connected = acc.ACI
	c.fake.connects = append(c.fake.connects, acc.ACI)

	incoming := make([]signal.Event, 0, len(c.fake.Incoming))
	for _, evt := range c.fake.Incoming {
		if conn, ok := evt.(*signal.Connection); ok && conn.State == signal.StateLoggedOut {
			evt = &signal.Connection{State: signal.StateLoggedOut, Err: c.fake.markUnlinked(acc)}
		}

		incoming = append(incoming, evt)
	}

	if signal.NewConnectOptions(opts...).SendOnly {
		c.lost = lostError(incoming)

		return nil
	}

	c.fed = make(chan struct{})
	go c.feed(incoming)

	return nil
}

func (c *client) Events() <-chan signal.Event {
	return c.events
}

func (c *client) Download(_ context.Context, att signal.Attachment) ([]byte, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	key := att.Remote.CDNKey

	err := c.fake.DownloadErrs[key]
	if err != nil {
		return nil, err
	}

	data, ok := c.fake.Attachments[key]
	if !ok || key == "" {
		return nil, fmt.Errorf("%w (fake)", signal.ErrAttachmentNotFound)
	}

	return slices.Clone(data), nil
}

func (c *client) Resolve(_ context.Context, recipients []signal.Recipient) ([]signal.Recipient, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	if c.closed {
		return nil, signal.ErrClosed
	}

	out := slices.Clone(recipients)

	var errs []error

	for i, rcpt := range out {
		if rcpt.ACI != "" {
			continue
		}

		known, err := c.lookup(rcpt)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w (fake)", rcpt, err))

			continue
		}

		out[i] = known
	}

	err := errors.Join(errs...)
	if err != nil {
		return nil, err
	}

	return out, nil
}

func (c *client) Upload(
	_ context.Context, attachments []signal.OutgoingAttachment,
) ([]signal.UploadedAttachment, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	switch {
	case c.closed:
		return nil, signal.ErrClosed
	case c.connected == "":
		return nil, signal.ErrNotConnected
	case c.lost != nil:
		return nil, fmt.Errorf("upload: %w", c.lost)
	case c.fake.UploadErr != nil:
		return nil, c.fake.UploadErr
	}

	out := make([]signal.UploadedAttachment, 0, len(attachments))

	for _, att := range attachments {
		id := fmt.Sprintf("upload-%d", len(c.fake.uploaded)+1)
		c.fake.uploaded = append(c.fake.uploaded, att)
		c.uploads = append(c.uploads, id)
		out = append(out, signal.UploadedAttachment{
			ID:          id,
			ContentType: att.ContentType,
			Filename:    att.Filename,
			Size:        uint32(len(att.Data)), //nolint:gosec // test data
		})
	}

	return out, nil
}

func (c *client) Send(_ context.Context, req signal.SendRequest) (signal.SendResult, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err := c.checkSend(req)
	if err != nil {
		return signal.SendResult{}, err
	}

	recipients, err := c.sendTo(req)
	if err != nil {
		return signal.SendResult{}, err
	}

	c.fake.sent = append(c.fake.sent, req)

	res := signal.SendResult{Timestamp: req.Timestamp}
	if res.Timestamp == 0 {
		c.fake.nextTS++
		res.Timestamp = c.fake.nextTS
	}

	for _, rcpt := range recipients {
		result := signal.RecipientResult{Recipient: rcpt, Err: c.fake.SendFailures[rcpt.ACI]}
		// Sealed sender, except for the sync transcript of a note-to-self.
		result.Unidentified = result.Err == nil && rcpt.ACI != c.connected
		res.Results = append(res.Results, result)
	}

	return res, nil
}

func (c *client) SendReceipt(
	_ context.Context, sender signal.Recipient, typ signal.ReceiptType, timestamps []uint64,
) error {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	switch {
	case c.closed:
		return signal.ErrClosed
	case c.connected == "":
		return signal.ErrNotConnected
	case typ < signal.ReceiptDelivery || typ > signal.ReceiptViewed || len(timestamps) == 0:
		return fmt.Errorf("%w (fake)", signal.ErrInvalidReceipt)
	case sender.ACI == "":
		return fmt.Errorf("%s: %w (fake)", sender, signal.ErrUnresolvable)
	case c.lost != nil:
		return fmt.Errorf("receipt: %w", c.lost)
	case c.fake.ReceiptErr != nil:
		return c.fake.ReceiptErr
	}

	c.fake.receipts = append(c.fake.receipts, ReceiptCall{
		Sender: sender, Type: typ, Timestamps: slices.Clone(timestamps),
	})

	return nil
}

func (c *client) Sync(_ context.Context, opts signal.SyncOptions) (signal.SyncResult, error) {
	c.fake.mu.Lock()

	switch {
	case c.closed:
		c.fake.mu.Unlock()

		return signal.SyncResult{}, signal.ErrClosed
	case c.connected == "":
		c.fake.mu.Unlock()

		return signal.SyncResult{}, signal.ErrNotConnected
	case c.lost != nil:
		c.fake.mu.Unlock()

		return signal.SyncResult{}, fmt.Errorf("sync: %w", c.lost)
	}

	c.fake.syncs = append(c.fake.syncs, c.connected)
	res, err := c.fake.SyncResult, c.fake.SyncErr
	c.fake.mu.Unlock()

	// Outside the lock, in case Progress calls back into the fake. The storage key is always
	// known.
	for _, stage := range []signal.SyncStage{
		signal.SyncRequestingContacts, signal.SyncFetchingStorage, signal.SyncWaitingForContacts, signal.SyncDone,
	} {
		opts.Report(stage)
	}

	return res, err
}

// lostError returns the error of the first event in incoming that ends the connection for good.
func lostError(incoming []signal.Event) error {
	for _, evt := range incoming {
		conn, ok := evt.(*signal.Connection)
		if !ok {
			continue
		}

		switch conn.State {
		case signal.StateLoggedOut, signal.StateFailed:
			if conn.Err != nil {
				return conn.Err
			}

			return signal.ErrConnectionFailed
		case signal.StateConnected, signal.StateDisconnected, signal.StateError:
		}
	}

	return nil
}

func (c *client) Devices(context.Context) ([]signal.Device, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	acc, err := c.fake.account(c.opts)
	if err != nil {
		return nil, err
	}

	if acc.Unlinked() {
		return nil, unlinkedError(acc)
	}

	if errors.Is(c.fake.DevicesErr, signal.ErrDeviceUnlinked) {
		return nil, c.fake.markUnlinked(acc)
	}

	if c.fake.DevicesErr != nil {
		return nil, c.fake.DevicesErr
	}

	return append([]signal.Device(nil), c.fake.Devices...), nil
}

func (c *client) Unlink(_ context.Context, opts signal.UnlinkOptions) (signal.Account, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	acc, err := c.fake.account(c.opts)
	if err != nil {
		return signal.Account{}, err
	}

	err = c.fake.checkInUse(acc.ACI)
	if err != nil {
		return signal.Account{}, err
	}

	// The server already removed a device marked as unlinked.
	opts.LocalOnly = opts.LocalOnly || acc.Unlinked()

	if !opts.LocalOnly && c.fake.UnlinkErr != nil {
		return signal.Account{}, c.fake.UnlinkErr
	}

	c.fake.Linked = slices.DeleteFunc(c.fake.Linked, func(old signal.Account) bool { return old.ACI == acc.ACI })
	c.fake.unlinks = append(c.fake.unlinks, UnlinkCall{ACI: acc.ACI, LocalOnly: opts.LocalOnly})

	return acc, nil
}

func (c *client) Close() error {
	c.fake.mu.Lock()
	if c.closed {
		c.fake.mu.Unlock()

		return nil
	}

	c.closed = true
	fed := c.fed
	c.fake.mu.Unlock()

	close(c.done)

	if fed != nil {
		<-fed
	}

	close(c.events)

	return nil
}

// checkSend fails like the real client for a send it can't make.
func (c *client) checkSend(req signal.SendRequest) error {
	switch {
	case c.closed:
		return signal.ErrClosed
	case c.connected == "":
		return signal.ErrNotConnected
	case (req.GroupID == "") == (len(req.Recipients) == 0):
		return signal.ErrInvalidSendRequest
	case c.lost != nil:
		return fmt.Errorf("send: %w", c.lost)
	case c.fake.SendErr != nil:
		return c.fake.SendErr
	}

	return c.checkContent(req)
}

// checkContent fails like signal.SendRequest.Check, for attachments this client didn't upload
// and for a quote author or mentioned user without ACI.
func (c *client) checkContent(req signal.SendRequest) error {
	err := req.Check()
	if err != nil {
		return fmt.Errorf("%w (fake)", err)
	}

	for _, att := range req.Attachments {
		if !slices.Contains(c.uploads, att.ID) {
			return fmt.Errorf("%w: %s", signal.ErrUnknownAttachment, att.Filename)
		}
	}

	if req.Quote != nil && req.Quote.Author.ACI == "" {
		return fmt.Errorf("quote: %w", signal.ErrUnresolvable)
	}

	for _, mention := range req.Mentions {
		if mention.Recipient.ACI == "" {
			return fmt.Errorf("mention: %w", signal.ErrUnresolvable)
		}
	}

	return nil
}

// feed delivers incoming on Events until Close.
func (c *client) feed(incoming []signal.Event) {
	defer close(c.fed)

	for _, evt := range incoming {
		select {
		case c.events <- evt:
			c.fake.mu.Lock()
			c.fake.delivered++
			c.fake.mu.Unlock()
		case <-c.done:
			return
		}
	}
}

// sendTo returns the recipients of req: its Recipients, or the members of its group without us
// (like signalmeow); the caller holds c.fake.mu.
func (c *client) sendTo(req signal.SendRequest) ([]signal.Recipient, error) {
	if req.GroupID == "" {
		return req.Recipients, nil
	}

	members, ok := c.fake.Groups[req.GroupID]
	if !ok {
		members, ok = c.fake.groupMembers(req.GroupID)
	}

	if !ok {
		return nil, fmt.Errorf("%w %s (fake)", signal.ErrUnknownGroup, req.GroupID)
	}

	return slices.DeleteFunc(slices.Clone(members), func(m signal.Recipient) bool { return m.ACI == c.connected }), nil
}

// lookup finds rcpt in the Directory; the caller holds c.fake.mu.
func (c *client) lookup(rcpt signal.Recipient) (signal.Recipient, error) {
	var match func(signal.Recipient) bool

	switch {
	case rcpt.Number != "":
		if c.connected == "" {
			return signal.Recipient{}, signal.ErrNotConnected
		}

		match = func(known signal.Recipient) bool { return known.Number == rcpt.Number }
	case rcpt.Username != "":
		match = func(known signal.Recipient) bool { return strings.EqualFold(known.Username, rcpt.Username) }
	default:
		return signal.Recipient{}, signal.ErrUnresolvable
	}

	i := slices.IndexFunc(c.fake.Directory, match)
	if i < 0 {
		return signal.Recipient{}, signal.ErrNotOnSignal
	}

	known := c.fake.Directory[i]
	// Keep what the caller asked for, like the real client does.
	known.Number, known.Username = rcpt.Number, rcpt.Username

	return known, nil
}
