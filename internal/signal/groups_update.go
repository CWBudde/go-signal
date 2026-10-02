package signal

import (
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"
)

var (
	// ErrInvalidGroupUpdate means the settings update is empty or contains an invalid description.
	ErrInvalidGroupUpdate = errors.New("invalid group update")
	// ErrGroupUpdateUncertain means a group PATCH may have been accepted despite its error.
	// Inspect the group before retrying; the result does not claim an accepted revision.
	ErrGroupUpdateUncertain = errors.New("group update outcome is uncertain")
)

// GroupUpdate contains optional group settings. Nil preserves the current value;
// empty descriptions, zero timers and false booleans explicitly clear or disable them.
type GroupUpdate struct {
	Description              *string
	TimerSeconds             *uint32
	AnnouncementsOnly        *bool
	MembersCanEditAttributes *bool
	MembersCanAddMembers     *bool
}

// Check validates a settings update before an account is opened.
func (update GroupUpdate) Check() error {
	if update.Description == nil && update.TimerSeconds == nil && !update.adminSettings() {
		return fmt.Errorf("%w: provide at least one setting", ErrInvalidGroupUpdate)
	}

	if update.Description != nil && !utf8.ValidString(*update.Description) {
		return fmt.Errorf("%w: description must be valid UTF-8", ErrInvalidGroupUpdate)
	}

	return nil
}

// CheckUpdate checks every supplied setting against the original membership and permissions.
func (g Group) CheckUpdate(self string, update GroupUpdate) error {
	err := update.Check()
	if err != nil {
		return err
	}

	membership, role := g.MembershipOf(self)
	if membership != MembershipMember {
		return ErrNotAMember
	}

	if update.adminSettings() && role != GroupRoleAdmin {
		return fmt.Errorf("%w: only administrators can change announcement mode or group permissions", ErrGroupPermission)
	}

	if (update.Description != nil || update.TimerSeconds != nil) && role != GroupRoleAdmin && !g.MembersCanEditAttributes {
		return fmt.Errorf("%w: only administrators can edit group attributes", ErrGroupPermission)
	}

	return nil
}

// WithUpdate applies a permitted settings update, incrementing the revision once if changed.
func (g Group) WithUpdate(self string, update GroupUpdate) (Group, error) {
	err := g.CheckUpdate(self, update)
	if err != nil {
		return Group{}, err
	}

	next := g
	if update.Description != nil {
		next.Description = *update.Description
	}

	if update.TimerSeconds != nil {
		next.Timer = time.Duration(*update.TimerSeconds) * time.Second
	}

	if update.AnnouncementsOnly != nil {
		next.AnnouncementsOnly = *update.AnnouncementsOnly
	}

	if update.MembersCanEditAttributes != nil {
		next.MembersCanEditAttributes = *update.MembersCanEditAttributes
	}

	if update.MembersCanAddMembers != nil {
		next.MembersCanAddMembers = *update.MembersCanAddMembers
	}

	if !next.sameSettings(g) {
		if g.Revision == math.MaxUint32 {
			return Group{}, fmt.Errorf("%w: group revision cannot be incremented", ErrUnknownGroup)
		}

		next.Revision++
	}

	return next, nil
}

func (update GroupUpdate) adminSettings() bool {
	return update.AnnouncementsOnly != nil || update.MembersCanEditAttributes != nil || update.MembersCanAddMembers != nil
}

func (g Group) sameSettings(other Group) bool {
	return g.Description == other.Description && g.Timer == other.Timer &&
		g.AnnouncementsOnly == other.AnnouncementsOnly &&
		g.MembersCanEditAttributes == other.MembersCanEditAttributes &&
		g.MembersCanAddMembers == other.MembersCanAddMembers
}
