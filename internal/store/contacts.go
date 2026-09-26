//go:build cgo

package store

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/util/dbutil"
)

// BlockOverride is a block or unblock made on this device that the storage service doesn't
// reflect yet. Until it does, storage syncs would undo it, so the client re-applies it.
type BlockOverride struct {
	ACI     string
	Blocked bool
	SetAt   time.Time
}

// SetBlockOverride records (or replaces) the override for aci.
func (s *Store) SetBlockOverride(ctx context.Context, override BlockOverride) error {
	_, err := s.own.Exec(ctx, `INSERT INTO gosignal_block_overrides (aci, blocked, set_at) VALUES ($1, $2, $3)
		ON CONFLICT (aci) DO UPDATE SET blocked=excluded.blocked, set_at=excluded.set_at`,
		override.ACI, override.Blocked, override.SetAt.UnixMilli())
	if err != nil {
		return fmt.Errorf("write block override %s: %w", override.ACI, err)
	}

	return nil
}

// BlockOverrides returns all overrides, ordered by ACI.
func (s *Store) BlockOverrides(ctx context.Context) ([]BlockOverride, error) {
	rows, err := s.own.Query(ctx, "SELECT aci, blocked, set_at FROM gosignal_block_overrides ORDER BY aci")

	overrides, err := dbutil.NewRowIterWithError(rows, scanBlockOverride, err).AsList()
	if err != nil {
		return nil, fmt.Errorf("read block overrides: %w", err)
	}

	return overrides, nil
}

// DeleteBlockOverride removes the override for aci, if any.
func (s *Store) DeleteBlockOverride(ctx context.Context, aci string) error {
	_, err := s.own.Exec(ctx, "DELETE FROM gosignal_block_overrides WHERE aci=$1", aci)
	if err != nil {
		return fmt.Errorf("delete block override %s: %w", aci, err)
	}

	return nil
}

// BlockedACIs returns the ACIs of the users signalmeow has marked as blocked for the account
// accountACI, sorted. signalmeow's RecipientStore can only list users with a name or number.
func (s *Store) BlockedACIs(ctx context.Context, accountACI string) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT aci_uuid FROM signalmeow_recipients
		WHERE account_id=$1 AND blocked AND aci_uuid IS NOT NULL ORDER BY aci_uuid`, accountACI)

	acis, err := dbutil.NewRowIterWithError(rows, dbutil.ScanSingleColumn[string], err).AsList()
	if err != nil {
		return nil, fmt.Errorf("list blocked users: %w", err)
	}

	return acis, nil
}

func scanBlockOverride(row dbutil.Scannable) (BlockOverride, error) {
	var (
		override BlockOverride
		setAt    int64
	)

	err := row.Scan(&override.ACI, &override.Blocked, &setAt)
	if err != nil {
		return BlockOverride{}, fmt.Errorf("scan block override: %w", err)
	}

	override.SetAt = time.UnixMilli(setAt).UTC()

	return override, nil
}
