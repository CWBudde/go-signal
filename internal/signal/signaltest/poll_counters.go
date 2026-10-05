package signaltest

import (
	"context"
	"fmt"
	"math"

	"github.com/cwbudde/go-signal/internal/signal"
)

type pollCounterKey struct {
	account, chat, author string
	timestamp             uint64
}

func (c *client) ReservePollVote(_ context.Context, req signal.PollVoteCounterRequest) (uint32, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err := req.Check()
	if err != nil {
		return 0, fmt.Errorf("poll vote counter: %w", err)
	}

	err = c.checkInbox()
	if err != nil {
		return 0, err
	}

	acc, err := c.fake.account(c.opts)
	if err != nil {
		return 0, err
	}

	return c.fake.reservePollVote(acc.ACI, req)
}

// reservePollVote requires the fake's mutex.
func (f *Fake) reservePollVote(account string, req signal.PollVoteCounterRequest) (uint32, error) {
	if f.PollCounterErr != nil {
		return 0, f.PollCounterErr
	}

	if f.pollCounters == nil {
		f.pollCounters = make(map[pollCounterKey]uint32)
	}

	key := pollCounterKey{account: account, chat: req.Chat.Key(), author: req.Author.ACI, timestamp: req.Timestamp}

	count := req.Explicit
	if count == 0 {
		if f.pollCounters[key] == math.MaxUint32 {
			return 0, signal.ErrPollVoteExhausted
		}

		count = f.pollCounters[key] + 1
	}

	f.pollCounters[key] = max(f.pollCounters[key], count)

	return count, nil
}
