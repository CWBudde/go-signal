package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/google/uuid"
)

// DefaultPinScanLimit and MaxPinScanLimit bound offline pinned-message inspection.
const (
	DefaultPinScanLimit = 1000
	MaxPinScanLimit     = 10000
)

// Check validates the canonical chat and bounded offline snapshot size.
func (req PinListRequest) Check() error {
	target, err := ParseRecipient(req.Chat)
	if err != nil {
		return err
	}

	if target.IsGroup() {
		if req.Chat != GroupPrefix+target.GroupID {
			return fmt.Errorf("%w: list requires a canonical chat", ErrInvalidRecipient)
		}
	} else if !canonicalPinACI(req.Chat) {
		return fmt.Errorf("%w: list requires a canonical non-nil ACI", ErrInvalidRecipient)
	}

	if req.ScanLimit < 0 || req.ScanLimit > MaxPinScanLimit {
		return fmt.Errorf("%w: scan limit must be 0 through %d", signal.ErrInvalidPin, MaxPinScanLimit)
	}

	return nil
}

// PinList reads retained observations from one bounded local snapshot.
func (a *App) PinList(ctx context.Context, req PinListRequest) (PinState, error) {
	err := req.Check()
	if err != nil {
		return PinState{}, fmt.Errorf("pins list: %w", err)
	}

	err = ctx.Err()
	if err != nil {
		return PinState{}, fmt.Errorf("pins list: %w", err)
	}

	target, _ := ParseRecipient(req.Chat)
	state := PinState{
		Chat:         signal.Chat{GroupID: target.GroupID, Recipient: target.Recipient},
		Completeness: "unknown", Observations: []PinObservation{},
	}

	limit := req.ScanLimit
	if limit == 0 {
		limit = DefaultPinScanLimit
	}

	now := a.now()

	entries, err := a.client.InboxList(ctx, signal.InboxQuery{Chat: req.Chat, Newest: true, Limit: limit + 1})
	if err != nil {
		return PinState{}, fmt.Errorf("pins list: inbox: %w", err)
	}

	state.boundSnapshot(&entries, limit)
	reducer := pinReducer{
		state: &state, now: now,
		observations: map[pinReference]PinObservation{},
		identities:   map[pinIdentity]pinPayload{}, deleted: map[pinReference]bool{},
	}

	for _, entry := range entries {
		err := ctx.Err()
		if err != nil {
			return PinState{}, fmt.Errorf("pins list: %w", err)
		}

		reducer.observe(entry)
	}

	err = ctx.Err()
	if err != nil {
		return PinState{}, fmt.Errorf("pins list: %w", err)
	}

	reducer.finalize()

	return state, nil
}

func canonicalPinACI(aci string) bool {
	parsed, err := uuid.Parse(aci)

	return err == nil && parsed != uuid.Nil && parsed.String() == aci
}

func (state *PinState) boundSnapshot(entries *[]signal.InboxEntry, limit int) {
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

type pinReference struct {
	author    string
	timestamp uint64
}

type pinIdentity struct {
	sender    string
	timestamp uint64
}

type pinPayload struct {
	reference pinReference
	operation string
	duration  uint32
	forever   bool
}

type pinReducer struct {
	state        *PinState
	now          time.Time
	observations map[pinReference]PinObservation
	identities   map[pinIdentity]pinPayload
	deleted      map[pinReference]bool
}

func (r *pinReducer) observe(entry signal.InboxEntry) {
	env, ok := envelopeOf(entry.Event)
	if !ok || env.Chat.Key() != r.state.Chat.Key() || entry.Chat.Key() != r.state.Chat.Key() {
		return
	}

	switch evt := entry.Event.(type) {
	case *signal.Unsupported:
		if evt.Type == "invalidPin" {
			r.state.IgnoredInvalid++
		}

		return
	case *signal.Delete:
		r.observeDelete(evt, entry.ReceivedAt)

		return
	}

	observation, recognized := pinObservation(entry)
	if !recognized {
		return
	}

	r.observeValid(observation)
}

func (r *pinReducer) observeDelete(evt *signal.Delete, received time.Time) {
	if !canonicalPinACI(evt.Sender.ACI) || evt.Timestamp == 0 || evt.TargetTimestamp == 0 || received.IsZero() {
		r.state.IgnoredInvalid++

		return
	}

	r.deleted[pinReference{author: evt.Sender.ACI, timestamp: evt.TargetTimestamp}] = true
}

func (r *pinReducer) observeValid(observation PinObservation) {
	if !validPinObservation(observation) {
		r.state.IgnoredInvalid++

		return
	}

	reference := pinReference{author: observation.TargetAuthor.ACI, timestamp: observation.TargetTimestamp}
	identity := pinIdentity{sender: observation.Sender.ACI, timestamp: observation.Timestamp}

	payload := pinPayload{
		reference: reference, operation: observation.Operation,
		duration: observation.DurationSeconds, forever: observation.Forever,
	}
	if first, exists := r.identities[identity]; exists {
		if first != payload {
			r.state.Conflicts++
		}

		return
	}

	r.identities[identity] = payload

	if observation.Operation == pinOperation && !observation.Forever {
		observation.ExpiresAt = observation.ReceivedAt.Add(time.Duration(observation.DurationSeconds) * time.Second)
		observation.ExpiryReached = !r.now.Before(observation.ExpiresAt)
	}

	r.observations[reference] = observation
}

func pinObservation(entry signal.InboxEntry) (PinObservation, bool) {
	observation := PinObservation{EntryID: entry.ID, ReceivedAt: entry.ReceivedAt}
	switch evt := entry.Event.(type) {
	case *signal.Pin:
		observation.Sender, observation.Timestamp = evt.Sender, evt.Timestamp
		observation.TargetAuthor, observation.TargetTimestamp = evt.TargetAuthor, evt.TargetTimestamp
		observation.Operation = pinOperation
		observation.DurationSeconds, observation.Forever = evt.DurationSeconds, evt.Forever
	case *signal.Unpin:
		observation.Sender, observation.Timestamp = evt.Sender, evt.Timestamp
		observation.TargetAuthor, observation.TargetTimestamp = evt.TargetAuthor, evt.TargetTimestamp
		observation.Operation = unpinOperation
	default:
		return PinObservation{}, false
	}

	return observation, true
}

func validPinObservation(obs PinObservation) bool {
	if !canonicalPinACI(obs.Sender.ACI) || !canonicalPinACI(obs.TargetAuthor.ACI) ||
		obs.Timestamp == 0 || obs.ReceivedAt.IsZero() {
		return false
	}

	if obs.Operation == pinOperation {
		return (signal.OutgoingPin{
			TargetAuthor: obs.TargetAuthor, TargetTimestamp: obs.TargetTimestamp,
			DurationSeconds: obs.DurationSeconds, Forever: obs.Forever,
		}).Check() == nil
	}

	return (signal.OutgoingUnpin{TargetAuthor: obs.TargetAuthor, TargetTimestamp: obs.TargetTimestamp}).Check() == nil
}

func (r *pinReducer) finalize() {
	for reference, observation := range r.observations {
		observation.TargetDeleted = r.deleted[reference]
		r.state.Observations = append(r.state.Observations, observation)
	}

	slices.SortFunc(r.state.Observations, func(a, b PinObservation) int {
		if order := strings.Compare(a.TargetAuthor.ACI, b.TargetAuthor.ACI); order != 0 {
			return order
		}

		return cmp.Compare(a.TargetTimestamp, b.TargetTimestamp)
	})
}
