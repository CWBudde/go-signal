package signaltest

import (
	"context"
	"fmt"
	"slices"

	"github.com/cwbudde/go-signal/internal/signal"
)

// ProfileUpdateCall records an UpdateOwnProfile invocation, including no-ops and failures.
type ProfileUpdateCall struct {
	ACI    string
	Update signal.ProfileUpdate
	Result signal.ProfileUpdateResult
}

// ProfileUpdates returns independent snapshots of every profile update invocation, in order.
func (f *Fake) ProfileUpdates() []ProfileUpdateCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	calls := slices.Clone(f.profileUpdates)
	for i := range calls {
		calls[i].Update = cloneProfileUpdate(calls[i].Update)
	}

	return calls
}

func (c *client) OwnProfile(ctx context.Context) (signal.Profile, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	err := c.checkProfileOp(ctx)
	if err != nil {
		return signal.Profile{}, err
	}

	return c.ownProfile()
}

func (c *client) UpdateOwnProfile(
	ctx context.Context, update signal.ProfileUpdate,
) (signal.ProfileUpdateResult, error) {
	c.fake.mu.Lock()
	defer c.fake.mu.Unlock()

	call := ProfileUpdateCall{ACI: c.connected, Update: cloneProfileUpdate(update)}
	if call.ACI == "" {
		acc, err := c.fake.account(c.opts)
		if err == nil {
			call.ACI = acc.ACI
		}
	}

	result, err := c.updateOwnProfile(ctx, update)
	call.Result = result
	c.fake.profileUpdates = append(c.fake.profileUpdates, call)

	return result, err
}

// updateOwnProfile executes the simulated update; the caller holds fake.mu.
func (c *client) updateOwnProfile(
	ctx context.Context, update signal.ProfileUpdate,
) (signal.ProfileUpdateResult, error) {
	err := update.Check()
	if err != nil {
		return signal.ProfileUpdateResult{}, err //nolint:wrapcheck // signal facade error
	}

	err = c.checkProfileOp(ctx)
	if err != nil {
		return signal.ProfileUpdateResult{}, err
	}

	profile, err := c.ownProfile()
	if err != nil {
		return signal.ProfileUpdateResult{}, err
	}

	merged, changed, err := update.Apply(profile)
	if err != nil {
		return signal.ProfileUpdateResult{}, err //nolint:wrapcheck // signal facade error
	}

	if !changed {
		return signal.ProfileUpdateResult{Profile: profile, Verified: true}, nil
	}

	if c.fake.UpdateProfileErr != nil {
		return signal.ProfileUpdateResult{}, c.fake.UpdateProfileErr
	}

	// Check cancellation immediately before the simulated write as well as at entry.
	err = ctx.Err()
	if err != nil {
		return signal.ProfileUpdateResult{}, fmt.Errorf("update profile: %w", err)
	}

	c.fake.Profiles[c.connected] = merged

	result := signal.ProfileUpdateResult{Profile: merged, Changed: true, Accepted: true, Verified: true}
	if c.fake.ProfileVerificationFails {
		result.Profile = signal.Profile{ACI: c.connected}
		result.Verified = false

		if c.fake.ProfileFollowUpErr == nil {
			return result, signal.ErrProfileVerification
		}
	}

	return result, c.fake.ProfileFollowUpErr
}

// checkProfileOp follows connection, close and remote-unlink behavior; the caller holds fake.mu.
func (c *client) checkProfileOp(ctx context.Context) error {
	err := c.checkGroupOp("profile")
	if err != nil {
		return err
	}

	err = ctx.Err()
	if err != nil {
		return fmt.Errorf("profile: %w", err)
	}

	return nil
}

// ownProfile reads only seeded remote profile state; the caller holds fake.mu.
func (c *client) ownProfile() (signal.Profile, error) {
	if c.fake.OwnProfileErr != nil {
		return signal.Profile{}, c.fake.OwnProfileErr
	}

	profile, ok := c.fake.Profiles[c.connected]
	if !ok {
		return signal.Profile{}, fmt.Errorf("%w: no seeded own profile (fake)", signal.ErrInvalidProfile)
	}

	profile.ACI = c.connected

	return profile, nil
}

func cloneProfileUpdate(update signal.ProfileUpdate) signal.ProfileUpdate {
	for _, field := range []**string{&update.GivenName, &update.FamilyName, &update.About, &update.AboutEmoji} {
		if *field != nil {
			value := **field
			*field = &value
		}
	}

	return update
}
