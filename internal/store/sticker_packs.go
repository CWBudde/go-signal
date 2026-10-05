//go:build cgo || libsignal_go

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"go.mau.fi/util/dbutil"
)

// StickerPack returns an opaque complete pack cache record by canonical ID.
func (s *Store) StickerPack(ctx context.Context, id string) ([]byte, bool, error) {
	var data []byte

	err := s.own.QueryRow(ctx, "SELECT data FROM gosignal_sticker_packs WHERE pack_id=$1", id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("read sticker pack: %w", err)
	}

	return data, true, nil
}

// PutStickerPack atomically replaces one complete pack, leaving other packs untouched.
func (s *Store) PutStickerPack(ctx context.Context, id string, data []byte) error {
	_, err := s.own.Exec(ctx, `INSERT INTO gosignal_sticker_packs (pack_id,data) VALUES ($1,$2)
 ON CONFLICT (pack_id) DO UPDATE SET data=excluded.data`, id, data)
	if err != nil {
		return fmt.Errorf("write sticker pack: %w", err)
	}

	return nil
}

// StickerPackIDs returns account-local installed IDs in canonical order.
func (s *Store) StickerPackIDs(ctx context.Context) ([]string, error) {
	rows, err := s.own.Query(ctx, "SELECT pack_id FROM gosignal_sticker_packs ORDER BY pack_id")

	ids, err := dbutil.NewRowIterWithError(rows, dbutil.ScanSingleColumn[string], err).AsList()
	if err != nil {
		return nil, fmt.Errorf("list sticker packs: %w", err)
	}

	return ids, nil
}
