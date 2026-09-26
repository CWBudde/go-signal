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

// IdentityRecord is go-signal's trust state of another user's identity key (table
// gosignal_identities). signalmeow keeps its own copy of the key in signalmeow_identity_keys.
//
// The methods that read and write records take the context of signalmeow's store callbacks,
// which may carry its database transaction; they run inside it then.
type IdentityRecord struct {
	// ServiceID is the user's service ID as signalmeow stores it: the ACI, or PNI:<uuid>.
	ServiceID string
	// Key is the serialized identity (public) key.
	Key []byte
	// Trust is the trust level's name (see signal.TrustLevel).
	Trust string
	// FirstSeen is when the first key of the user was stored; zero if unknown.
	FirstSeen time.Time
	// ChangedAt is when the key last changed; zero if it never did.
	ChangedAt time.Time
	// PreviousKey is the last trusted key before a change; nil if there was none.
	PreviousKey []byte
	// PendingEvent marks a change that hasn't been reported as an event yet.
	PendingEvent bool
}

const identityColumns = "service_id, identity_key, trust, first_seen, changed_at, previous_key, pending_event"

// Identity returns the record of serviceID, or nil if there is none.
func (s *Store) Identity(ctx context.Context, serviceID string) (*IdentityRecord, error) {
	rec, err := scanIdentity(s.own.QueryRow(ctx,
		"SELECT "+identityColumns+" FROM gosignal_identities WHERE service_id=$1", serviceID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // no record is not an error
	}

	if err != nil {
		return nil, fmt.Errorf("read identity %s: %w", serviceID, err)
	}

	return &rec, nil
}

// Identities returns all records, ordered by service ID.
func (s *Store) Identities(ctx context.Context) ([]IdentityRecord, error) {
	return s.queryIdentities(ctx, "SELECT "+identityColumns+" FROM gosignal_identities ORDER BY service_id")
}

// PendingIdentityChanges returns the records whose change hasn't been reported yet, ordered by
// the time of the change.
func (s *Store) PendingIdentityChanges(ctx context.Context) ([]IdentityRecord, error) {
	return s.queryIdentities(ctx, "SELECT "+identityColumns+
		" FROM gosignal_identities WHERE pending_event ORDER BY changed_at, service_id")
}

// PutIdentity inserts or replaces the record of rec.ServiceID.
func (s *Store) PutIdentity(ctx context.Context, rec IdentityRecord) error {
	_, err := s.own.Exec(ctx, `
		INSERT INTO gosignal_identities (`+identityColumns+`) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (service_id) DO UPDATE SET
			identity_key=excluded.identity_key, trust=excluded.trust, first_seen=excluded.first_seen,
			changed_at=excluded.changed_at, previous_key=excluded.previous_key,
			pending_event=excluded.pending_event`,
		rec.ServiceID, rec.Key, rec.Trust, unixMilli(rec.FirstSeen), unixMilli(rec.ChangedAt),
		rec.PreviousKey, rec.PendingEvent)
	if err != nil {
		return fmt.Errorf("write identity %s: %w", rec.ServiceID, err)
	}

	return nil
}

// MarkIdentityReported clears PendingEvent of serviceID's record, unless its key is no longer
// key (it changed again in the meantime).
func (s *Store) MarkIdentityReported(ctx context.Context, serviceID string, key []byte) error {
	_, err := s.own.Exec(ctx,
		"UPDATE gosignal_identities SET pending_event=false WHERE service_id=$1 AND identity_key=$2",
		serviceID, key)
	if err != nil {
		return fmt.Errorf("mark identity %s reported: %w", serviceID, err)
	}

	return nil
}

// StoredIdentityKeys returns the identity keys signalmeow stored for the account accountID (its
// ACI), by service ID. They include the account's own ACI and PNI keys.
func (s *Store) StoredIdentityKeys(ctx context.Context, accountID string) (map[string][]byte, error) {
	rows, err := s.db.Query(ctx,
		"SELECT their_service_id, key FROM signalmeow_identity_keys WHERE account_id=$1", accountID)

	keys, err := dbutil.RowIterAsMap(dbutil.NewRowIterWithError(rows, scanStoredKey, err),
		func(k storedKey) (string, []byte) { return k.serviceID, k.key })
	if err != nil {
		return nil, fmt.Errorf("read identity keys: %w", err)
	}

	return keys, nil
}

type storedKey struct {
	serviceID string
	key       []byte
}

func scanStoredKey(row dbutil.Scannable) (storedKey, error) {
	var k storedKey

	err := row.Scan(&k.serviceID, &k.key)

	return k, err //nolint:wrapcheck // wrapped by StoredIdentityKeys
}

func (s *Store) queryIdentities(ctx context.Context, query string) ([]IdentityRecord, error) {
	rows, err := s.own.Query(ctx, query)

	recs, err := dbutil.NewRowIterWithError(rows, scanIdentity, err).AsList()
	if err != nil {
		return nil, fmt.Errorf("read identities: %w", err)
	}

	return recs, nil
}

func scanIdentity(row dbutil.Scannable) (IdentityRecord, error) {
	var (
		rec                  IdentityRecord
		firstSeen, changedAt int64
	)

	err := row.Scan(&rec.ServiceID, &rec.Key, &rec.Trust, &firstSeen, &changedAt, &rec.PreviousKey,
		&rec.PendingEvent)
	if err != nil {
		return IdentityRecord{}, err //nolint:wrapcheck // callers wrap it
	}

	rec.FirstSeen, rec.ChangedAt = fromUnixMilli(firstSeen), fromUnixMilli(changedAt)

	return rec, nil
}

// unixMilli stores t as ms since the epoch, 0 for the zero time.
func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return t.UnixMilli()
}

func fromUnixMilli(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}

	return time.UnixMilli(ms).UTC()
}
