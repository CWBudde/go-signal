//go:build cgo || libsignal_go

package store

import (
	"context"
	"fmt"

	"go.mau.fi/util/dbutil"
)

// IdentitySync is the last local ACI trust decision waiting to reach linked devices.
// Token distinguishes retries from a newer decision, even for the same key and trust.
type IdentitySync struct {
	ServiceID string
	Key       []byte
	Trust     string
	Token     string
}

// QueueIdentitySync replaces the pending decision. Call inside the trust transaction.
func (s *Store) QueueIdentitySync(ctx context.Context, update IdentitySync) error {
	_, err := s.own.Exec(ctx, `INSERT INTO gosignal_identity_sync(service_id,identity_key,trust,token)
 VALUES ($1,$2,$3,$4) ON CONFLICT(service_id) DO UPDATE SET
 identity_key=excluded.identity_key,trust=excluded.trust,token=excluded.token`,
		update.ServiceID, update.Key, update.Trust, update.Token)
	if err != nil {
		return fmt.Errorf("queue identity verification: %w", err)
	}

	return nil
}

// DeleteIdentitySync supersedes all queued local decisions for this identity.
// Incoming authenticated updates use it in the same transaction as applying trust.
func (s *Store) DeleteIdentitySync(ctx context.Context, serviceID string) error {
	_, err := s.own.Exec(ctx, `DELETE FROM gosignal_identity_sync WHERE service_id=$1`, serviceID)
	if err != nil {
		return fmt.Errorf("discard identity verification: %w", err)
	}

	return nil
}

// PendingIdentitySync discards obsolete decisions and lists only exact current keys/trust.
// The protocol key must belong to the selected account. The queue itself is account-local.
func (s *Store) PendingIdentitySync(ctx context.Context, accountID string) ([]IdentitySync, error) {
	_, err := s.own.Exec(ctx, `DELETE FROM gosignal_identity_sync AS pending WHERE NOT EXISTS (
 SELECT 1 FROM gosignal_identities AS identity JOIN signalmeow_identity_keys AS protocol
 ON protocol.their_service_id=identity.service_id AND protocol.account_id=$1
 WHERE identity.service_id=pending.service_id AND identity.identity_key=pending.identity_key
 AND protocol.key=pending.identity_key AND identity.trust=pending.trust)`, accountID)
	if err != nil {
		return nil, fmt.Errorf("prune identity verification: %w", err)
	}

	rows, err := s.own.Query(ctx,
		`SELECT service_id,identity_key,trust,token FROM gosignal_identity_sync ORDER BY service_id`)

	updates, err := dbutil.NewRowIterWithError(rows, func(row dbutil.Scannable) (IdentitySync, error) {
		var update IdentitySync

		scanErr := row.Scan(&update.ServiceID, &update.Key, &update.Trust, &update.Token)

		return update, scanErr //nolint:wrapcheck // row scanner forwards the scan error
	}, err).AsList()
	if err != nil {
		return nil, fmt.Errorf("read pending identity verification: %w", err)
	}

	return updates, nil
}

// IdentitySyncCurrent rechecks a snapshot immediately before sending.
func (s *Store) IdentitySyncCurrent(ctx context.Context, accountID string, update IdentitySync) (bool, error) {
	var current bool

	err := s.own.QueryRow(ctx, `SELECT EXISTS (
 SELECT 1 FROM gosignal_identity_sync AS pending JOIN gosignal_identities AS identity
 ON identity.service_id=pending.service_id JOIN signalmeow_identity_keys AS protocol
 ON protocol.their_service_id=pending.service_id AND protocol.account_id=$1
 WHERE pending.service_id=$2 AND pending.token=$3 AND identity.identity_key=pending.identity_key
 AND protocol.key=pending.identity_key AND identity.trust=pending.trust)`,
		accountID, update.ServiceID, update.Token).Scan(&current)
	if err != nil {
		return false, fmt.Errorf("check pending identity verification: %w", err)
	}

	return current, nil
}

// MarkIdentitySynced removes only the accepted snapshot; newer decisions remain queued.
func (s *Store) MarkIdentitySynced(ctx context.Context, update IdentitySync) error {
	_, err := s.own.Exec(ctx, `DELETE FROM gosignal_identity_sync WHERE service_id=$1 AND token=$2`,
		update.ServiceID, update.Token)
	if err != nil {
		return fmt.Errorf("mark identity verification synced: %w", err)
	}

	return nil
}
