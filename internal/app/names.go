package app

import (
	"context"
	"fmt"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

// NameReloadInterval is how often a NameBook reloads the names at most.
const NameReloadInterval = 30 * time.Second

// shortACILen is how much of an ACI tells apart contacts with the same name.
const shortACILen = 8

// Names resolves users to the names the store knows (see signal.Contact.Name), for output. It
// is built from Client.Contacts and cheap to rebuild. The zero value knows no names.
type Names struct {
	own      string
	byACI    map[string]nameEntry
	byNumber map[string]nameEntry
	groups   map[string]string // group titles by ID
}

type nameEntry struct {
	// name is the contact's name ("" if they have none) and label what plain output shows.
	name, label string
}

// NewNames builds Names from contacts; ownACI is the account's own ACI (see IsSelf).
func NewNames(ownACI string, contacts []signal.Contact) Names {
	names := Names{
		own:      ownACI,
		byACI:    make(map[string]nameEntry, len(contacts)),
		byNumber: make(map[string]nameEntry, len(contacts)),
	}

	count := make(map[string]int, len(contacts))
	for _, contact := range contacts {
		count[contact.DisplayName()]++
	}

	for _, contact := range contacts {
		entry := nameEntry{name: contact.Name(), label: contact.DisplayName()}
		if count[entry.label] > 1 {
			entry.label += " (" + distinguish(contact) + ")"
		}

		if contact.ACI != "" {
			names.byACI[contact.ACI] = entry
		}

		if contact.Number != "" {
			names.byNumber[contact.Number] = entry
		}
	}

	return names
}

// distinguish returns what tells contact apart from others with the same display name.
func distinguish(contact signal.Contact) string {
	switch {
	case contact.Number != "" && contact.Number != contact.DisplayName():
		return contact.Number
	case len(contact.ACI) > shortACILen:
		return contact.ACI[:shortACILen]
	default:
		return contact.String()
	}
}

// WithGroups returns n with the group titles by group ID (see Client.GroupTitles).
func (n Names) WithGroups(titles map[string]string) Names {
	n.groups = titles

	return n
}

// GroupTitle returns the title of the group with the ID, or "" if it is unknown.
func (n Names) GroupTitle(groupID string) string {
	return n.groups[groupID]
}

// Names returns the names of the contacts and the titles of the groups in the store. It needs
// no connection.
func (a *App) Names(ctx context.Context) (Names, error) {
	acc, err := a.client.Account(ctx)
	if err != nil {
		return Names{}, fmt.Errorf("load names: %w", err)
	}

	contacts, err := a.client.Contacts(ctx)
	if err != nil {
		return Names{}, fmt.Errorf("load names: %w", err)
	}

	titles, err := a.client.GroupTitles(ctx)
	if err != nil {
		return Names{}, fmt.Errorf("load names: %w", err)
	}

	return NewNames(acc.ACI, contacts).WithGroups(titles), nil
}

// Name returns the name of rcpt: nickname, name in the phone's contacts or profile name; "" if
// there is none.
func (n Names) Name(rcpt signal.Recipient) string {
	entry := n.lookup(rcpt)

	return entry.name
}

// Label returns how plain output names rcpt: the name, or else the number, the ACI or the PNI
// the store has, with the number (or the start of the ACI) added when several contacts share it.
// It returns "" for users the store doesn't know.
func (n Names) Label(rcpt signal.Recipient) string {
	entry := n.lookup(rcpt)

	return entry.label
}

// IsSelf reports whether rcpt is the account itself.
func (n Names) IsSelf(rcpt signal.Recipient) bool {
	return n.own != "" && rcpt.ACI == n.own
}

// lookup finds rcpt by ACI, else by number; the zero entry if it is unknown.
func (n Names) lookup(rcpt signal.Recipient) nameEntry {
	if entry, ok := n.byACI[rcpt.ACI]; ok && rcpt.ACI != "" {
		return entry
	}

	if rcpt.Number != "" {
		return n.byNumber[rcpt.Number]
	}

	return nameEntry{}
}

// NameBook keeps Names up to date while events arrive (receive): profiles and contacts reach the
// store during a receive, too, so it reloads the names when an event names a user without one,
// at most once per NameReloadInterval.
type NameBook struct {
	app      *App
	names    Names
	loaded   time.Time
	interval time.Duration
}

// NameBook loads the names. When that fails, the book starts empty (with the error) and tries
// again later.
func (a *App) NameBook(ctx context.Context) (*NameBook, error) {
	book := &NameBook{app: a, interval: NameReloadInterval}

	_, err := book.reload(ctx)

	return book, err
}

// Names returns the current names.
func (b *NameBook) Names() Names {
	return b.names
}

// Refresh reloads the names if evt names a user (not us) without a name and the last load is
// at least NameReloadInterval ago. It reports whether it reloaded.
func (b *NameBook) Refresh(ctx context.Context, evt signal.Event) (bool, error) {
	if b.app.now().Sub(b.loaded) < b.interval {
		return false, nil
	}

	for _, rcpt := range EventRecipients(evt) {
		if rcpt.ACI != "" && !b.names.IsSelf(rcpt) && b.names.Name(rcpt) == "" {
			return b.reload(ctx)
		}
	}

	return false, nil
}

func (b *NameBook) reload(ctx context.Context) (bool, error) {
	b.loaded = b.app.now()

	names, err := b.app.Names(ctx)
	if err != nil {
		return false, err
	}

	b.names = names

	return true, nil
}

// EventRecipients returns the users evt names: sender, chat partner, quote or reaction author,
// the senders of read messages.
//
//nolint:cyclop // one case per event type
func EventRecipients(evt signal.Event) []signal.Recipient {
	envelope := func(env signal.Envelope, more ...signal.Recipient) []signal.Recipient {
		return append([]signal.Recipient{env.Sender, env.Chat.Recipient}, more...)
	}

	switch evt := evt.(type) {
	case *signal.Message:
		if evt.Quote != nil {
			return envelope(evt.Envelope, evt.Quote.Author)
		}

		return envelope(evt.Envelope)
	case *signal.Edit:
		return envelope(evt.Envelope)
	case *signal.Delete:
		return envelope(evt.Envelope)
	case *signal.Reaction:
		return envelope(evt.Envelope, evt.TargetAuthor)
	case *signal.Typing:
		return envelope(evt.Envelope)
	case *signal.Unsupported:
		return envelope(evt.Envelope)
	case *signal.Receipt:
		return []signal.Recipient{evt.Sender}
	case *signal.DecryptionFailure:
		return []signal.Recipient{evt.Sender}
	case *signal.ReadSync:
		out := make([]signal.Recipient, 0, len(evt.Messages))
		for _, mark := range evt.Messages {
			out = append(out, mark.Sender)
		}

		return out
	default:
		return nil
	}
}
