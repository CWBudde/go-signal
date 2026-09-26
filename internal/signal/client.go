package signal

import (
	"context"
	"log/slog"
	"time"
)

// Client is the facade that cmd/ (and later internal/app) talks to. The signalmeow-backed
// implementation needs cgo; signaltest.Fake implements it for tests without cgo.
//
// A Client is used by a single command: Open it, then either Link a new account or Connect an
// existing one, read Events, and Close it.
type Client interface { //nolint:interfacebloat // the one facade over signalmeow; tests fake it whole
	// Link provisions a new secondary device. It calls onURI with the sgnl://linkdevice URI to
	// show to the user, then blocks until the phone has scanned it and the account is stored.
	Link(ctx context.Context, deviceName string, onURI func(uri string)) (Account, error)

	// Account returns the selected linked account without connecting. It fails with
	// ErrNotLinked when there is none. An account marked as unlinked is still returned (with
	// UnlinkedAt set).
	Account(ctx context.Context) (Account, error)

	// CheckLock reports whether the selected account could be connected as far as the account
	// lock goes, without taking it: it fails with ErrAccountInUse while another process holds
	// the lock (e.g. a running `mcp serve`). A client that holds the lock itself passes.
	CheckLock(ctx context.Context) error

	// Connect starts receiving for the selected account. Events are delivered on Events until
	// Close. Connection changes arrive there as *Connection events; the client reconnects on its
	// own and reports StateFailed once it gives up. The connection outlives ctx (it only bounds
	// the setup), so that Close can shut it down gracefully. When the server logs the
	// device out (it was unlinked on the phone), the account is marked as unlinked and a
	// StateLoggedOut event carries UnlinkedError. On an account already marked, Connect fails
	// with it right away, without contacting the server. A second Connect fails with
	// ErrAlreadyConnected.
	//
	// With the SendOnly option, Connect is for commands that only send: incoming messages are
	// not handed out on Events but left on the server (not acked), so the next receive gets
	// them. See SendOnly.
	Connect(ctx context.Context, opts ...ConnectOption) error

	// Events returns the channel of incoming events. It is unbuffered and closed by Close. An
	// event counts as handled (and is acked to the server) once it has been read from the
	// channel; events not read before Close are delivered again next time.
	Events() <-chan Event

	// Download fetches an attachment of a received message from Signal's CDN, verifies its
	// digest and MAC, and returns the decrypted content. The CDN needs no authentication, so
	// Download needs neither Connect nor the account lock. It fails with ErrAttachmentNotFound
	// when the CDN doesn't have the attachment (any more) and with ErrAttachmentInvalid when it
	// fails verification; nothing unverified is returned. The whole content is held in memory.
	Download(ctx context.Context, att Attachment) ([]byte, error)

	// Resolve returns recipients with their ACI filled in, in the same order. Recipients that
	// already have one are returned as they are. A number is looked up in the store first and
	// otherwise through contact discovery, which needs Connect (ErrNotConnected); the result is
	// cached in the store. A username (nickname.discriminator) is looked up by its hash and needs
	// no connection. Each recipient that has no Signal account fails with ErrNotOnSignal; the
	// errors of all recipients are joined.
	Resolve(ctx context.Context, recipients []Recipient) ([]Recipient, error)

	// Upload encrypts the attachments and uploads them to Signal's CDN, so that Send can refer to
	// them; an attachment uploaded once can go to any number of recipients and groups. It needs
	// Connect (ErrNotConnected) and fails with ErrClosed after Close.
	Upload(ctx context.Context, attachments []OutgoingAttachment) ([]UploadedAttachment, error)

	// Send sends a message to req.Recipients, which need their ACI (see Resolve), or to the
	// group req.GroupID, with the sent timestamp req.Timestamp (zero means now). A recipient with
	// our own ACI gets a note-to-self: only a sync transcript to our other devices. For every
	// other recipient our other devices get a sync transcript too. It needs Connect
	// (ErrNotConnected) and fails with ErrClosed after Close. Failures of single recipients (or
	// group members) are reported in the result; an error means that nothing was sent, e.g.
	// because the group is unknown (ErrUnknownGroup), an attachment wasn't uploaded by this client
	// (ErrUnknownAttachment), the quote author or a mentioned user has no ACI (ErrUnresolvable),
	// or the connection is lost for good (such as ErrDeviceUnlinked).
	Send(ctx context.Context, req SendRequest) (SendResult, error)

	// SendReceipt tells sender that we received (ReceiptDelivery), read (ReceiptRead) or viewed
	// (ReceiptViewed) their messages with the given sent timestamps. sender needs their ACI. A
	// read receipt also reaches our other devices (as a read sync). It needs Connect
	// (ErrNotConnected) and fails with ErrClosed after Close, and with ErrInvalidReceipt for an
	// unknown type or no timestamps.
	SendReceipt(ctx context.Context, sender Recipient, typ ReceiptType, timestamps []uint64) error

	// Sync fetches the account's contacts and groups from the phone and the storage service
	// into the store, as right after Link: it asks the phone for its contact list, makes sure
	// the storage service key is known (asking the phone for it if not), fetches the storage
	// service (contacts with names, numbers, profile keys and blocked state; group master keys;
	// the account record), and waits for the contact list. It needs Connect (ErrNotConnected;
	// SendOnly is enough) and fails with ErrClosed after Close.
	//
	// ctx bounds the whole sync. When it ends first, or a part fails (e.g. the storage service),
	// Sync returns the result so far with an error wrapping ErrSyncIncomplete and the cause;
	// what did arrive is stored. Other errors (such as a connection lost for good, e.g.
	// ErrDeviceUnlinked) mean that nothing was synced. opts.Progress reports the stages.
	Sync(ctx context.Context, opts SyncOptions) (SyncResult, error)

	// Devices lists all devices of the selected account as the server knows them. It needs
	// neither Connect nor the account lock. Like Connect, it fails with ErrDeviceUnlinked on an
	// account marked as unlinked, and marks the account when the server rejects the device.
	Devices(ctx context.Context) ([]Device, error)

	// Unlink removes this device from the selected account on the server (unless
	// opts.LocalOnly) and then deletes the account's local data. It takes the account lock, so
	// it fails with ErrAccountInUse while another process is connected. A device the server
	// already logged out (ErrDeviceUnlinked) counts as removed, and an account marked as unlinked
	// skips the server as with LocalOnly. It returns the removed account as recorded in
	// accounts.json.
	Unlink(ctx context.Context, opts UnlinkOptions) (Account, error)

	// Close shuts down gracefully: it waits for in-flight sends (later ones and every other
	// method that fails with ErrClosed are refused), makes sure the acks of events read from
	// Events reach the server, disconnects and releases the store. Sends get a few seconds before
	// it disconnects; the store is only released once every running method has returned.
	Close() error

	// Contacts returns the users the store knows with a name or number, and those we blocked,
	// without the account itself, in no particular order. It reads only the store (filled by
	// Sync, the storage service and received messages) and needs no Connect. A pending block or
	// unblock made with SetBlocked shows even while the store is still being overridden by
	// the storage service (see BlockOverrideTTL). It fails with ErrClosed after Close.
	Contacts(ctx context.Context) ([]Contact, error)

	// Contact returns what the store knows about one user, looked up by ACI, else PNI, else
	// number (a username alone is not stored; Resolve it first). Our own account can be looked
	// up, too. It reads only the store and needs no Connect. A user the store doesn't know fails
	// with ErrUnknownContact.
	Contact(ctx context.Context, rcpt Recipient) (Contact, error)

	// SetBlocked blocks (or unblocks) the recipients, which need their ACI (see Resolve;
	// ErrUnresolvable otherwise). It reads the current blocked list (users and groups) from the
	// storage service (ErrStorageKeyUnknown if its key is unknown), applies the change and sends
	// the complete list to our other devices as a blocked-list sync message: the phone replaces
	// its list with it and updates the storage service itself. So the list must be complete: if
	// the storage service has no manifest, records couldn't be read, or a blocked entry can't be
	// put in the message, it fails with ErrBlockedListIncomplete. Once the list went out, the
	// store is updated and the change is kept against storage syncs that still say otherwise
	// until the storage service has changed since (the phone wrote it, so its state wins), or for
	// BlockOverrideTTL. It needs Connect (ErrNotConnected; SendOnly is enough) and fails with
	// ErrClosed after Close. An error such as a sync message that didn't go out or a connection
	// lost for good means that nothing changed; only an error from the store after the list went
	// out (it says so) leaves the store behind the phone.
	SetBlocked(ctx context.Context, recipients []Recipient, blocked bool) error

	// Identities lists the identity keys stored for other users, by ACI, with their trust level
	// (see TrustLevel). With rcpt (which needs its ACI, see Resolve) only that user's key is
	// listed, or none. It works on the local store, without Connect or the account lock.
	//
	// Trust is on first use: the first key seen for a user is trusted without verification. A
	// key that changes later, while receiving from or sending to them, is untrusted: go-signal
	// logs a warning, reports an *IdentityChanged event on Events, and fails every send to them
	// with ErrUntrustedIdentity (see UntrustedError) until TrustIdentity. Receiving keeps working.
	Identities(ctx context.Context, rcpt *Recipient) ([]Identity, error)

	// SafetyNumber returns the safety number of our account and rcpt (which needs its ACI) for
	// their current identity key, as the Signal apps show it, with the key's Identity. It fails
	// with ErrUnknownIdentity when no key is stored for them. Like Identities it only reads the
	// local store.
	SafetyNumber(ctx context.Context, rcpt Recipient) (SafetyNumber, error)

	// TrustIdentity trusts rcpt's current identity key, so that sending to them works again after
	// a change. Without safetyNumber the key becomes TrustUnverified (a key that is already
	// verified stays so); with it, the key becomes TrustVerified if safetyNumber (60 digits, white
	// space ignored) is the current one, and otherwise nothing changes and it fails with
	// ErrSafetyNumberMismatch (or ErrInvalidSafetyNumber). It fails with ErrUnknownIdentity when
	// no key is stored for them. It works on the local store, also while another process is
	// connected; that process sees the new trust level with its next send.
	TrustIdentity(ctx context.Context, rcpt Recipient, safetyNumber string) (Identity, error)

	// Groups fetches the state of every group whose master key the store holds (from a sync or
	// a group message) from the server, sorted by title (see SortGroups). A group the server
	// doesn't show us (ErrNotAMember, e.g. we left or were removed) or doesn't know
	// (ErrUnknownGroup) is still listed, with Group.Err set and its last known title; any other
	// failure fails the whole list. Every group fetched updates the title cache (see
	// GroupTitles). It needs Connect (ErrNotConnected; SendOnly is enough) and fails with
	// ErrClosed after Close, and with the error of a connection lost for good (such as
	// ErrDeviceUnlinked).
	Groups(ctx context.Context) ([]Group, error)

	// Group fetches the state of one group from the server, like Groups. ref is the group's ID
	// or its master key, both 32 bytes in standard base64: it is looked up as an ID first, then
	// as a master key. A group whose master key the store doesn't hold fails with
	// ErrUnknownGroup; one the server doesn't show us with ErrNotAMember.
	Group(ctx context.Context, ref string) (Group, error)

	// LeaveGroup leaves the group ref (as for Group): it removes us as a member, declines an
	// invitation or cancels a join request, and tells the other members. See Group.CheckLeave
	// for when that is refused (ErrNotAMember, ErrLastAdmin, ErrInvalidPromotion); opts.Promote
	// makes members admins in the same change. The group's master key stays in the store, and
	// the title cache remembers that we left (Group.LeftAt). A change that conflicts with one made
	// meanwhile fails with ErrGroupChanged. It needs Connect like Group.
	LeaveGroup(ctx context.Context, ref string, opts LeaveOptions) (LeaveResult, error)

	// GroupTitles returns what the title cache knows about the groups fetched before (by Groups,
	// Group or LeaveGroup), by group ID, from the store: it needs no Connect. Groups never fetched
	// are missing. It fails with ErrClosed after Close.
	GroupTitles(ctx context.Context) (map[string]CachedGroup, error)

	// InboxAdd stores entry in the account's inbox, where `mcp serve` keeps the events it
	// received, and returns it with its new ID. Every event type but *Connection and *QueueEmpty
	// can be stored (ErrNotStorable). Like the other inbox methods, it works on the local store
	// and needs no Connect; it fails with ErrClosed after Close.
	InboxAdd(ctx context.Context, entry InboxEntry) (InboxEntry, error)

	// InboxList returns the inbox entries that q selects, sorted by ID.
	InboxList(ctx context.Context, q InboxQuery) ([]InboxEntry, error)

	// InboxChats summarizes the inbox by chat, newest chat first; entries without a chat are left
	// out.
	InboxChats(ctx context.Context) ([]InboxChat, error)

	// InboxMarkRead marks the unread entries of the messages with these senders (by ACI) and
	// timestamps as read and returns how many there were.
	InboxMarkRead(ctx context.Context, marks []ReadMark) (int, error)

	// InboxPrune deletes the entries received earlier than before (unless it is zero) and all
	// but the newest keep entries (unless keep is zero), and returns how many it deleted.
	InboxPrune(ctx context.Context, before time.Time, keep int) (int, error)
}

// Options configures a Client.
type Options struct {
	// DataDir holds the account store.
	DataDir string
	// Account selects the account by number or ACI; empty selects the first one.
	Account string
	// Logger receives the client's (and signalmeow's) logs; nil means slog.Default().
	Logger *slog.Logger
}

// Factory opens a Client. The root command takes one so that tests can inject a fake.
type Factory func(ctx context.Context, opts Options) (Client, error)

func (o Options) logger() *slog.Logger {
	if o.Logger != nil {
		return o.Logger
	}

	return slog.Default()
}

// ConnectOptions are the settings a ConnectOption changes.
type ConnectOptions struct {
	// SendOnly is set by the SendOnly option.
	SendOnly bool
}

// ConnectOption configures Connect.
type ConnectOption func(*ConnectOptions)

// SendOnly connects without receiving, for commands that only send.
//
// signalmeow hands every incoming envelope to our handler on the websocket's request loop and
// only acks it once the handler returns. A command that never reads Events would block that
// loop; after 256 queued requests the websocket stalls, and with it the responses to our own
// requests (sends, contact discovery). In send-only mode the handler returns right away without
// acking, so the server keeps the envelope and delivers it again on the next connection (the
// decrypted content stays in signalmeow's event buffer until then). Nothing is lost and Events
// stays silent until Close closes it. A connection lost for good (logged out, or reconnecting
// gave up) is not reported on Events either; the next Send fails with its error instead.
func SendOnly() ConnectOption {
	return func(o *ConnectOptions) {
		o.SendOnly = true
	}
}

// NewConnectOptions applies opts; Client implementations call it in Connect.
func NewConnectOptions(opts ...ConnectOption) ConnectOptions {
	var out ConnectOptions
	for _, opt := range opts {
		opt(&out)
	}

	return out
}
