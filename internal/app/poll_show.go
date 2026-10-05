package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/cwbudde/go-signal/internal/signal"
)

// PollShow reads a bounded inbox snapshot or durable projection without connecting or resolving identities.
func (a *App) PollShow(ctx context.Context, req PollShowRequest) (PollState, error) {
	err := req.Check()
	if err != nil {
		return PollState{}, fmt.Errorf("poll show: %w", err)
	}

	err = ctx.Err()
	if err != nil {
		return PollState{}, fmt.Errorf("poll show: %w", err)
	}

	author, timestamp, _ := ParseTarget(req.Target)
	state := PollState{
		Chat: signal.Chat{GroupID: req.GroupID}, Author: author.Recipient,
		Timestamp: timestamp, Completeness: "unknown",
	}

	if req.Recipient != "" {
		state.Chat = signal.Chat{Recipient: signal.Recipient{ACI: req.Recipient}}
	}

	ref := signal.PollReference{Chat: state.Chat, Author: state.Author, Timestamp: state.Timestamp}
	if req.Durable {
		projected, err := a.client.PollProjection(ctx, ref)
		if err != nil {
			return PollState{}, fmt.Errorf("poll show: %w", err)
		}

		return projected, nil
	}

	limit := req.ScanLimit
	if limit == 0 {
		limit = DefaultPollScanLimit
	}

	entries, err := a.client.InboxList(ctx, signal.InboxQuery{Chat: state.Chat.Key(), Newest: true, Limit: limit + 1})
	if err != nil {
		return PollState{}, fmt.Errorf("poll show: inbox: %w", err)
	}

	boundPollSnapshot(&state, &entries, limit)

	events := make([]signal.Event, 0, len(entries))
	for _, entry := range entries {
		err := ctx.Err()
		if err != nil {
			return PollState{}, fmt.Errorf("poll show: %w", err)
		}

		events = append(events, entry.Event)
	}

	reduced := signal.ReducePoll(ref, events)
	reduced.Scanned, reduced.FirstEntryID, reduced.LastEntryID = state.Scanned, state.FirstEntryID, state.LastEntryID
	reduced.Truncated = state.Truncated
	state = reduced

	return state, nil
}

func boundPollSnapshot(state *PollState, entries *[]signal.InboxEntry, limit int) {
	slices.SortFunc(*entries, func(a, b signal.InboxEntry) int { return cmp.Compare(a.ID, b.ID) })

	if len(*entries) > limit {
		state.Truncated = true
		*entries = (*entries)[len(*entries)-limit:]
	}

	state.Scanned = len(*entries)
	if len(*entries) > 0 {
		state.FirstEntryID = (*entries)[0].ID
		state.LastEntryID = (*entries)[len(*entries)-1].ID
	}
}
