//go:build cgo || purego

package signal

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "go.mau.fi/mautrix-signal/pkg/signalmeow/store"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/types"
)

const (
	// masterKeyPollInterval is how often Sync looks for a storage key the phone sent.
	masterKeyPollInterval = 500 * time.Millisecond
	// lastSyncKey is the gosignal_meta key recording when the last complete Sync finished.
	lastSyncKey = "last_sync"
)

// Sync runs the initial sync on the connected client (see Client.Sync).
//
// signalmeow does the storing: it stores the phone's contact list (a SyncMessage.Contacts reply
// to our request) before it hands us an events.ContactList, which handle passes to the waiter
// registered here, in send-only mode too. A storage key the phone sends (SyncMessage.Keys) is
// stored before signalmeow returns from the envelope, and nothing reaches our handler, so Sync
// polls the device table for it.
//
// signalmeow's SyncStorage returns no error, so Sync first fetches the storage service itself
// with FetchStorage only to see whether that works, and then lets SyncStorage fetch and store
// it again: the manifest and records are downloaded twice, which is cheap next to not knowing.
// Since SyncStorage's own fetch or database update can still fail silently, Sync then checks that
// the store holds the contacts and groups of the first fetch.
func (c *meowClient) Sync(ctx context.Context, opts SyncOptions) (SyncResult, error) {
	if c.cancelLoops == nil {
		return SyncResult{}, ErrNotConnected
	}

	if !c.begin(&c.sending) {
		return SyncResult{}, ErrClosed
	}
	defer c.sending.Done()

	err := c.connectionLost()
	if err != nil {
		return SyncResult{}, fmt.Errorf("sync: %w", err)
	}

	c.cliMu.Lock()
	cli := c.cli
	c.cliMu.Unlock()

	res, problems := c.runSync(c.zlog.WithContext(ctx), cli, opts)

	return c.finishSync(ctx, res, problems, opts)
}

// runSync runs the stages of Sync and returns what it achieved and what went wrong.
func (c *meowClient) runSync(ctx context.Context, cli *signalmeow.Client, opts SyncOptions) (SyncResult, []error) {
	var (
		res      SyncResult
		problems []error
	)

	// Register before asking, so that a quick reply isn't missed.
	contactList, stopWaiting := c.awaitContactList()
	defer stopWaiting()

	opts.Report(SyncRequestingContacts)

	// signalmeow skips the request (reporting success) if this client sent one in the last
	// minute; the reply to that one is still to come then.
	err := cli.SendContactSyncRequest(ctx)
	requested := err == nil

	if err != nil {
		problems = append(problems, fmt.Errorf("request contacts: %w", err))
	}

	key, err := c.masterKey(ctx, cli, opts)
	if err != nil {
		problems = append(problems, err)
	}

	if key != nil {
		res.MasterKey = true

		opts.Report(SyncFetchingStorage)

		update, err := syncStorage(ctx, cli, key)
		if err != nil {
			problems = append(problems, err)
		} else {
			res.Storage = true

			// The sync may have undone a pending block or unblock (see SetBlocked).
			c.storageFetched(ctx, update)
		}
	}

	if requested {
		opts.Report(SyncWaitingForContacts)

		res.ContactList, err = c.waitContactList(ctx, contactList)
		if err != nil {
			problems = append(problems, err)
		}
	}

	return res, problems
}

// finishSync counts what the store holds, records a complete sync and reports SyncDone.
func (c *meowClient) finishSync(ctx context.Context, res SyncResult, problems []error, opts SyncOptions,
) (SyncResult, error) {
	// Close ran out of patience waiting for the sync; the store may be gone.
	select {
	case <-c.done:
		return SyncResult{}, ErrClosed
	default:
	}

	if slices.ContainsFunc(problems, func(err error) bool { return errors.Is(err, ErrClosed) }) {
		return SyncResult{}, ErrClosed
	}

	// A connection lost for good (e.g. the device was unlinked) is an error, not a partial sync.
	lost := c.connectionLost()
	if lost != nil {
		return SyncResult{}, fmt.Errorf("sync: %w", lost)
	}

	// Count on a fresh context: the sync's one may just have run out.
	countCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ackFlushTimeout)
	defer cancel()

	var err error

	res.Contacts, res.Groups, err = c.syncCounts(countCtx, c.connDevice)
	if err != nil {
		return res, fmt.Errorf("sync: %w", err)
	}

	if res.Complete() {
		err = c.data.SetMeta(countCtx, lastSyncKey, time.Now().UTC().Format(time.RFC3339))
		if err != nil {
			c.log.Warn("record sync time", "error", err)
		}
	}

	opts.Report(SyncDone)

	if !res.Complete() {
		return res, fmt.Errorf("%w (missing %s): %w",
			ErrSyncIncomplete, strings.Join(res.Missing(), ", "), syncErrors(problems))
	}

	return res, nil
}

// masterKey returns the storage service key, asking the phone for it and waiting until it arrives
// if it is unknown. It returns nil with the reason when it didn't arrive in time.
func (c *meowClient) masterKey(ctx context.Context, cli *signalmeow.Client, opts SyncOptions) ([]byte, error) {
	key, err := c.storedMasterKey(ctx)
	if key != nil || err != nil {
		return key, err
	}

	opts.Report(SyncWaitingForKey)

	// signalmeow asks once on connect already; ask again in case that got lost.
	err = cli.SendStorageMasterKeyRequest(ctx)
	if err != nil {
		return nil, fmt.Errorf("request storage key: %w", err)
	}

	ticker := time.NewTicker(masterKeyPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for storage key: %w", ctx.Err())
		case <-c.done:
			return nil, ErrClosed
		case <-ticker.C:
		}

		key, err = c.storedMasterKey(ctx)
		if key != nil || err != nil {
			return key, err
		}
	}
}

// storedMasterKey loads the storage service key from the device table; nil if it is unknown.
// signalmeow writes the key into the shared device struct from its receive loop, so it is read
// from the database instead.
func (c *meowClient) storedMasterKey(ctx context.Context) ([]byte, error) {
	device, err := c.data.Devices.DeviceByACI(ctx, c.connDevice.ACI)
	if err != nil {
		return nil, fmt.Errorf("load storage key: %w", err)
	}

	if device == nil || len(device.MasterKey) == 0 {
		return nil, nil
	}

	return device.MasterKey, nil
}

// syncStorage fetches the storage service once to find out whether that works, and then has
// signalmeow fetch and store it (see Sync). SyncStorage only logs its failures, so the store is
// checked afterwards for what the first fetch got (see verifyStorageStored). It returns what the
// first fetch got; nil, without syncing, if the storage service has no manifest.
func syncStorage(ctx context.Context, cli *signalmeow.Client, key []byte) (*signalmeow.StorageUpdate, error) {
	update, err := cli.FetchStorage(ctx, key, 0, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch storage service: %w", err)
	}

	// Nothing to store, and SyncStorage would dereference its own nil update.
	if update == nil {
		return nil, nil //nolint:nilnil // no manifest yet: synced, with nothing to store
	}

	cli.SyncStorage(ctx)

	// SyncStorage gives up silently when ctx ends.
	err = ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("sync storage service: %w", err)
	}

	err = verifyStorageStored(ctx, cli.Store, update)
	if err != nil {
		return nil, fmt.Errorf("sync storage service: %w", err)
	}

	return update, nil
}

// verifyStorageStored checks that the store holds what signalmeow's SyncStorage stores from
// update: a recipient row for every contact record with an ACI, and the master key of every
// GroupV2 record with a valid one. SyncStorage returns no error, so this is how Sync notices that
// its fetch or the database update failed (ErrStorageNotStored). Only what signalmeow is known to
// store is checked: it skips contact records with neither ACI nor PNI and group keys of the wrong
// length, stores PNI-only contacts by PNI, and has no use for the other record types. Nothing is
// created while checking. signalmeow deletes nothing, so a record the phone removed between the
// two fetches only counts as missing if it was new in the first one (e.g. a group joined and left
// in that second), which the next sync resolves; one added in between isn't checked. A nil update
// (no manifest) has nothing to check.
func verifyStorageStored(ctx context.Context, device *mstore.Device, update *signalmeow.StorageUpdate) error {
	if update == nil {
		return nil
	}

	loader, ok := device.RecipientStore.(recipientLoader)
	if !ok {
		return errNoRecipientLoader
	}

	var contacts, groups int

	for _, record := range update.NewRecords {
		contact, group, err := recordMissing(ctx, loader, device.GroupStore, record)
		if err != nil {
			return err
		}

		if contact {
			contacts++
		}

		if group {
			groups++
		}
	}

	if contacts > 0 || groups > 0 {
		return fmt.Errorf("%w: %d contacts, %d groups missing", ErrStorageNotStored, contacts, groups)
	}

	return nil
}

// recordMissing reports whether the store lacks the user of a contact record or the group of a
// group record (see verifyStorageStored).
func recordMissing(
	ctx context.Context, loader recipientLoader, groups mstore.GroupStore, record *signalmeow.DecryptedStorageRecord,
) (bool, bool, error) {
	switch data := record.StorageRecord.GetRecord().(type) {
	case *signalpb.StorageRecord_Contact:
		missing, err := contactMissing(ctx, loader, data.Contact)

		return missing, false, err
	case *signalpb.StorageRecord_GroupV2:
		missing, err := groupMissing(ctx, groups, data.GroupV2)

		return false, missing, err
	default:
		return false, false, nil
	}
}

// contactMissing reports whether the store lacks the user of a contact record with an ACI (see
// verifyStorageStored).
func contactMissing(ctx context.Context, loader recipientLoader, contact *signalpb.ContactRecord) (bool, error) {
	// signalmeow ignores the parse error; a malformed ACI is not checked.
	aci, parseErr := signalmeow.ParseStringOrBinaryUUID(contact.GetAci(), contact.GetAciBinary())
	if parseErr == nil && aci != uuid.Nil {
		rcpt, err := loader.LoadRecipientByACI(ctx, aci)
		if err != nil {
			return false, fmt.Errorf("check stored contact %s: %w", aci, err)
		}

		return rcpt == nil, nil
	}

	return false, nil
}

// groupMissing reports whether the store lacks the master key of a group record with a valid
// one (see verifyStorageStored).
func groupMissing(ctx context.Context, groups mstore.GroupStore, group *signalpb.GroupV2Record) (bool, error) {
	if len(group.GetMasterKey()) != libsignalgo.GroupMasterKeyLength {
		return false, nil
	}

	gid, err := groupIDFromMasterKey(group.GetMasterKey())
	if err != nil {
		return false, fmt.Errorf("check stored group: %w", err)
	}

	known, err := knownGroup(ctx, groups, gid)
	if err != nil {
		return false, fmt.Errorf("check stored group: %w", err)
	}

	return !known, nil
}

// waitContactList waits for the phone's contact list.
func (c *meowClient) waitContactList(ctx context.Context, arrived <-chan int) (bool, error) {
	// It may have arrived while ctx ran out waiting for the storage key.
	select {
	case n := <-arrived:
		c.log.Debug("contact list arrived", "contacts", n)

		return true, nil
	default:
	}

	select {
	case n := <-arrived:
		c.log.Debug("contact list arrived", "contacts", n)

		return true, nil
	case <-ctx.Done():
		return false, fmt.Errorf("wait for contact list: %w", ctx.Err())
	case <-c.done:
		return false, ErrClosed
	}
}

// syncCounts counts the contacts (users with a name or number, without ourselves) and the
// groups with a master key that the store holds for device.
func (c *meowClient) syncCounts(ctx context.Context, device *mstore.Device) (int, int, error) {
	contacts, err := device.RecipientStore.LoadAllContacts(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("count contacts: %w", err)
	}

	contacts = slices.DeleteFunc(contacts, func(r *types.Recipient) bool { return r.ACI == device.ACI })

	groups, err := c.data.GroupIdentifiers(ctx, device.ACI.String())
	if err != nil {
		return 0, 0, fmt.Errorf("count groups: %w", err)
	}

	return len(contacts), len(groups), nil
}

// awaitContactList registers a waiter for the phone's contact list: the channel receives the
// number of contacts in it. stop unregisters it.
func (c *meowClient) awaitContactList() (<-chan int, func()) {
	waiter := make(chan int, 1)

	c.contactsMu.Lock()
	c.contactWaiters = append(c.contactWaiters, waiter)
	c.contactsMu.Unlock()

	return waiter, func() {
		c.contactsMu.Lock()
		defer c.contactsMu.Unlock()

		c.contactWaiters = slices.DeleteFunc(c.contactWaiters, func(w chan int) bool { return w == waiter })
	}
}

// contactListArrived tells the waiters that the phone's contact list with n contacts has been
// stored.
func (c *meowClient) contactListArrived(n int) {
	c.contactsMu.Lock()
	defer c.contactsMu.Unlock()

	for _, ch := range c.contactWaiters {
		select {
		case ch <- n:
		default: // it already got one
		}
	}
}

// syncErrors joins the reasons of an incomplete sync on one line.
type syncErrors []error

func (p syncErrors) Error() string {
	msgs := make([]string, 0, len(p))
	for _, err := range p {
		msgs = append(msgs, err.Error())
	}

	return strings.Join(msgs, "; ")
}

func (p syncErrors) Unwrap() []error {
	return p
}
