package signal

import "errors"

// MyStoryID is Signal's reserved distribution ID for My Story.
const MyStoryID = "00000000-0000-0000-0000-000000000000"

// ErrStoryAudienceUnavailable means the complete private audience cannot be determined.
var ErrStoryAudienceUnavailable = errors.New("story audience unavailable")

// StoryAudience is one phone-defined distribution list and its expanded ACI audience.
type StoryAudience struct {
	ID            string
	Name          string
	IsBlockList   bool
	AllowsReplies bool
	Recipients    []Recipient
}

// StoryAudiences is a complete audience snapshot at one storage manifest version.
type StoryAudiences struct {
	StorageVersion uint64
	Audiences      []StoryAudience
}
