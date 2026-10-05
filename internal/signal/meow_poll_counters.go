//go:build cgo || libsignal_go

package signal

import (
	"context"
	"errors"
	"fmt"

	"github.com/cwbudde/go-signal/internal/store"
)

func (c *meowClient) ReservePollVote(ctx context.Context, req PollVoteCounterRequest) (uint32, error) {
	err := req.Check()
	if err != nil {
		return 0, err
	}

	var count uint32

	err = c.withStore(ctx, func(data *store.Store) error {
		var reserveErr error

		count, reserveErr = data.ReservePollVote(ctx, req.Chat.Key(), req.Author.ACI, req.Timestamp, req.Explicit)

		return reserveErr //nolint:wrapcheck // Wrapped by this method below.
	})
	if errors.Is(err, store.ErrPollVoteExhausted) {
		return 0, ErrPollVoteExhausted
	}

	if err != nil {
		return 0, fmt.Errorf("poll vote counter: %w", err)
	}

	return count, nil
}

func (c *meowClient) learnPollVoteCounter(ctx context.Context, evt Event) error {
	req, ok := OwnPollVoteCounter(evt, c.ownACI)
	if !ok {
		return nil
	}

	_, err := c.ReservePollVote(ctx, req)

	return err
}
