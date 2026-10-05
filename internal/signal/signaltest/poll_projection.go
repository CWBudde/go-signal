package signaltest

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cwbudde/go-signal/internal/signal"
)

type pollEvidence struct {
	events []signal.Event
	seen   map[string]bool
}

func projectionKey(account string, ref signal.PollReference) pollCounterKey {
	return pollCounterKey{account: account, chat: ref.Chat.Key(), author: ref.Author.ACI, timestamp: ref.Timestamp}
}

func (c *client) PollProjection(ctx context.Context, ref signal.PollReference) (signal.PollState, error) {
	err := ctx.Err()
	if err != nil {
		return signal.PollState{}, fmt.Errorf("poll projection: %w", err)
	}

	err = ref.Check()
	if err != nil {
		return signal.PollState{}, fmt.Errorf("poll projection: %w", err)
	}

	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err = c.checkInbox()
	if err != nil {
		return signal.PollState{}, fmt.Errorf("poll projection: %w", err)
	}

	if c.fake.PollProjectionErr != nil {
		return signal.PollState{}, c.fake.PollProjectionErr
	}

	account, err := c.fake.account(c.opts)
	if err != nil {
		return signal.PollState{}, err
	}

	evidence := c.fake.pollEvidence[projectionKey(account.ACI, ref)]
	state := signal.ReducePoll(ref, evidence.events)
	state.Source, state.Observations = "durable", len(evidence.events)

	return state, nil
}

// observePoll requires the fake mutex. Canonical JSON excludes mutable identity metadata.
func (f *Fake) observePoll(account string, event signal.Event) error {
	ref, copyEvent, ok := signal.PollEvidence(event)
	if !ok {
		return nil
	}

	if f.PollProjectionErr != nil {
		return f.PollProjectionErr
	}

	encoded, err := json.Marshal(copyEvent)
	if err != nil {
		return fmt.Errorf("encode poll evidence: %w", err)
	}

	if f.pollEvidence == nil {
		f.pollEvidence = make(map[pollCounterKey]pollEvidence)
	}

	key := projectionKey(account, ref)

	evidence := f.pollEvidence[key]
	if evidence.seen == nil {
		evidence.seen = make(map[string]bool)
	}

	identity := fmt.Sprintf("%T:%s", copyEvent, encoded)
	if !evidence.seen[identity] {
		evidence.events = append(evidence.events, copyEvent)
		evidence.seen[identity] = true
		f.pollEvidence[key] = evidence
	}

	return nil
}
