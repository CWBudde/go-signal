package signal

import (
	"errors"
	"fmt"

	"github.com/cwbudde/go-signal/internal/signal/groupinvite"
)

// GroupJoinStatus describes freshly verified membership or a pending request.
type GroupJoinStatus string

const (
	GroupJoinMember     GroupJoinStatus = "member"
	GroupJoinRequesting GroupJoinStatus = "requesting"
)

// GroupJoinResult distinguishes acceptance from verification. ID alone proves neither.
type GroupJoinResult struct {
	ID       string
	Title    string
	Revision uint32
	Status   GroupJoinStatus
	Changed  bool
	Accepted bool
	Verified bool
}

var (
	ErrInvalidGroupInviteLink            = errors.New("invalid group invite link")
	ErrGroupLinkInactive                 = errors.New("group invite link is unavailable, disabled, reset or banned")
	ErrGroupTerminated                   = errors.New("group is terminated")
	ErrGroupInvitationRequiresAcceptance = errors.New("existing group invitation requires acceptance")
)

// CheckGroupInviteLink validates sensitive input before opening an account.
func CheckGroupInviteLink(link string) error {
	_, err := groupinvite.Parse(link)
	if err != nil {
		return ErrInvalidGroupInviteLink
	}

	return nil
}

type groupJoinOperationError struct {
	message string
	cause   error
}

func (e groupJoinOperationError) Error() string { return e.message }
func (e groupJoinOperationError) Unwrap() error { return e.cause }

// GroupJoinOperationError hides arbitrary dependency errors while preserving identity
// and explaining accepted or uncertain outcomes. The result must contain no secrets.
func GroupJoinOperationError(err error, result GroupJoinResult) error {
	message := "group join failed"

	const inspect = "; inspect groups show or ask an administrator or your phone before retrying " +
		"(show may be unavailable until approval)"

	switch {
	case result.Accepted:
		message = fmt.Sprintf("join accepted; group %s revision %d; follow-up failed"+inspect, result.ID, result.Revision)
	case errors.Is(err, ErrGroupUpdateUncertain):
		message = fmt.Sprintf("join of group %s is uncertain at attempted revision %d"+inspect, result.ID, result.Revision)
	case errors.Is(err, ErrGroupInvitationRequiresAcceptance):
		message = ErrGroupInvitationRequiresAcceptance.Error()
	case errors.Is(err, ErrGroupTerminated):
		message = ErrGroupTerminated.Error()
	case errors.Is(err, ErrGroupLinkInactive):
		message = ErrGroupLinkInactive.Error()
	case errors.Is(err, ErrGroupChanged):
		message = "group changed before joining; inspect before retrying"
	case errors.Is(err, ErrInvalidGroupInviteLink):
		message = ErrInvalidGroupInviteLink.Error()
	}

	return groupJoinOperationError{message: message, cause: err}
}
