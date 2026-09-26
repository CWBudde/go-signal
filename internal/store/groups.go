//go:build cgo

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"go.mau.fi/util/dbutil"
)

// GroupRecord is what go-signal remembers about a group between fetches (gosignal_groups): its
// title for offline name resolution, and whether we left it.
type GroupRecord struct {
	// ID is the base64 group identifier.
	ID       string
	Title    string
	Revision uint32
	// LeftAt is when we left the group; zero if we didn't (or rejoined).
	LeftAt time.Time
	// UpdatedAt is when the record was last written.
	UpdatedAt time.Time
}

const groupColumns = "group_id, title, revision, left_at, updated_at"

// PutGroup stores rec, replacing the record of the same group.
func (s *Store) PutGroup(ctx context.Context, rec GroupRecord) error {
	_, err := s.own.Exec(ctx, `
		INSERT INTO gosignal_groups (`+groupColumns+`) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (group_id) DO UPDATE SET
			title=excluded.title, revision=excluded.revision, left_at=excluded.left_at,
			updated_at=excluded.updated_at`,
		rec.ID, rec.Title, rec.Revision, nullableMilli(rec.LeftAt), rec.UpdatedAt.UnixMilli())
	if err != nil {
		return fmt.Errorf("write group %s: %w", rec.ID, err)
	}

	return nil
}

// Group returns the record of the group groupID, and whether there is one.
func (s *Store) Group(ctx context.Context, groupID string) (GroupRecord, bool, error) {
	rec, err := scanGroupRecord(s.own.QueryRow(ctx,
		"SELECT "+groupColumns+" FROM gosignal_groups WHERE group_id=$1", groupID))
	if errors.Is(err, sql.ErrNoRows) {
		return GroupRecord{}, false, nil
	}

	if err != nil {
		return GroupRecord{}, false, fmt.Errorf("read group %s: %w", groupID, err)
	}

	return rec, true, nil
}

// Groups returns all group records, sorted by ID.
func (s *Store) Groups(ctx context.Context) ([]GroupRecord, error) {
	rows, err := s.own.Query(ctx, "SELECT "+groupColumns+" FROM gosignal_groups ORDER BY group_id")

	recs, err := dbutil.NewRowIterWithError(rows, scanGroupRecord, err).AsList()
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}

	return recs, nil
}

func scanGroupRecord(row dbutil.Scannable) (GroupRecord, error) {
	var (
		rec     GroupRecord
		leftAt  sql.NullInt64
		updated int64
	)

	err := row.Scan(&rec.ID, &rec.Title, &rec.Revision, &leftAt, &updated)
	if err != nil {
		return GroupRecord{}, err //nolint:wrapcheck // callers wrap it
	}

	if leftAt.Valid {
		rec.LeftAt = time.UnixMilli(leftAt.Int64).UTC()
	}

	rec.UpdatedAt = time.UnixMilli(updated).UTC()

	return rec, nil
}

// nullableMilli converts t to ms since the epoch, or NULL for the zero time.
func nullableMilli(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}

	return sql.NullInt64{Int64: t.UnixMilli(), Valid: true}
}
