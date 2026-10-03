package signal

import (
	"errors"
	"fmt"
)

// GroupCancelRequestResult distinguishes HTTP acceptance from exact signed deletion.
// Verified proves deletion at Revision, or a fresh no-op preview, not future absence.
// ID is exposed only after binding the selected account's known key.
type GroupCancelRequestResult struct {
	ID       string
	Title    string
	Revision uint32
	Changed  bool
	Accepted bool
	Verified bool
}

// CheckGroupCancelRequestReference validates sensitive input before opening an account.
func CheckGroupCancelRequestReference(ref string) error { return CheckGroupAcceptReference(ref) }

// NormalizeGroupCancelRequestReference reuses acceptance's canonical known reference forms.
func NormalizeGroupCancelRequestReference(ref string) string {
	return NormalizeGroupAcceptReference(ref)
}

type groupCancelRequestOperationError struct {
	message string
	cause   error
}

func (e groupCancelRequestOperationError) Error() string { return e.message }
func (e groupCancelRequestOperationError) Unwrap() error { return e.cause }

// GroupCancelRequestOperationError hides arbitrary nested text while preserving causes.
// Only an already key-bound canonical ID belongs in the partial result.
func GroupCancelRequestOperationError(err error, result GroupCancelRequestResult) error {
	message := "group join request cancellation failed"

	identity := ""
	if acceptGroupID(result.ID) {
		identity = fmt.Sprintf("; group %s revision %d", result.ID, result.Revision)
	}

	const inspect = "; inspect groups show or ask your phone or an administrator before retrying"

	switch {
	case result.Accepted:
		message = "join request cancellation accepted" + identity + "; operation failed" + inspect
	case errors.Is(err, ErrGroupUpdateUncertain):
		message = "group join request cancellation is uncertain" + identity + inspect
	case errors.Is(err, ErrGroupTerminated):
		message = ErrGroupTerminated.Error()
	case errors.Is(err, ErrGroupChanged):
		message = "group changed before cancelling the join request; inspect groups show before retrying"
	case errors.Is(err, ErrUnknownGroup):
		message = "group is not known to this account"
	}

	return groupCancelRequestOperationError{message: message, cause: err}
}
