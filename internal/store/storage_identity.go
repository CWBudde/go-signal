//go:build cgo || libsignal_go

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// StorageIdentity records the last remote tuple and the local state at observation.
// Dirty protects a later local or authenticated sync decision, even after its outbox clears.
type StorageIdentity struct {
	ServiceID   string
	RemoteKey   []byte
	RemoteTrust string
	LocalKey    []byte
	LocalTrust  string
	Dirty       bool
}

// StorageIdentity returns the observation for the account-local service ID.
func (s *Store) StorageIdentity(ctx context.Context, serviceID string) (*StorageIdentity, error) {
	rec := &StorageIdentity{ServiceID: serviceID}

	err := s.own.QueryRow(ctx, `SELECT remote_key,remote_trust,local_key,local_trust,dirty
 FROM gosignal_storage_identities WHERE service_id=$1`, serviceID).Scan(
		&rec.RemoteKey, &rec.RemoteTrust, &rec.LocalKey, &rec.LocalTrust, &rec.Dirty)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // no observation yet
	}

	if err != nil {
		return nil, fmt.Errorf("read storage identity: %w", err)
	}

	return rec, nil
}

// PutStorageIdentity saves an observation in the caller's transaction.
func (s *Store) PutStorageIdentity(ctx context.Context, rec StorageIdentity) error {
	_, err := s.own.Exec(ctx, `INSERT INTO gosignal_storage_identities
 (service_id,remote_key,remote_trust,local_key,local_trust,dirty)
 VALUES ($1,$2,$3,$4,$5,$6)
 ON CONFLICT(service_id) DO UPDATE SET remote_key=excluded.remote_key,remote_trust=excluded.remote_trust,
 local_key=excluded.local_key,local_trust=excluded.local_trust,dirty=excluded.dirty`,
		rec.ServiceID, rec.RemoteKey, rec.RemoteTrust, rec.LocalKey, rec.LocalTrust, rec.Dirty)
	if err != nil {
		return fmt.Errorf("write storage identity: %w", err)
	}

	return nil
}

// ProtectStorageIdentity preserves a local decision against stale contact storage.
func (s *Store) ProtectStorageIdentity(ctx context.Context, serviceID string) error {
	_, err := s.own.Exec(ctx, `INSERT INTO gosignal_storage_identities (service_id,dirty) VALUES ($1,true)
 ON CONFLICT(service_id) DO UPDATE SET dirty=true`, serviceID)
	if err != nil {
		return fmt.Errorf("protect storage identity: %w", err)
	}

	return nil
}
