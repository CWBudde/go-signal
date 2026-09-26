//go:build cgo || purego

package store

import (
	"context"
	"fmt"
)

// Pragma returns the value of an SQLite pragma on the account database, for tests that check
// the connection options of either driver.
func (s *Store) Pragma(ctx context.Context, name string) (string, error) {
	var value string

	err := s.db.QueryRow(ctx, "PRAGMA "+name).Scan(&value)
	if err != nil {
		return "", fmt.Errorf("pragma %s: %w", name, err)
	}

	return value, nil
}
