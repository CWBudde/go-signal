//go:build cgo || libsignal_go

package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

// IdentityTestSQL executes controlled identity fixtures and failure triggers.
func (s *Store) IdentityTestSQL(ctx context.Context, query string, args ...any) error {
	_, err := s.db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("identity test SQL: %w", err)
	}

	return nil
}

// SQLiteDriverForTest names the database/sql driver of this build, which identifies the
// backend (cgo: mattn "sqlite3", libsignal_go: modernc "sqlite") in cross-backend tests.
const SQLiteDriverForTest = sqliteDriver

// SetPublishHook makes d run hook after each publication step; an error stops the step there.
func (d *Dir) SetPublishHook(hook func(step string) error) {
	d.hook = hook
}

// Stage returns the directory of the import attempt.
func (im *Import) Stage() string {
	return im.stage
}

// Abandon releases the attempt's owner lock without cleaning up, as a crashed process would.
func (im *Import) Abandon() {
	_ = im.owner.Close()
	im.owner = nil
}

// HoldRegistry takes d's registry lock and returns the function that releases it.
func (d *Dir) HoldRegistry() (func(), error) {
	file, err := os.OpenFile(filepath.Join(d.path, registryLockFile), os.O_RDWR|os.O_CREATE, filePerm)
	if err != nil {
		return nil, fmt.Errorf("open registry lock: %w", err)
	}

	err = tryLock(file)
	if err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("lock registry: %w", err)
	}

	return func() { _ = file.Close() }, nil
}

// PublishSteps returns the publication steps in order, as passed to the publish hook.
func PublishSteps() []string {
	return []string{
		stepAllocated, stepBuilt, stepMarked, stepIntent, stepRenamed, stepSynced, stepRegistered, stepRetiring,
	}
}
