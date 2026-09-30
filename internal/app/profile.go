package app

import (
	"context"
	"fmt"

	"github.com/cwbudde/go-signal/internal/signal"
)

// ProfileShow fetches the selected account's fresh server profile, connecting in send-only
// mode unless the client is already connected.
func (a *App) ProfileShow(ctx context.Context) (signal.Profile, error) {
	err := a.connectSendOnly(ctx)
	if err != nil {
		return signal.Profile{}, fmt.Errorf("profile show: connect: %w", err)
	}

	profile, err := a.client.OwnProfile(ctx)
	if err != nil {
		return signal.Profile{}, fmt.Errorf("profile show: %w", err)
	}

	return profile, nil
}

// ProfileUpdate validates supplied text before connecting in send-only mode and delegates
// exactly once. Accepted results survive follow-up errors; callers must inspect ProfileShow
// before retrying a write that was accepted but returned an error.
func (a *App) ProfileUpdate(
	ctx context.Context, update signal.ProfileUpdate,
) (signal.ProfileUpdateResult, error) {
	err := update.Check()
	if err != nil {
		return signal.ProfileUpdateResult{}, fmt.Errorf("profile update: %w", err)
	}

	err = a.connectSendOnly(ctx)
	if err != nil {
		return signal.ProfileUpdateResult{}, fmt.Errorf("profile update: connect: %w", err)
	}

	result, err := a.client.UpdateOwnProfile(ctx, update)
	if err != nil {
		if result.Accepted {
			return result, fmt.Errorf("profile update: write accepted; inspect profile show before retrying: %w", err)
		}

		return result, fmt.Errorf("profile update: %w", err)
	}

	return result, nil
}
