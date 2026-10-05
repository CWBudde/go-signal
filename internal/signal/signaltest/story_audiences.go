package signaltest

import (
	"context"
	"fmt"
	"slices"

	"github.com/cwbudde/go-signal/internal/signal"
)

func (c *client) StoryAudiences(ctx context.Context) (signal.StoryAudiences, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err := ctx.Err()
	if err != nil {
		return signal.StoryAudiences{}, fmt.Errorf("story audiences: %w", err)
	}

	if c.closed {
		return signal.StoryAudiences{}, signal.ErrClosed
	}

	if c.connected == "" {
		return signal.StoryAudiences{}, signal.ErrNotConnected
	}

	if c.lost != nil {
		return signal.StoryAudiences{}, c.lost
	}

	if c.fake.StoryAudiencesErr != nil {
		return signal.StoryAudiences{}, c.fake.StoryAudiencesErr
	}

	snapshot, ok := c.fake.StoryAudienceSnapshots[c.connected]
	if !ok {
		return signal.StoryAudiences{}, signal.ErrStoryAudienceUnavailable
	}

	snapshot.Audiences = slices.Clone(snapshot.Audiences)
	for i := range snapshot.Audiences {
		snapshot.Audiences[i].Recipients = slices.Clone(snapshot.Audiences[i].Recipients)
	}

	return snapshot, nil
}
