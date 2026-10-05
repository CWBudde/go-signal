//go:build cgo || libsignal_go

package signal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cwbudde/go-signal/internal/store"
)

func pollStoreKey(ref PollReference) store.PollKey {
	return store.PollKey{Chat: ref.Chat.Key(), Author: ref.Author.ACI, Timestamp: ref.Timestamp}
}

func pollObservation(event Event) (*store.PollObservation, error) {
	ref, evidence, ok := PollEvidence(event)
	if !ok {
		return nil, nil //nolint:nilnil // Unrelated events have no poll evidence.
	}

	encoded, err := marshalEvent(evidence, ref.Chat)
	if err != nil {
		return nil, err
	}

	digest := sha256.Sum256(encoded)

	return &store.PollObservation{PollKey: pollStoreKey(ref), Hash: hex.EncodeToString(digest[:]), Event: encoded}, nil
}

func pollSeed(record store.InboxRecord) (*store.PollObservation, error) {
	event, _ := unmarshalEvent(record.Event)
	return pollObservation(event)
}

func pollReduce(key store.PollKey, encoded [][]byte) ([]byte, error) {
	ref := PollReference{Author: Recipient{ACI: key.Author}, Timestamp: key.Timestamp}

	groupID, isGroup := strings.CutPrefix(key.Chat, "group:")
	if isGroup {
		ref.Chat.GroupID = groupID
	} else {
		ref.Chat.Recipient.ACI = key.Chat
	}

	events := make([]Event, 0, len(encoded))
	for _, data := range encoded {
		event, _ := unmarshalEvent(data)
		if unsupported, ok := event.(*Unsupported); ok && unsupported.Type == UnreadableEntry {
			return nil, fmt.Errorf("%w: unreadable poll evidence", ErrNotStorable)
		}

		events = append(events, event)
	}

	state := ReducePoll(ref, events)
	state.Source, state.Observations = "durable", len(events)

	data, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("encode poll projection: %w", err)
	}

	return data, nil
}

func (c *meowClient) PollProjection(ctx context.Context, ref PollReference) (PollState, error) {
	err := ref.Check()
	if err != nil {
		return PollState{}, err
	}

	state := ReducePoll(ref, nil)
	state.Source = "durable"

	err = c.withStore(ctx, func(data *store.Store) error {
		err := data.ProjectPolls(ctx, pollSeed, pollReduce, nil)
		if err != nil {
			return err //nolint:wrapcheck // Wrapped by PollProjection below.
		}

		encoded, err := data.PollProjectionRecord(ctx, pollStoreKey(ref))
		if err != nil {
			return err //nolint:wrapcheck // Wrapped by PollProjection below.
		}

		if len(encoded) > 0 {
			err = json.Unmarshal(encoded, &state)
			if err != nil {
				return fmt.Errorf("decode poll projection: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		return PollState{}, fmt.Errorf("poll projection: %w", err)
	}

	return state, nil
}

func (c *meowClient) learnPollProjection(ctx context.Context, event Event) error {
	observation, err := pollObservation(event)
	if err != nil || observation == nil {
		return err
	}

	return c.withStore(ctx, func(data *store.Store) error {
		return data.ProjectPolls(ctx, pollSeed, pollReduce, observation)
	})
}

func (c *meowClient) learnPollState(ctx context.Context, event Event) error {
	err := c.learnPollVoteCounter(ctx, event)
	if err != nil {
		return err
	}

	return c.learnPollProjection(ctx, event)
}
