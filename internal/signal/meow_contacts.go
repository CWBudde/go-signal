//go:build cgo

package signal

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
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

// overrideSettleTimeout bounds re-applying block overrides outside of a command's context.
const overrideSettleTimeout = 5 * time.Second

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

func (c *meowClient) SetBlocked(ctx context.Context, recipients []Recipient, blocked bool) error {
	if c.cancelLoops == nil {
		return ErrNotConnected
	}

	acis := make([]uuid.UUID, 0, len(recipients))

	for _, rcpt := range recipients {
		id, err := aciServiceID(rcpt)
		if err != nil {
			return err
		}

		acis = append(acis, id.UUID)
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

	stored, err := c.storedBlockedList(ctx, cli)
	if err != nil {
		return err
	}

	// Earlier overrides count, too: the phone may not have stored them yet.
	c.overridesMu.Lock()
	defer c.overridesMu.Unlock()

	now := time.Now()

	list, err := c.pendingBlockedList(ctx, stored, now)
	if err != nil {
		return err
	}

	for i, aci := range acis {
		list.set(aci, c.knownNumber(ctx, aci, recipients[i].Number), blocked, now)
	}

	err = sendSyncMessage(ctx, cli, list.syncMessage())
	if err != nil {
		return fmt.Errorf("send blocked list to our other devices: %w", err)
	}

	return c.storeBlocked(ctx, stored, acis, blocked, now)
}

// storedBlockedList reads the current blocked list from the storage service.
func (c *meowClient) storedBlockedList(ctx context.Context, cli *signalmeow.Client) (*blockedList, error) {
	key, err := c.storedMasterKey(ctx)
	if err != nil {
		return nil, err
	}

	if key == nil {
		return nil, ErrStorageKeyUnknown
	}

	update, err := cli.FetchStorage(ctx, key, 0, nil)
	if err != nil {
		return nil, fmt.Errorf("read the blocked list from the storage service: %w", err)
	}

	return blockedFromStorage(update)
}

// pendingBlockedList returns stored with the pending overrides applied; the caller holds
// overridesMu.
func (c *meowClient) pendingBlockedList(ctx context.Context, stored *blockedList, now time.Time,
) (*blockedList, error) {
	list := stored.clone()

	overrides, err := c.data.BlockOverrides(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store's errors say what failed
	}

	for _, override := range overrides {
		aci, err := uuid.Parse(override.ACI)
		if err != nil || expired(override, now) || list.isBlocked(aci) == override.Blocked {
			continue
		}

		list.set(aci, c.knownNumber(ctx, aci, ""), override.Blocked, override.SetAt)
	}

	return list, nil
}

// storeBlocked records the change in the store once the blocked list went out: an override
// where the storage service (stored) says otherwise, and the new state of each user. Then it
// settles the other overrides against stored. The caller holds overridesMu.
func (c *meowClient) storeBlocked(
	ctx context.Context, stored *blockedList, acis []uuid.UUID, blocked bool, now time.Time,
) error {
	for _, aci := range acis {
		var err error

		if stored.isBlocked(aci) == blocked {
			err = c.data.DeleteBlockOverride(ctx, aci.String())
		} else {
			err = c.data.SetBlockOverride(ctx, store.BlockOverride{ACI: aci.String(), Blocked: blocked, SetAt: now})
		}

		if err != nil {
			return fmt.Errorf("blocked list sent, but: %w", err)
		}

		err = setBlocked(ctx, c.connDevice.RecipientStore, aci, blocked)
		if err != nil {
			return fmt.Errorf("blocked list sent, but: %w", err)
		}
	}

	c.settleOverridesLocked(ctx, c.connDevice.RecipientStore, stored.state, now)

	return nil
}

// knownNumber returns number, or else the number the store has for aci ("" if none).
func (c *meowClient) knownNumber(ctx context.Context, aci uuid.UUID, number string) string {
	if number != "" {
		return number
	}

	loader, ok := c.connDevice.RecipientStore.(recipientLoader)
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

// storageState reports the blocked state the storage service has for an ACI, and whether it is
// known.
type storageState func(aci string) (blocked, known bool)

// settleOverrides re-applies the pending block overrides to the store, and drops those that
// expired (BlockOverrideTTL) or that the storage service agrees with (per storage; nil if
// unknown). Failures are only logged: the next settle tries again.
func (c *meowClient) settleOverrides(ctx context.Context, recipients mstore.RecipientStore, storage storageState) {
	c.overridesMu.Lock()
	defer c.overridesMu.Unlock()

	c.settleOverridesLocked(ctx, recipients, storage, time.Now())
}

// settleOverridesLocked is settleOverrides; the caller holds overridesMu.
func (c *meowClient) settleOverridesLocked(
	ctx context.Context, recipients mstore.RecipientStore, storage storageState, now time.Time,
) {
	overrides, err := c.data.BlockOverrides(ctx)
	if err != nil {
		c.log.Warn("re-apply blocked state", "error", err)

		return
	}

	for _, override := range overrides {
		agrees := false

		if storage != nil {
			stored, known := storage(override.ACI)
			agrees = known && stored == override.Blocked
		}

		if agrees || expired(override, now) {
			c.log.Debug("dropping block override", "aci", override.ACI, "storage agrees", agrees)

			err = c.data.DeleteBlockOverride(ctx, override.ACI)
			if err != nil {
				c.log.Warn("drop block override", "aci", override.ACI, "error", err)
			}

			continue
		}

		aci, err := uuid.Parse(override.ACI)
		if err != nil {
			continue
		}

		err = setBlocked(ctx, recipients, aci, override.Blocked)
		if err != nil {
			c.log.Warn("re-apply blocked state", "aci", override.ACI, "error", err)
		}
	}
}

// storageSynced settles the overrides after signalmeow stored contacts from the storage service
// (a ContactList event with IsFromDB): changed has the users whose record changed, with the
// blocked state the storage service has.
func (c *meowClient) storageSynced(ctx context.Context, device *mstore.Device, changed []*types.Recipient) {
	if device == nil {
		return
	}

	states := make(map[string]bool, len(changed))

	for _, rcpt := range changed {
		if rcpt != nil && rcpt.ACI != uuid.Nil {
			states[rcpt.ACI.String()] = rcpt.Blocked
		}
	}

	c.settleOverrides(ctx, device.RecipientStore, func(aci string) (bool, bool) {
		blocked, known := states[aci]

		return blocked, known
	})
}

// storageFetched settles the overrides after a storage sync with the records it fetched.
func (c *meowClient) storageFetched(ctx context.Context, update *signalmeow.StorageUpdate) {
	list, err := blockedFromStorage(update)
	if err != nil {
		c.log.Debug("read blocked list from storage update", "error", err)

		list = nil
	}

	var storage storageState
	if list != nil {
		storage = list.state
	}

	c.settleOverrides(ctx, c.connDevice.RecipientStore, storage)
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

// blockedFromStorage reads the blocked users and groups from the storage service's contact and
// group records. A nil update (no manifest) is an empty list.
func blockedFromStorage(update *signalmeow.StorageUpdate) (*blockedList, error) {
	list := newBlockedList()
	if update == nil {
		return list, nil
	}

	for _, record := range update.NewRecords {
		switch data := record.StorageRecord.GetRecord().(type) {
		case *signalpb.StorageRecord_Contact:
			list.addContact(data.Contact)
		case *signalpb.StorageRecord_GroupV2:
			err := list.addGroup(data.GroupV2)
			if err != nil {
				return nil, err
			}
		}
	}

	return list, nil
}

// addContact adds the user of a storage service contact record if it is blocked.
func (l *blockedList) addContact(contact *signalpb.ContactRecord) {
	if !contact.GetBlocked() {
		return
	}

	aci, err := signalmeow.ParseStringOrBinaryUUID(contact.GetAci(), contact.GetAciBinary())

	switch {
	case err == nil && aci != uuid.Nil:
		l.acis[aci] = blockedEntry{number: contact.GetE164(), at: contact.GetBlockedAtTimestamp()}
	case contact.GetE164() != "":
		l.numbers[contact.GetE164()] = contact.GetBlockedAtTimestamp()
	}
}

// addGroup adds the group of a storage service group record if it is blocked.
func (l *blockedList) addGroup(group *signalpb.GroupV2Record) error {
	if !group.GetBlocked() || len(group.GetMasterKey()) != libsignalgo.GroupMasterKeyLength {
		return nil
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

// state is a storageState: the list is complete, so every ACI is known.
func (l *blockedList) state(aci string) (bool, bool) {
	id, err := uuid.Parse(aci)
	if err != nil {
		return false, false
	}

	return l.isBlocked(id), true
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
