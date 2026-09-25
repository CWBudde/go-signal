package signal

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// ErrAccountRequired means several accounts are linked and -a/--account doesn't pick one.
var ErrAccountRequired = errors.New("several accounts are linked; choose one with -a/--account")

// ErrInvalidAccount means -a/--account is neither an E.164 number nor an ACI.
var ErrInvalidAccount = errors.New("account must be an E.164 number (+4915112345678) or an ACI (UUID)")

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)

// SelectAccount picks the account that want (E.164 number or ACI; "" for the default) names
// among the linked accounts. Without want, exactly one account must be linked.
func SelectAccount(accounts []Account, want string) (Account, error) {
	if want != "" {
		return findAccount(accounts, want)
	}

	switch len(accounts) {
	case 0:
		return Account{}, ErrNotLinked
	case 1:
		return accounts[0], nil
	}

	choices := make([]string, 0, len(accounts))
	for _, acc := range accounts {
		choices = append(choices, fmt.Sprintf("%s (ACI %s)", acc.Number, acc.ACI))
	}

	return Account{}, fmt.Errorf("%w: %s", ErrAccountRequired, strings.Join(choices, ", "))
}

func findAccount(accounts []Account, want string) (Account, error) {
	match, err := accountMatcher(want)
	if err != nil {
		return Account{}, err
	}

	for _, acc := range accounts {
		if match(acc) {
			return acc, nil
		}
	}

	return Account{}, fmt.Errorf("%w: %s", ErrAccountNotFound, want)
}

// accountMatcher returns a predicate for the account want names.
func accountMatcher(want string) (func(Account) bool, error) {
	if e164.MatchString(want) {
		return func(acc Account) bool { return acc.Number == want }, nil
	}

	aci, err := uuid.Parse(want)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrInvalidAccount, want)
	}

	return func(acc Account) bool { return strings.EqualFold(acc.ACI, aci.String()) }, nil
}
