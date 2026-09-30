package signal

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	profileNameLimit  = 257
	profileAboutLimit = 512
	profileEmojiLimit = 32
)

var (
	// ErrInvalidProfileUpdate means supplied or merged text is invalid, or no field was supplied.
	ErrInvalidProfileUpdate = errors.New("invalid profile update")
	// ErrProfileKeyUnavailable means the selected account has no usable profile key.
	ErrProfileKeyUnavailable = errors.New("profile key unavailable")
	// ErrProfileKeyChanged means the profile key is stale or changed during the operation.
	ErrProfileKeyChanged = errors.New("profile key changed")
	// ErrProfileV2Unsupported means the account requires an unsupported profile protocol.
	ErrProfileV2Unsupported = errors.New("profiles v2 unsupported")
	// ErrInvalidProfile means the server profile cannot be used safely.
	ErrInvalidProfile = errors.New("invalid profile")
	// ErrProfileRejected means the server rejected a profile write.
	ErrProfileRejected = errors.New("profile write rejected")
	// ErrProfileVerification means an accepted write could not be verified.
	ErrProfileVerification = errors.New("profile verification failed")
)

// Profile is the selected account's own server profile, retaining the exact name split.
type Profile struct {
	ACI        string
	GivenName  string
	FamilyName string
	About      string
	AboutEmoji string
	AvatarPath string
}

// ProfileUpdate changes supplied text fields. Nil preserves a field; a pointer to empty clears it.
type ProfileUpdate struct {
	GivenName  *string
	FamilyName *string
	About      *string
	AboutEmoji *string
}

// ProfileUpdateResult distinguishes confirmed acceptance from successful verification.
// Before verification, an accepted result contains only the account ACI in Profile.
// A no-op returns the fetched profile with Verified true and Changed/Accepted false.
type ProfileUpdateResult struct {
	Profile  Profile
	Changed  bool
	Accepted bool
	Verified bool
}

// Check validates supplied text before fetching any omitted fields. Limits are UTF-8 bytes.
func (u ProfileUpdate) Check() error {
	if u.GivenName == nil && u.FamilyName == nil && u.About == nil && u.AboutEmoji == nil {
		return fmt.Errorf("%w: supply at least one field", ErrInvalidProfileUpdate)
	}

	for _, field := range []struct {
		name  string
		value *string
		limit int
	}{
		{name: "given name", value: u.GivenName, limit: profileNameLimit},
		{name: "family name", value: u.FamilyName, limit: profileNameLimit - 1},
		{name: "about", value: u.About, limit: profileAboutLimit},
		{name: "about emoji", value: u.AboutEmoji, limit: profileEmojiLimit},
	} {
		if field.value == nil {
			continue
		}

		err := checkProfileText(field.name, *field.value, field.limit)
		if err != nil {
			return err
		}
	}

	if u.GivenName != nil && u.FamilyName != nil {
		return checkProfileName(*u.GivenName, *u.FamilyName)
	}

	return nil
}

// Apply merges supplied text into profile and validates the combined encoded name.
// Its bool reports a text difference; it does not indicate server acceptance.
func (u ProfileUpdate) Apply(profile Profile) (Profile, bool, error) {
	err := u.Check()
	if err != nil {
		return Profile{}, false, err
	}

	merged := profile
	for _, field := range []struct {
		supplied *string
		current  *string
	}{
		{supplied: u.GivenName, current: &merged.GivenName},
		{supplied: u.FamilyName, current: &merged.FamilyName},
		{supplied: u.About, current: &merged.About},
		{supplied: u.AboutEmoji, current: &merged.AboutEmoji},
	} {
		if field.supplied != nil {
			*field.current = *field.supplied
		}
	}

	err = checkProfileName(merged.GivenName, merged.FamilyName)
	if err != nil {
		return Profile{}, false, err
	}

	return merged, merged != profile, nil
}

func checkProfileText(field, text string, limit int) error {
	switch {
	case !utf8.ValidString(text):
		return fmt.Errorf("%w: %s must be valid UTF-8", ErrInvalidProfileUpdate, field)
	case strings.ContainsRune(text, '\x00'):
		return fmt.Errorf("%w: %s contains NUL", ErrInvalidProfileUpdate, field)
	case len(text) > limit:
		return fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalidProfileUpdate, field, limit)
	}

	return nil
}

func checkProfileName(given, family string) error {
	size := len(given)
	if family != "" {
		size += 1 + len(family)
	}

	if size > profileNameLimit {
		return fmt.Errorf("%w: combined name exceeds 257 bytes", ErrInvalidProfileUpdate)
	}

	return nil
}
