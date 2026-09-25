package signal_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func accounts() (signal.Account, signal.Account) {
	return signal.Account{Number: "+15550100", ACI: "11111111-1111-1111-1111-111111111111", DeviceID: 2},
		signal.Account{Number: "+15550101", ACI: "33333333-3333-3333-3333-333333333333", DeviceID: 3}
}

func TestSelectAccount(t *testing.T) {
	t.Parallel()

	alice, bob := accounts()

	tests := []struct {
		name     string
		accounts []signal.Account
		want     string
		expect   signal.Account
		err      error
	}{
		{"none", nil, "", signal.Account{}, signal.ErrNotLinked},
		{"none with -a", nil, alice.Number, signal.Account{}, signal.ErrAccountNotFound},
		{"one", []signal.Account{alice}, "", alice, nil},
		{"one by number", []signal.Account{alice}, alice.Number, alice, nil},
		{"one by other number", []signal.Account{alice}, bob.Number, signal.Account{}, signal.ErrAccountNotFound},
		{"two without -a", []signal.Account{alice, bob}, "", signal.Account{}, signal.ErrAccountRequired},
		{"two by number", []signal.Account{alice, bob}, bob.Number, bob, nil},
		{"two by ACI", []signal.Account{alice, bob}, bob.ACI, bob, nil},
		{"ACI in upper case", []signal.Account{alice, bob}, strings.ToUpper(bob.ACI), bob, nil},
		{"invalid", []signal.Account{alice}, "alice", signal.Account{}, signal.ErrInvalidAccount},
		{"number without plus", []signal.Account{alice}, "15550100", signal.Account{}, signal.ErrInvalidAccount},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := signal.SelectAccount(test.accounts, test.want)
			if !errors.Is(err, test.err) || got != test.expect {
				t.Errorf("got %+v, %v; want %+v, %v", got, err, test.expect, test.err)
			}
		})
	}
}

func TestSelectAccountListsChoices(t *testing.T) {
	t.Parallel()

	alice, bob := accounts()

	_, err := signal.SelectAccount([]signal.Account{alice, bob}, "")

	for _, acc := range []signal.Account{alice, bob} {
		if !strings.Contains(err.Error(), acc.Number) || !strings.Contains(err.Error(), acc.ACI) {
			t.Errorf("error doesn't list %s: %v", acc.Number, err)
		}
	}
}
