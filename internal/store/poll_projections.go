//go:build cgo || libsignal_go

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"go.mau.fi/util/dbutil"
)

// PollKey identifies a poll inside one account database.
type PollKey struct {
	Chat, Author string
	Timestamp    uint64
}

// PollObservation is one canonical, deduplicated piece of poll evidence.
type PollObservation struct {
	PollKey

	Hash  string
	Event []byte
}

// PollSeed decodes an existing inbox record, returning nil for unrelated events.
type PollSeed func(InboxRecord) (*PollObservation, error)

// PollReduce materializes a poll from its evidence in observation order.
type PollReduce func(PollKey, [][]byte) ([]byte, error)

// ProjectPolls bootstraps retained inbox evidence and applies next atomically. Write
// transactions serialize updates across clients. Evidence is independent of inbox retention.
func (s *Store) ProjectPolls(ctx context.Context, seed PollSeed, reduce PollReduce, next *PollObservation) error {
	err := s.own.DoTxn(ctx, nil, func(ctx context.Context) error {
		changed := make(map[PollKey]bool)

		_, ready, err := s.Meta(ctx, "poll-projections-seeded")
		if err != nil {
			return err
		}

		if !ready {
			err = s.seedPolls(ctx, seed, changed)
			if err != nil {
				return err
			}

			err = s.SetMeta(ctx, "poll-projections-seeded", "1")
			if err != nil {
				return err
			}
		}

		if next != nil {
			err = s.addPollEvidence(ctx, next, changed)
			if err != nil {
				return err
			}
		}

		for key := range changed {
			err = s.reducePoll(ctx, key, reduce)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("project polls: %w", err)
	}

	return nil
}

const pollSeedBatchSize = 1000

func (s *Store) seedPolls(ctx context.Context, seed PollSeed, changed map[PollKey]bool) error {
	var after int64
	for {
		records, err := s.InboxRecords(ctx, InboxFilter{After: after, Limit: pollSeedBatchSize})
		if err != nil {
			return err
		}

		if len(records) == 0 {
			return nil
		}

		for _, rec := range records {
			observation, err := seed(rec)
			if err != nil {
				return fmt.Errorf("decode poll seed: %w", err)
			}

			if observation != nil {
				err = s.addPollEvidence(ctx, observation, changed)
				if err != nil {
					return err
				}
			}

			after = rec.ID
		}
	}
}

func (s *Store) addPollEvidence(ctx context.Context, observation *PollObservation, changed map[PollKey]bool) error {
	result, err := s.own.Exec(ctx, `INSERT INTO gosignal_poll_evidence (chat,author,timestamp,hash,event)
 VALUES ($1,$2,$3,$4,$5) ON CONFLICT (chat,author,timestamp,hash) DO NOTHING`,
		observation.Chat, observation.Author, strconv.FormatUint(observation.Timestamp, 10),
		observation.Hash, string(observation.Event))
	if err != nil {
		return fmt.Errorf("write poll evidence: %w", err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count poll evidence: %w", err)
	}

	if count > 0 {
		changed[observation.PollKey] = true
	}

	return nil
}

func (s *Store) reducePoll(ctx context.Context, key PollKey, reduce PollReduce) error {
	timestamp := strconv.FormatUint(key.Timestamp, 10)
	rows, err := s.own.Query(ctx, `SELECT event FROM gosignal_poll_evidence
 WHERE chat=$1 AND author=$2 AND timestamp=$3 ORDER BY id`, key.Chat, key.Author, timestamp)

	events, err := dbutil.NewRowIterWithError(rows, dbutil.ScanSingleColumn[[]byte], err).AsList()
	if err != nil {
		return fmt.Errorf("read poll evidence: %w", err)
	}

	state, err := reduce(key, events)
	if err != nil {
		return fmt.Errorf("reduce poll evidence: %w", err)
	}

	_, err = s.own.Exec(ctx, `INSERT INTO gosignal_poll_projections (chat,author,timestamp,state)
 VALUES ($1,$2,$3,$4) ON CONFLICT (chat,author,timestamp) DO UPDATE SET state=excluded.state`,
		key.Chat, key.Author, timestamp, string(state))
	if err != nil {
		return fmt.Errorf("write poll projection: %w", err)
	}

	return nil
}

// PollProjectionRecord reads the materialized state, or nil when it is unknown.
func (s *Store) PollProjectionRecord(ctx context.Context, key PollKey) ([]byte, error) {
	var state []byte

	err := s.own.QueryRow(ctx, `SELECT state FROM gosignal_poll_projections
 WHERE chat=$1 AND author=$2 AND timestamp=$3`,
		key.Chat, key.Author, strconv.FormatUint(key.Timestamp, 10)).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read poll projection: %w", err)
	}

	return state, nil
}
