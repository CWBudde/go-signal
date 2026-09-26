//go:build cgo

package store

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	"go.mau.fi/util/dbutil"
)

// InboxRecord is one entry of the inbox of `mcp serve` (gosignal_inbox).
type InboxRecord struct {
	// ID is assigned by AddInboxRecord.
	ID         int64
	ReceivedAt time.Time
	Time       time.Time
	// Chat is the chat's key; Sender (ACI) and Timestamp identify a message for read marks.
	Chat      string
	Sender    string
	Timestamp uint64
	Unread    bool
	// Event is the encoded event.
	Event []byte
}

// InboxFilter selects inbox records; zero fields don't filter (see signal.InboxQuery).
type InboxFilter struct {
	After, Until int64
	Chat         string
	Since        time.Time
	Unread       bool
	Limit        int
	Newest       bool
}

// InboxChatRecord summarizes the records of one chat.
type InboxChatRecord struct {
	Chat    string
	Entries int
	Unread  int
	Last    InboxRecord
}

// InboxMark identifies a message by its sender's ACI and its timestamp.
type InboxMark struct {
	Sender    string
	Timestamp uint64
}

const inboxColumns = "id, received_at, time, chat, sender, timestamp, unread, event"

// AddInboxRecord stores rec and returns its new ID.
func (s *Store) AddInboxRecord(ctx context.Context, rec InboxRecord) (int64, error) {
	var newID int64

	err := s.own.QueryRow(ctx, `
		INSERT INTO gosignal_inbox (received_at, time, chat, sender, timestamp, unread, event)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		rec.ReceivedAt.UnixMilli(), rec.Time.UnixMilli(), rec.Chat, rec.Sender, timestampArg(rec.Timestamp),
		rec.Unread, string(rec.Event)).Scan(&newID)
	if err != nil {
		return 0, fmt.Errorf("write inbox entry: %w", err)
	}

	return newID, nil
}

// InboxRecords returns the records that filter selects, sorted by ID.
func (s *Store) InboxRecords(ctx context.Context, filter InboxFilter) ([]InboxRecord, error) {
	where, args := filter.where()

	query := "SELECT " + inboxColumns + " FROM gosignal_inbox"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	order := "ASC"
	if filter.Newest {
		order = "DESC"
	}

	query += " ORDER BY id " + order

	if filter.Limit > 0 {
		args = append(args, filter.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}

	rows, err := s.own.Query(ctx, query, args...)

	recs, err := dbutil.NewRowIterWithError(rows, scanInboxRecord, err).AsList()
	if err != nil {
		return nil, fmt.Errorf("read inbox: %w", err)
	}

	if filter.Newest {
		for i, j := 0, len(recs)-1; i < j; i, j = i+1, j-1 {
			recs[i], recs[j] = recs[j], recs[i]
		}
	}

	return recs, nil
}

// where returns the conditions of filter's fields with their arguments ($1, $2, …).
func (f InboxFilter) where() ([]string, []any) {
	var (
		where []string
		args  []any
	)

	add := func(cond string, arg any) {
		args = append(args, arg)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}

	if f.After > 0 {
		add("id > $%d", f.After)
	}

	if f.Until > 0 {
		add("id <= $%d", f.Until)
	}

	if f.Chat != "" {
		add("chat = $%d", f.Chat)
	}

	if !f.Since.IsZero() {
		add("time >= $%d", f.Since.UnixMilli())
	}

	if f.Unread {
		add("unread = $%d", true)
	}

	return where, args
}

// InboxChats summarizes the records by chat, the chat with the newest record first; records
// without a chat are left out.
func (s *Store) InboxChats(ctx context.Context) ([]InboxChatRecord, error) {
	rows, err := s.own.Query(ctx, `
		SELECT chats.chat, chats.entries, chats.unread, `+prefixed("last.", inboxColumns)+`
		FROM (
			SELECT chat, COUNT(*) AS entries, SUM(unread) AS unread, MAX(id) AS last_id
			FROM gosignal_inbox WHERE chat != '' GROUP BY chat
		) AS chats JOIN gosignal_inbox AS last ON last.id = chats.last_id
		ORDER BY chats.last_id DESC`)

	chats, err := dbutil.NewRowIterWithError(rows, scanInboxChat, err).AsList()
	if err != nil {
		return nil, fmt.Errorf("read inbox chats: %w", err)
	}

	return chats, nil
}

// MarkInboxRead marks the unread records of the messages marks identifies as read and returns
// how many there were.
func (s *Store) MarkInboxRead(ctx context.Context, marks []InboxMark) (int, error) {
	marked := 0

	err := s.own.DoTxn(ctx, nil, func(ctx context.Context) error {
		for _, mark := range marks {
			res, err := s.own.Exec(ctx,
				"UPDATE gosignal_inbox SET unread=false WHERE unread AND sender=$1 AND timestamp=$2",
				mark.Sender, timestampArg(mark.Timestamp))
			if err != nil {
				return err //nolint:wrapcheck // wrapped below
			}

			n, err := res.RowsAffected()
			if err != nil {
				return err //nolint:wrapcheck // wrapped below
			}

			marked += int(n)
		}

		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("mark inbox entries read: %w", err)
	}

	return marked, nil
}

// PruneInbox deletes the records received earlier than before (unless it is zero) and all but
// the newest keep records (unless keep is zero), and returns how many it deleted.
func (s *Store) PruneInbox(ctx context.Context, before time.Time, keep int) (int, error) {
	deleted := int64(0)

	if !before.IsZero() {
		res, err := s.own.Exec(ctx, "DELETE FROM gosignal_inbox WHERE received_at < $1", before.UnixMilli())
		if err != nil {
			return 0, fmt.Errorf("prune inbox: %w", err)
		}

		n, _ := res.RowsAffected()
		deleted += n
	}

	if keep > 0 {
		res, err := s.own.Exec(ctx, `DELETE FROM gosignal_inbox WHERE id <= (
			SELECT id FROM gosignal_inbox ORDER BY id DESC LIMIT 1 OFFSET $1)`, keep)
		if err != nil {
			return 0, fmt.Errorf("prune inbox: %w", err)
		}

		n, _ := res.RowsAffected()
		deleted += n
	}

	return int(deleted), nil
}

func scanInboxRecord(row dbutil.Scannable) (InboxRecord, error) {
	var (
		rec                      InboxRecord
		received, when, sentTime int64
		event                    string
	)

	err := row.Scan(&rec.ID, &received, &when, &rec.Chat, &rec.Sender, &sentTime, &rec.Unread, &event)
	if err != nil {
		return InboxRecord{}, err //nolint:wrapcheck // callers wrap it
	}

	rec.ReceivedAt = time.UnixMilli(received).UTC()
	rec.Time = time.UnixMilli(when).UTC()
	rec.Timestamp = uint64(max(sentTime, 0))
	rec.Event = []byte(event)

	return rec, nil
}

func scanInboxChat(row dbutil.Scannable) (InboxChatRecord, error) {
	var (
		chat                     InboxChatRecord
		unread                   sql.NullInt64
		received, when, sentTime int64
		event                    string
	)

	err := row.Scan(&chat.Chat, &chat.Entries, &unread,
		&chat.Last.ID, &received, &when, &chat.Last.Chat, &chat.Last.Sender, &sentTime, &chat.Last.Unread, &event)
	if err != nil {
		return InboxChatRecord{}, err //nolint:wrapcheck // callers wrap it
	}

	chat.Unread = int(unread.Int64)
	chat.Last.ReceivedAt = time.UnixMilli(received).UTC()
	chat.Last.Time = time.UnixMilli(when).UTC()
	chat.Last.Timestamp = uint64(max(sentTime, 0))
	chat.Last.Event = []byte(event)

	return chat, nil
}

// timestampArg converts a Signal timestamp for SQLite, whose integers are signed.
func timestampArg(ts uint64) int64 {
	return int64(min(ts, math.MaxInt64))
}

// prefixed qualifies every column in the comma-separated columns with prefix.
func prefixed(prefix, columns string) string {
	parts := strings.Split(columns, ", ")
	for i, part := range parts {
		parts[i] = prefix + part
	}

	return strings.Join(parts, ", ")
}
