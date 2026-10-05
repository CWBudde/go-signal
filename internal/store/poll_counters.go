//go:build cgo || libsignal_go

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// ErrPollVoteExhausted means there is no larger uint32 vote counter.
var ErrPollVoteExhausted = errors.New("poll vote counter exhausted")

// ReservePollVote atomically reserves the next counter when explicit is zero. A positive
// explicit value is returned unchanged, while the durable maximum can only increase.
// Reservations survive failed sends and inbox pruning. Timestamps are exact unsigned text.
func (s *Store) ReservePollVote(ctx context.Context, chat, author string, timestamp uint64, explicit uint32,
) (uint32, error) {
	query := `INSERT INTO gosignal_poll_counters (chat, author, timestamp, counter)
  VALUES ($1, $2, $3, $4) ON CONFLICT (chat, author, timestamp)
  DO UPDATE SET counter=MAX(counter, excluded.counter) RETURNING counter`

	initial := explicit
	if explicit == 0 {
		initial = 1
		query = `INSERT INTO gosignal_poll_counters (chat, author, timestamp, counter)
   VALUES ($1, $2, $3, $4) ON CONFLICT (chat, author, timestamp)
   DO UPDATE SET counter=counter+1 WHERE counter < 4294967295 RETURNING counter`
	}

	var count uint32

	err := s.own.QueryRow(ctx, query, chat, author, strconv.FormatUint(timestamp, 10), initial).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrPollVoteExhausted
	}

	if err != nil {
		return 0, fmt.Errorf("reserve poll vote counter: %w", err)
	}

	if explicit != 0 {
		return explicit, nil
	}

	return count, nil
}
