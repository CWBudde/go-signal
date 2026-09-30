package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/google/uuid"
)

const (
	pinOperation   = "pin"
	unpinOperation = "unpin"
)

// Check validates a pin request before opening a client.
func (req PinRequest) Check() error {
	err := checkPinTarget(req.Recipients, req.Target)
	if err != nil {
		return err
	}

	// The target may need lookup; validate its duration independently before connecting.
	err = (signal.OutgoingPin{
		TargetAuthor:    signal.Recipient{ACI: "00000000-0000-0000-0000-000000000001"},
		TargetTimestamp: 1, DurationSeconds: req.DurationSeconds, Forever: req.Forever,
	}).Check()
	if err != nil {
		return fmt.Errorf("pin: %w", err)
	}

	return nil
}

// Check validates an unpin request before opening a client.
func (req UnpinRequest) Check() error {
	return checkPinTarget(req.Recipients, req.Target)
}

func checkPinTarget(recipients []string, target string) error {
	errs := checkRecipients(recipients)

	author, _, err := ParseTarget(target)
	if err != nil {
		errs = append(errs, err)
	} else if author.Recipient.ACI == uuid.Nil.String() {
		errs = append(errs, fmt.Errorf("%w: target author must be a non-nil ACI", signal.ErrInvalidPin))
	}

	return errors.Join(errs...)
}

// Pin sends a standalone pin control through the normal send policy.
func (a *App) Pin(ctx context.Context, req PinRequest) (PinSendResult, error) {
	err := req.Check()
	if err != nil {
		return PinSendResult{}, fmt.Errorf("pin: %w", err)
	}

	author, timestamp, _ := ParseTarget(req.Target)
	out := PinSendResult{
		Operation: pinOperation, TargetTimestamp: timestamp,
		DurationSeconds: req.DurationSeconds, Forever: req.Forever,
	}
	out.SendResult, err = a.sendContent(ctx, pinOperation, req.Recipients, func(ctx context.Context) (content, error) {
		err := a.checkPinGroups(ctx, req.Recipients)
		if err != nil {
			return content{}, err
		}

		out.TargetAuthor, err = a.resolvePinAuthor(ctx, author)
		if err != nil {
			return content{}, err
		}

		pin := &signal.OutgoingPin{
			TargetAuthor: out.TargetAuthor, TargetTimestamp: timestamp,
			DurationSeconds: req.DurationSeconds, Forever: req.Forever,
		}

		err = pin.Check()
		if err != nil {
			return content{}, fmt.Errorf("pin payload: %w", err)
		}

		return content{pin: pin}, nil
	})

	return out, err
}

// Unpin sends a standalone unpin control through the normal send policy.
func (a *App) Unpin(ctx context.Context, req UnpinRequest) (PinSendResult, error) {
	err := req.Check()
	if err != nil {
		return PinSendResult{}, fmt.Errorf("unpin: %w", err)
	}

	author, timestamp, _ := ParseTarget(req.Target)
	out := PinSendResult{Operation: unpinOperation, TargetTimestamp: timestamp}
	out.SendResult, err = a.sendContent(ctx, unpinOperation, req.Recipients, func(ctx context.Context) (content, error) {
		err := a.checkPinGroups(ctx, req.Recipients)
		if err != nil {
			return content{}, err
		}

		out.TargetAuthor, err = a.resolvePinAuthor(ctx, author)
		if err != nil {
			return content{}, err
		}

		unpin := &signal.OutgoingUnpin{TargetAuthor: out.TargetAuthor, TargetTimestamp: timestamp}

		err = unpin.Check()
		if err != nil {
			return content{}, fmt.Errorf("unpin payload: %w", err)
		}

		return content{unpin: unpin}, nil
	})

	return out, err
}

// checkPinGroups uses fresh group state after sendContent has checked every allowlist.
func (a *App) checkPinGroups(ctx context.Context, recipients []string) error {
	seen := map[string]bool{}

	var own *signal.Account

	for _, arg := range recipients {
		target, err := ParseRecipient(arg)
		if err != nil {
			return err
		}

		if !target.IsGroup() || seen[target.GroupID] {
			continue
		}

		seen[target.GroupID] = true

		if own == nil {
			acc, err := a.client.Account(ctx)
			if err != nil {
				return fmt.Errorf("own account: %w", err)
			}

			own = &acc
		}

		err = a.checkPinGroup(ctx, target.GroupID, own.ACI)
		if err != nil {
			return err
		}
	}

	return nil
}

func (a *App) checkPinGroup(ctx context.Context, groupID, own string) error {
	group, err := a.client.Group(ctx, groupID)
	if err != nil {
		return fmt.Errorf("group %s: %w", groupID, err)
	}

	membership, role := group.MembershipOf(own)
	if membership != signal.MembershipMember {
		return fmt.Errorf("group %s: %w", groupID, signal.ErrNotAMember)
	}

	if role != signal.GroupRoleAdmin && !group.MembersCanEditAttributes {
		return fmt.Errorf("group %s: %w: pinning requires attribute-edit permission", groupID, signal.ErrGroupPermission)
	}

	return nil
}

func (a *App) resolvePinAuthor(ctx context.Context, author Target) (signal.Recipient, error) {
	authors := []Target{author}

	pending, err := a.markSelf(ctx, authors)
	if err != nil {
		return signal.Recipient{}, fmt.Errorf("resolve target author: %w", err)
	}

	if len(pending) > 0 {
		resolved, err := a.client.Resolve(ctx, []signal.Recipient{author.Recipient})
		if err != nil {
			return signal.Recipient{}, fmt.Errorf("resolve target author: %w", err)
		}

		if len(resolved) != 1 {
			return signal.Recipient{}, fmt.Errorf("%w: missing resolved target author", signal.ErrInvalidPin)
		}

		authors[0].Recipient = resolved[0]
	}

	return authors[0].Recipient, nil
}
