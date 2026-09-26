//go:build cgo

package signal

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "go.mau.fi/mautrix-signal/pkg/signalmeow/store"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/types"
)

// overrideSettleTimeout bounds settling the block overrides after a background storage sync,
// including the fetch of the storage service that it may take (see storageSynced; Close cancels
// that fetch).
const overrideSettleTimeout = 30 * time.Second

var errNoRecipientLoader = errors.New("signalmeow's recipient store can't load by ACI or PNI")

// recipientLoader is the part of signalmeow's recipient store that its RecipientStore interface
// leaves out. Unlike LoadAndUpdateRecipient, these don't create a row for an unknown user.
type recipientLoader interface {
	LoadRecipientByACI(ctx context.Context, aci uuid.UUID) (*types.Recipient, error)
	LoadRecipientByPNI(ctx context.Context, pni uuid.UUID) (*types.Recipient, error)
}

func (c *meowClient) Contacts(ctx context.Context) ([]Contact, error) {
	// Close waits for a running read like for a send, so that the store stays open.
	if !c.begin(&c.sending) {
		return nil, ErrClosed
	}
	defer c.sending.Done()

	ctx = c.zlog.WithContext(ctx)

	device, loader, err := c.recipientStore(ctx)
	if err != nil {
		return nil, err
	}

	known, err := c.loadContacts(ctx, device, loader)
	if err != nil {
		return nil, err
	}

	pending, err := c.pendingOverrides(ctx, time.Now())
	if err != nil {
		return nil, err
	}

	out := make([]Contact, 0, len(known))

	for _, rcpt := range known {
		if rcpt.ACI != uuid.Nil && rcpt.ACI == device.ACI {
			continue
		}

		out = append(out, withOverride(contactFrom(rcpt), pending))
	}

	return out, nil
}

// loadContacts loads the users with a name or number, and the blocked ones.
func (c *meowClient) loadContacts(ctx context.Context, device *mstore.Device, loader recipientLoader,
) ([]*types.Recipient, error) {
	known, err := device.RecipientStore.LoadAllContacts(ctx)
	if err != nil {
		return nil, fmt.Errorf("load contacts: %w", err)
	}

	// LoadAllContacts only has users with a name or number; blocked users count, too.
	blocked, err := c.data.BlockedACIs(ctx, device.ACI.String())
	if err != nil {
		return nil, err //nolint:wrapcheck // the store's errors say what failed
	}

	seen := make(map[uuid.UUID]bool, len(known))
	for _, rcpt := range known {
		seen[rcpt.ACI] = true
	}

	for _, raw := range blocked {
		aci, err := uuid.Parse(raw)
		if err != nil || seen[aci] {
			continue
		}

		rcpt, err := loader.LoadRecipientByACI(ctx, aci)
		if err != nil {
			return nil, fmt.Errorf("load contact %s: %w", aci, err)
		}

		if rcpt != nil {
			known = append(known, rcpt)
		}
	}

	return known, nil
}

func (c *meowClient) Contact(ctx context.Context, rcpt Recipient) (Contact, error) {
	if !c.begin(&c.sending) {
		return Contact{}, ErrClosed
	}
	defer c.sending.Done()

	ctx = c.zlog.WithContext(ctx)

	device, loader, err := c.recipientStore(ctx)
	if err != nil {
		return Contact{}, err
	}

	found, err := loadRecipient(ctx, device.RecipientStore, loader, rcpt)
	if err != nil {
		return Contact{}, err
	}

	pending, err := c.pendingOverrides(ctx, time.Now())
	if err != nil {
		return Contact{}, err
	}

	return withOverride(contactFrom(found), pending), nil
}

// loadRecipient looks rcpt up by ACI, else PNI, else number, without creating a row.
func loadRecipient(
	ctx context.Context, recipients mstore.RecipientStore, loader recipientLoader, rcpt Recipient,
) (*types.Recipient, error) {
	var (
		found *types.Recipient
		err   error
	)

	switch {
	case rcpt.ACI != "":
		aci, parseErr := uuid.Parse(rcpt.ACI)
		if parseErr != nil {
			return nil, fmt.Errorf("%w: invalid ACI %q: %w", ErrUnresolvable, rcpt.ACI, parseErr)
		}

		found, err = loader.LoadRecipientByACI(ctx, aci)
	case rcpt.PNI != "":
		pni, parseErr := uuid.Parse(rcpt.PNI)
		if parseErr != nil {
			return nil, fmt.Errorf("%w: invalid PNI %q: %w", ErrUnresolvable, rcpt.PNI, parseErr)
		}

		found, err = loader.LoadRecipientByPNI(ctx, pni)
	case rcpt.Number != "":
		found, err = recipients.LoadRecipientByE164(ctx, rcpt.Number)
	}

	if err != nil {
		return nil, fmt.Errorf("load contact %s: %w", rcpt, err)
	}

	if found == nil {
		return nil, fmt.Errorf("%s: %w", rcptLabel(rcpt), ErrUnknownContact)
	}

	return found, nil
}

// rcptLabel names rcpt in errors: the number if set (that is what the user typed, usually).
func rcptLabel(rcpt Recipient) string {
	if rcpt.Number != "" && rcpt.ACI == "" {
		return rcpt.Number
	}

	return rcpt.String()
}

// blockTarget is a user to block or unblock: their ACI and the number given, if any.
type blockTarget struct {
	aci    uuid.UUID
	number string
}

func (c *meowClient) SetBlocked(ctx context.Context, recipients []Recipient, blocked bool) error {
	if c.cancelLoops == nil {
		return ErrNotConnected
	}

	targets := make([]blockTarget, 0, len(recipients))

	for _, rcpt := range recipients {
		id, err := aciServiceID(rcpt)
		if err != nil {
			return err
		}

		targets = append(targets, blockTarget{aci: id.UUID, number: rcpt.Number})
	}

	if !c.begin(&c.sending) {
		return ErrClosed
	}
	defer c.sending.Done()

	err := c.connectionLost()
	if err != nil {
		return fmt.Errorf("block: %w", err)
	}

	c.cliMu.Lock()
	cli := c.cli
	c.cliMu.Unlock()

	ctx = c.zlog.WithContext(ctx)

	update, err := c.fetchStorage(ctx, cli, 0)
	if err != nil {
		return err
	}

	return c.applyBlocked(ctx, c.connDevice.RecipientStore, update, targets, blocked,
		func(msg *signalpb.SyncMessage) error { return sendSyncMessage(ctx, cli, msg) })
}

// fetchStorage fetches the storage service's manifest and records, unless the manifest still has
// the version since (0: fetch in any case). It returns nil if it does, or if there is no manifest.
func (c *meowClient) fetchStorage(ctx context.Context, cli *signalmeow.Client, since uint64,
) (*signalmeow.StorageUpdate, error) {
	key, err := c.storedMasterKey(ctx)
	if err != nil {
		return nil, err
	}

	if key == nil {
		return nil, ErrStorageKeyUnknown
	}

	update, err := cli.FetchStorage(ctx, key, since, nil)
	if err != nil {
		return nil, fmt.Errorf("read the blocked list from the storage service: %w", err)
	}

	return update, nil
}

// applyBlocked makes the change of SetBlocked against update, the storage service as just
// fetched: it sends the complete new blocked list with send and then updates the store. Unless
// update has the complete blocked list (ErrBlockedListIncomplete), it sends and changes nothing.
func (c *meowClient) applyBlocked(
	ctx context.Context, recipients mstore.RecipientStore, update *signalmeow.StorageUpdate,
	targets []blockTarget, blocked bool, send func(*signalpb.SyncMessage) error,
) error {
	seen, err := completeStorage(update)
	if err != nil {
		return err
	}

	// Earlier overrides count, too: the phone may not have stored them yet.
	c.overridesMu.Lock()
	defer c.overridesMu.Unlock()

	now := time.Now()

	list, err := c.pendingBlockedList(ctx, recipients, seen, now)
	if err != nil {
		return err
	}

	for _, target := range targets {
		list.set(target.aci, knownNumber(ctx, recipients, target.aci, target.number), blocked, now)
	}

	err = send(list.syncMessage())
	if err != nil {
		return fmt.Errorf("send blocked list to our other devices: %w", err)
	}

	return c.storeBlocked(ctx, recipients, seen, targets, blocked, now)
}

// pendingBlockedList returns the blocked list of seen with the pending overrides applied that
// seen doesn't supersede; the caller holds overridesMu.
func (c *meowClient) pendingBlockedList(
	ctx context.Context, recipients mstore.RecipientStore, seen *storageSnapshot, now time.Time,
) (*blockedList, error) {
	list := seen.list.clone()

	overrides, err := c.data.BlockOverrides(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store's errors say what failed
	}

	for _, override := range overrides {
		if expired(override, now) || seen.supersedes(override) {
			continue
		}

		aci, err := uuid.Parse(override.ACI)
		if err != nil || list.isBlocked(aci) == override.Blocked {
			continue
		}

		list.set(aci, knownNumber(ctx, recipients, aci, ""), override.Blocked, override.SetAt)
	}

	return list, nil
}

// storeBlocked records the change in the store once the blocked list went out: an override,
// tied to seen's version, where the storage service (seen) says otherwise, and the new state of
// each user. Then it settles the other overrides against seen. The caller holds overridesMu.
func (c *meowClient) storeBlocked(
	ctx context.Context, recipients mstore.RecipientStore, seen *storageSnapshot, targets []blockTarget,
	blocked bool, now time.Time,
) error {
	for _, target := range targets {
		aci := target.aci.String()

		var err error

		if seen.list.isBlocked(target.aci) == blocked {
			err = c.data.DeleteBlockOverride(ctx, aci)
		} else {
			err = c.data.SetBlockOverride(ctx, store.BlockOverride{
				ACI: aci, Blocked: blocked, SetAt: now, StorageVersion: seen.version,
			})
		}

		if err != nil {
			return fmt.Errorf("blocked list sent, but: %w", err)
		}

		err = setBlocked(ctx, recipients, target.aci, blocked)
		if err != nil {
			return fmt.Errorf("blocked list sent, but: %w", err)
		}
	}

	c.settleOverridesLocked(ctx, recipients, seen, now)

	return nil
}

// knownNumber returns number, or else the number the store has for aci ("" if none).
func knownNumber(ctx context.Context, recipients mstore.RecipientStore, aci uuid.UUID, number string) string {
	if number != "" {
		return number
	}

	loader, ok := recipients.(recipientLoader)
	if !ok {
		return ""
	}

	rcpt, err := loader.LoadRecipientByACI(ctx, aci)
	if err != nil || rcpt == nil {
		return ""
	}

	return rcpt.E164
}

// sendSyncMessage sends msg to our other devices.
func sendSyncMessage(ctx context.Context, cli *signalmeow.Client, msg *signalpb.SyncMessage) error {
	sent := cli.SendMessage(ctx, cli.Store.ACIServiceID(), signalmeow.WrapSyncMessage(msg))
	if sent.WasSuccessful {
		return nil
	}

	if sent.Error != nil {
		return sent.Error
	}

	return ErrSendFailed
}

// setBlocked sets the blocked flag of aci in the store, creating a row for an unknown user.
func setBlocked(ctx context.Context, recipients mstore.RecipientStore, aci uuid.UUID, blocked bool) error {
	_, err := recipients.LoadAndUpdateRecipient(ctx, aci, uuid.Nil, func(rcpt *types.Recipient) (bool, error) {
		if rcpt.Blocked == blocked {
			return false, nil
		}

		rcpt.Blocked = blocked

		return true, nil
	})
	if err != nil {
		return fmt.Errorf("store blocked state of %s: %w", aci, err)
	}

	return nil
}

// settleOverrides settles the pending block overrides against seen, what a fetch of the storage
// service found (nil if nothing was fetched):
//   - an override that expired (BlockOverrideTTL) is dropped, and the store gets the state seen
//     has for the user if seen knows it. If it doesn't, the store keeps the overridden state until
//     signalmeow's next storage sync: that fetches every record and sets the blocked flag from
//     each contact record, changed or not (processStorageInTxn), so only a user without any
//     contact record in the storage service keeps it for good;
//   - one that seen supersedes (the phone has written the storage service since) is dropped and
//     the store gets the state seen has, if seen knows it (it read the user's record, or every
//     record). If seen's version is later but the user's record couldn't be read, the override
//     stays as it is, not re-applied (the phone may have changed it), until a fetch that knows or
//     until it expires;
//   - the others are re-applied to the store, since a storage sync may have undone them.
//
// Failures are only logged: the next settle tries again.
func (c *meowClient) settleOverrides(ctx context.Context, recipients mstore.RecipientStore, seen *storageSnapshot) {
	c.overridesMu.Lock()
	defer c.overridesMu.Unlock()

	c.settleOverridesLocked(ctx, recipients, seen, time.Now())
}

// settleOverridesLocked is settleOverrides; the caller holds overridesMu.
func (c *meowClient) settleOverridesLocked(
	ctx context.Context, recipients mstore.RecipientStore, seen *storageSnapshot, now time.Time,
) {
	overrides, err := c.data.BlockOverrides(ctx)
	if err != nil {
		c.log.Warn("re-apply blocked state", "error", err)

		return
	}

	for _, override := range overrides {
		switch {
		case expired(override, now):
			c.storeBlockedState(ctx, recipients, override.ACI, seen.state)
			c.dropOverride(ctx, override, "expired")
		case seen.supersedes(override) && seen.knows(override.ACI):
			// The phone has written the storage service since: it has our change or a newer one.
			c.dropOverride(ctx, override, "storage service changed since")
			c.storeBlockedState(ctx, recipients, override.ACI, seen.state)
		case seen.supersedes(override):
			// Written since, but the user's record wasn't read: keep the override, but don't
			// impose it on the store either.
			c.log.Debug("keeping block override: its record couldn't be read", "aci", override.ACI,
				"storage version", seen.version)
		default:
			c.storeBlockedState(ctx, recipients, override.ACI, func(uuid.UUID) (bool, bool) {
				return override.Blocked, true
			})
		}
	}
}

// storeBlockedState sets the blocked flag of the user aci in the store to what state says, if it
// knows. Failures are only logged.
func (c *meowClient) storeBlockedState(
	ctx context.Context, recipients mstore.RecipientStore, aci string, state func(uuid.UUID) (bool, bool),
) {
	parsed, err := uuid.Parse(aci)
	if err != nil {
		return
	}

	blocked, known := state(parsed)
	if !known {
		return
	}

	err = setBlocked(ctx, recipients, parsed, blocked)
	if err != nil {
		c.log.Warn("re-apply blocked state", "aci", aci, "error", err)
	}
}

// dropOverride deletes override from the store (why says why, for the log).
func (c *meowClient) dropOverride(ctx context.Context, override store.BlockOverride, why string) {
	c.log.Debug("dropping block override", "aci", override.ACI, "reason", why,
		"storage version", override.StorageVersion)

	err := c.data.DeleteBlockOverride(ctx, override.ACI)
	if err != nil {
		c.log.Warn("drop block override", "aci", override.ACI, "error", err)
	}
}

// storageFetcher fetches the storage service unless its manifest still has the version since
// (see fetchStorage).
type storageFetcher func(ctx context.Context, since uint64) (*signalmeow.StorageUpdate, error)

// storageSynced settles the overrides after signalmeow stored contacts from a background storage
// sync (a ContactList event with IsFromDB): changed has the users whose record changed.
func (c *meowClient) storageSynced(ctx context.Context, device *mstore.Device, changed []*types.Recipient) {
	if device == nil {
		return
	}

	c.storageSyncedWith(ctx, device.RecipientStore, changed, c.fetchStorageNow)
}

// storageSyncedWith is storageSynced with fetch for the storage service. If the sync changed an
// overridden user, it either undid our change (the phone hasn't written the storage service
// yet) or brought one made on the phone since. signalmeow doesn't tell which manifest version it
// synced, so fetch asks the storage service whether it still has the overrides' version.
func (c *meowClient) storageSyncedWith(
	ctx context.Context, recipients mstore.RecipientStore, changed []*types.Recipient, fetch storageFetcher,
) {
	overrides, err := c.data.BlockOverrides(ctx)
	if err != nil {
		c.log.Warn("re-apply blocked state", "error", err)

		return
	}

	changedACIs := make(map[string]bool, len(changed))

	for _, rcpt := range changed {
		if rcpt != nil && rcpt.ACI != uuid.Nil {
			changedACIs[rcpt.ACI.String()] = true
		}
	}

	affected := false
	since := uint64(math.MaxUint64)

	for _, override := range overrides {
		affected = affected || changedACIs[override.ACI]
		since = min(since, override.StorageVersion)
	}

	// The sync left the overridden users alone.
	if !affected {
		return
	}

	c.settleOverrides(ctx, recipients, c.storageSince(ctx, fetch, since))
}

// storageSince returns what the storage service has if its manifest version isn't since
// anymore, an empty snapshot at since if it still is, and nil if the fetch failed.
func (c *meowClient) storageSince(ctx context.Context, fetch storageFetcher, since uint64) *storageSnapshot {
	update, err := fetch(ctx, since)

	switch {
	case err != nil:
		c.log.Debug("check the storage service for block overrides", "error", err)

		return nil
	case update == nil:
		// The server answers "no content" while the manifest has the version asked about.
		return &storageSnapshot{version: since, list: newBlockedList()}
	default:
		return readStorage(update)
	}
}

// fetchStorageNow is fetchStorage with the current signalmeow client; Close cancels it.
func (c *meowClient) fetchStorageNow(ctx context.Context, since uint64) (*signalmeow.StorageUpdate, error) {
	c.cliMu.Lock()
	cli := c.cli
	c.cliMu.Unlock()

	if cli == nil {
		return nil, ErrNotConnected
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		select {
		case <-c.done:
			cancel()
		case <-ctx.Done():
		}
	}()

	return c.fetchStorage(ctx, cli, since)
}

// storageFetched settles the overrides after our own storage sync (see Sync) with what its fetch
// found (update; nil if the storage service has no manifest).
func (c *meowClient) storageFetched(ctx context.Context, update *signalmeow.StorageUpdate) {
	c.settleOverrides(ctx, c.connDevice.RecipientStore, readStorage(update))
}

// pendingOverrides returns the blocked state of the overrides that haven't expired, by ACI.
func (c *meowClient) pendingOverrides(ctx context.Context, now time.Time) (map[string]bool, error) {
	overrides, err := c.data.BlockOverrides(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store's errors say what failed
	}

	out := make(map[string]bool, len(overrides))

	for _, override := range overrides {
		if !expired(override, now) {
			out[override.ACI] = override.Blocked
		}
	}

	return out, nil
}

func expired(override store.BlockOverride, now time.Time) bool {
	return now.Sub(override.SetAt) > BlockOverrideTTL
}

// withOverride shows a pending block or unblock in contact.
func withOverride(contact Contact, pending map[string]bool) Contact {
	if blocked, ok := pending[contact.ACI]; ok && contact.ACI != "" {
		contact.Blocked = blocked
	}

	return contact
}

// recipientStore returns the device of the selected account (opening the store if needed) and
// its recipient store's loader.
func (c *meowClient) recipientStore(ctx context.Context) (*mstore.Device, recipientLoader, error) {
	device := c.connDevice
	if device == nil {
		var err error

		device, err = c.device(ctx)
		if err != nil {
			return nil, nil, err
		}
	}

	loader, ok := device.RecipientStore.(recipientLoader)
	if !ok {
		return nil, nil, errNoRecipientLoader
	}

	return device, loader, nil
}

// contactFrom converts signalmeow's recipient.
func contactFrom(rcpt *types.Recipient) Contact {
	out := Contact{
		Recipient:   Recipient{Number: rcpt.E164},
		ContactName: rcpt.ContactName,
		ProfileName: rcpt.Profile.Name,
		Nickname:    rcpt.Nickname,
		Blocked:     rcpt.Blocked,
	}

	if rcpt.ACI != uuid.Nil {
		out.ACI = rcpt.ACI.String()
	}

	if rcpt.PNI != uuid.Nil {
		out.PNI = rcpt.PNI.String()
	}

	if rcpt.Whitelisted != nil {
		out.Accepted = new(*rcpt.Whitelisted)
	}

	return out
}

// storageSnapshot is what one fetch of the storage service says about blocking.
type storageSnapshot struct {
	// version is the manifest version.
	version uint64
	// list has the blocked users and groups of the records read.
	list *blockedList
	// read has the users (by ACI) whose contact record was read.
	read map[uuid.UUID]bool
	// complete reports whether every record of the manifest was read: then a user without a
	// record is not blocked either.
	complete bool
	// problem says why list isn't the complete blocked list the phone has (nil if it is).
	problem error
}

// readStorage reads the blocked users and groups from the contact and group records of a fetch
// of the storage service; nil for a nil update (no manifest).
func readStorage(update *signalmeow.StorageUpdate) *storageSnapshot {
	if update == nil {
		return nil
	}

	seen := &storageSnapshot{
		version:  update.Version,
		list:     newBlockedList(),
		read:     make(map[uuid.UUID]bool),
		complete: len(update.MissingRecords) == 0,
	}

	if !seen.complete {
		seen.problem = fmt.Errorf("%w: %d records couldn't be read", ErrBlockedListIncomplete,
			len(update.MissingRecords))
	}

	for _, record := range update.NewRecords {
		var err error

		switch data := record.StorageRecord.GetRecord().(type) {
		case *signalpb.StorageRecord_Contact:
			aci, parseErr := signalmeow.ParseStringOrBinaryUUID(data.Contact.GetAci(), data.Contact.GetAciBinary())
			if parseErr == nil && aci != uuid.Nil {
				seen.read[aci] = true
			}

			err = seen.list.addContact(data.Contact)
		case *signalpb.StorageRecord_GroupV2:
			err = seen.list.addGroup(data.GroupV2)
		}

		if err != nil && seen.problem == nil {
			seen.problem = err
		}
	}

	return seen
}

// completeStorage is readStorage for a fetch that must hold the complete blocked list, as the
// phone replaces its own with the one we send: it fails with ErrBlockedListIncomplete for no
// manifest, records that couldn't be read, or blocked entries a sync message can't carry.
func completeStorage(update *signalmeow.StorageUpdate) (*storageSnapshot, error) {
	seen := readStorage(update)
	if seen == nil {
		return nil, fmt.Errorf("%w: it has no manifest yet", ErrBlockedListIncomplete)
	}

	if seen.problem != nil {
		return nil, seen.problem
	}

	return seen, nil
}

// supersedes reports whether s shows that the phone has written the storage service since
// override was made: s has a later manifest version. A nil s supersedes nothing.
func (s *storageSnapshot) supersedes(override store.BlockOverride) bool {
	return s != nil && override.StorageVersion < s.version
}

// state returns the blocked state s has for aci, and whether s knows it. A nil s knows nothing.
func (s *storageSnapshot) state(aci uuid.UUID) (bool, bool) {
	if s == nil {
		return false, false
	}

	if s.list.isBlocked(aci) {
		return true, true
	}

	return false, s.complete || s.read[aci]
}

// knows reports whether s knows the blocked state of the user aci (a string ACI).
func (s *storageSnapshot) knows(aci string) bool {
	parsed, err := uuid.Parse(aci)
	if err != nil {
		return false
	}

	_, known := s.state(parsed)

	return known
}

// blockedEntry is a blocked user: their number ("" if unknown) and when they were blocked
// (0 if unknown).
type blockedEntry struct {
	number string
	at     uint64
}

// blockedList is a complete blocked list as the phone keeps it: users by ACI, users known only
// by number, and groups (by raw group ID).
type blockedList struct {
	acis    map[uuid.UUID]blockedEntry
	numbers map[string]uint64
	groups  map[string]uint64
}

func newBlockedList() *blockedList {
	return &blockedList{
		acis:    make(map[uuid.UUID]blockedEntry),
		numbers: make(map[string]uint64),
		groups:  make(map[string]uint64),
	}
}

// addContact adds the user of a storage service contact record if it is blocked. A blocked user
// with neither an ACI nor a number can't be put in a sync message (ErrBlockedListIncomplete).
func (l *blockedList) addContact(contact *signalpb.ContactRecord) error {
	if !contact.GetBlocked() {
		return nil
	}

	aci, err := signalmeow.ParseStringOrBinaryUUID(contact.GetAci(), contact.GetAciBinary())

	switch {
	case err == nil && aci != uuid.Nil:
		l.acis[aci] = blockedEntry{number: contact.GetE164(), at: contact.GetBlockedAtTimestamp()}
	case contact.GetE164() != "":
		l.numbers[contact.GetE164()] = contact.GetBlockedAtTimestamp()
	default:
		return fmt.Errorf("%w: a blocked contact has neither an ACI nor a number", ErrBlockedListIncomplete)
	}

	return nil
}

// addGroup adds the group of a storage service group record if it is blocked. A blocked group
// without a valid master key has no group ID to send (ErrBlockedListIncomplete).
func (l *blockedList) addGroup(group *signalpb.GroupV2Record) error {
	if !group.GetBlocked() {
		return nil
	}

	if len(group.GetMasterKey()) != libsignalgo.GroupMasterKeyLength {
		return fmt.Errorf("%w: a blocked group has an invalid master key", ErrBlockedListIncomplete)
	}

	groupID, err := libsignalgo.GroupMasterKey(group.GetMasterKey()).GroupIdentifier()
	if err != nil {
		return fmt.Errorf("blocked group: %w", err)
	}

	l.groups[string(groupID[:])] = 0

	return nil
}

func (l *blockedList) clone() *blockedList {
	return &blockedList{acis: maps.Clone(l.acis), numbers: maps.Clone(l.numbers), groups: maps.Clone(l.groups)}
}

func (l *blockedList) isBlocked(aci uuid.UUID) bool {
	_, ok := l.acis[aci]

	return ok
}

// set blocks or unblocks the user aci (with their number, if known) at the time when.
func (l *blockedList) set(aci uuid.UUID, number string, blocked bool, when time.Time) {
	if !blocked {
		if entry, ok := l.acis[aci]; ok && entry.number != "" {
			delete(l.numbers, entry.number)
		}

		delete(l.acis, aci)

		if number != "" {
			delete(l.numbers, number)
		}

		return
	}

	entry, ok := l.acis[aci]
	if !ok {
		entry.at = uint64(when.UnixMilli()) //nolint:gosec // the clock is after 1970
	}

	if number != "" {
		entry.number = number
	}

	l.acis[aci] = entry
	delete(l.numbers, entry.number)
}

// syncMessage returns the list as a blocked-list sync message, sorted. It fills both the current
// fields (with the time of blocking, where known) and the deprecated ones that older clients
// read.
func (l *blockedList) syncMessage() *signalpb.SyncMessage {
	blocked := &signalpb.SyncMessage_Blocked{}

	byString := func(a, b uuid.UUID) int { return cmp.Compare(a.String(), b.String()) }

	for _, aci := range slices.SortedFunc(maps.Keys(l.acis), byString) {
		entry := l.acis[aci]
		blocked.Acis = append(blocked.Acis, aci.String())
		blocked.AcisBinary = append(blocked.AcisBinary, aci[:])
		blocked.BlockedAcis = append(blocked.BlockedAcis, &signalpb.SyncMessage_Blocked_BlockedAci{
			AciBinary: aci[:], Timestamp: optionalTimestamp(entry.at),
		})

		if entry.number != "" {
			blocked.Numbers = append(blocked.Numbers, entry.number)
			blocked.BlockedE164S = append(blocked.BlockedE164S, &signalpb.SyncMessage_Blocked_BlockedE164{
				E164: new(entry.number), Timestamp: optionalTimestamp(entry.at),
			})
		}
	}

	for _, number := range slices.Sorted(maps.Keys(l.numbers)) {
		blocked.Numbers = append(blocked.Numbers, number)
		blocked.BlockedE164S = append(blocked.BlockedE164S, &signalpb.SyncMessage_Blocked_BlockedE164{
			E164: new(number), Timestamp: optionalTimestamp(l.numbers[number]),
		})
	}

	for _, id := range slices.Sorted(maps.Keys(l.groups)) {
		blocked.GroupIds = append(blocked.GroupIds, []byte(id))
		blocked.BlockedGroups = append(blocked.BlockedGroups, &signalpb.SyncMessage_Blocked_BlockedGroup{
			GroupId: []byte(id), Timestamp: optionalTimestamp(l.groups[id]),
		})
	}

	return &signalpb.SyncMessage{Content: &signalpb.SyncMessage_Blocked_{Blocked: blocked}}
}

// optionalTimestamp returns nil for an unknown (zero) timestamp.
func optionalTimestamp(ms uint64) *uint64 {
	if ms == 0 {
		return nil
	}

	return new(ms)
}
