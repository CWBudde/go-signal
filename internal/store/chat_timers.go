//go:build cgo || libsignal_go

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ChatTimerRecord is an account-local direct chat's latest known disappearing timer.
// Zero seconds explicitly disables disappearing messages; version zero is legacy state.
type ChatTimerRecord struct {
	ACI              string
	Seconds, Version uint32
}

var errInvalidChatTimerACI = errors.New("chat timer requires a canonical nonzero ACI")

func validateChatTimerACI(aci string) error {
	id, err := uuid.Parse(aci)
	if err != nil || id == uuid.Nil || id.String() != aci {
		return fmt.Errorf("%w: %q", errInvalidChatTimerACI, aci)
	}

	return nil
}

// ChatTimer returns the timer for aci, distinguishing unknown from explicitly disabled.
func (s *Store) ChatTimer(ctx context.Context, aci string) (ChatTimerRecord, bool, error) {
	err := validateChatTimerACI(aci)
	if err != nil {
		return ChatTimerRecord{}, false, err
	}

	rec := ChatTimerRecord{ACI: aci}

	err = s.own.QueryRow(ctx, "SELECT seconds, version FROM gosignal_chat_timers WHERE aci=$1", aci).
		Scan(&rec.Seconds, &rec.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return ChatTimerRecord{}, false, nil
	}

	if err != nil {
		return ChatTimerRecord{}, false, fmt.Errorf("read chat timer %s: %w", aci, err)
	}

	return rec, true, nil
}

// MergeChatTimer initializes an unknown timer or updates it for a strictly newer version.
func (s *Store) MergeChatTimer(ctx context.Context, rec ChatTimerRecord) error {
	return s.MergeChatTimers(ctx, []ChatTimerRecord{rec})
}

// MergeChatTimers merges all records atomically, ignoring equal and older versions.
func (s *Store) MergeChatTimers(ctx context.Context, records []ChatTimerRecord) error {
	for _, rec := range records {
		err := validateChatTimerACI(rec.ACI)
		if err != nil {
			return err
		}
	}

	err := s.own.DoTxn(ctx, nil, func(ctx context.Context) error {
		for _, rec := range records {
			_, err := s.own.Exec(ctx, `INSERT INTO gosignal_chat_timers (aci, seconds, version)
    VALUES ($1, $2, $3) ON CONFLICT (aci) DO UPDATE
    SET seconds=excluded.seconds, version=excluded.version
    WHERE excluded.version > gosignal_chat_timers.version`, rec.ACI, int64(rec.Seconds), int64(rec.Version))
			if err != nil {
				return fmt.Errorf("write chat timer %s: %w", rec.ACI, err)
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("merge chat timers: %w", err)
	}

	return nil
}
