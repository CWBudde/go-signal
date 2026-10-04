//go:build cgo || libsignal_go

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

// FailChatTimerWrite installs a test-only trigger that aborts inserts for aci.
func (s *Store) FailChatTimerWrite(ctx context.Context, aci string) error {
	_, err := s.own.Exec(ctx, `CREATE TRIGGER fail_chat_timer_write BEFORE INSERT ON gosignal_chat_timers
 WHEN NEW.aci = '`+aci+`' BEGIN SELECT RAISE(ABORT, 'timer test failure'); END`)
	if err != nil {
		return fmt.Errorf("install chat timer failure: %w", err)
	}

	return nil
}
