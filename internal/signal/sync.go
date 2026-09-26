package signal

import "errors"

// ErrSyncIncomplete means that Sync ended before everything arrived, e.g. because its context
// ran out while the phone hadn't answered yet or the storage service failed. What did arrive is
// stored; the error also wraps the cause (such as context.DeadlineExceeded).
var ErrSyncIncomplete = errors.New("sync incomplete")

// ErrStorageNotStored means that the storage service was fetched, but the store lacks some of its
// contacts or groups afterwards: signalmeow's storage sync, which only logs its failures, didn't
// store them. Sync then reports the storage service as not synced.
var ErrStorageNotStored = errors.New("storage service records not stored")

// SyncStage is a step of Client.Sync, reported through SyncOptions.Progress.
type SyncStage int

// The stages of Sync, in the order they are reported. A stage that isn't needed is skipped.
const (
	// SyncRequestingContacts: asking the phone for its contact list.
	SyncRequestingContacts SyncStage = iota + 1
	// SyncWaitingForKey: the storage service key is unknown; asking the phone for it and
	// waiting.
	SyncWaitingForKey
	// SyncFetchingStorage: fetching contacts, groups and the blocked list from the storage
	// service.
	SyncFetchingStorage
	// SyncWaitingForContacts: waiting for the phone's contact list.
	SyncWaitingForContacts
	// SyncDone: finished (completely or not); the counts are final.
	SyncDone
)

// String describes the stage for progress output, e.g. "requesting contacts from the phone".
func (s SyncStage) String() string {
	switch s {
	case SyncRequestingContacts:
		return "requesting contacts from the phone"
	case SyncWaitingForKey:
		return "waiting for the storage key from the phone"
	case SyncFetchingStorage:
		return "fetching contacts and groups from the storage service"
	case SyncWaitingForContacts:
		return "waiting for the contact list from the phone"
	case SyncDone:
		return "done"
	default:
		return "unknown sync stage"
	}
}

// SyncOptions configures Client.Sync.
type SyncOptions struct {
	// Progress, if set, is called at the start of every stage, on the goroutine that called
	// Sync.
	Progress func(SyncStage)
}

// SyncResult is what Client.Sync achieved. The counts are what the store holds afterwards,
// including what earlier syncs and received messages stored.
type SyncResult struct {
	// Contacts is the number of known users with a name or number, not counting ourselves.
	Contacts int
	// Groups is the number of groups whose master key we have.
	Groups int
	// MasterKey reports whether the storage service key (derived from the phone's account
	// entropy pool) is known.
	MasterKey bool
	// Storage reports whether the storage service was fetched and stored.
	Storage bool
	// ContactList reports whether the phone's contact list arrived and was stored.
	ContactList bool
}

// Complete reports whether every part of the sync succeeded.
func (r SyncResult) Complete() bool {
	return r.MasterKey && r.Storage && r.ContactList
}

// Missing names the parts that didn't arrive, e.g. "contact list"; empty when Complete.
func (r SyncResult) Missing() []string {
	var missing []string

	if !r.MasterKey {
		missing = append(missing, "storage key")
	}

	if !r.Storage {
		missing = append(missing, "storage service")
	}

	if !r.ContactList {
		missing = append(missing, "contact list")
	}

	return missing
}

// Report calls o.Progress with stage if it is set; Client implementations use it.
func (o SyncOptions) Report(stage SyncStage) {
	if o.Progress != nil {
		o.Progress(stage)
	}
}
