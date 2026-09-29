package signal_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/google/uuid"
)

const createTitle = "Weekend"

func TestCreateGroupOptions(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		opts signal.CreateGroupOptions
		want error
	}{
		{"blank title", signal.CreateGroupOptions{Title: "\t"}, signal.ErrInvalidGroupTitle},
		{"invalid UTF-8", signal.CreateGroupOptions{Title: "\xff"}, signal.ErrInvalidGroupTitle},
		{
			"missing ACI",
			signal.CreateGroupOptions{Title: createTitle, Members: []signal.Recipient{{Number: "+4915112345678"}}},
			signal.ErrUnresolvable,
		},
		{
			"invalid ACI",
			signal.CreateGroupOptions{Title: createTitle, Members: []signal.Recipient{{ACI: "bad"}}},
			signal.ErrUnresolvable,
		},
		{
			"nil ACI",
			signal.CreateGroupOptions{Title: createTitle, Members: []signal.Recipient{{ACI: uuid.Nil.String()}}},
			signal.ErrUnresolvable,
		},
		{"alone", signal.CreateGroupOptions{Title: createTitle}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.opts.Check()
			if !errors.Is(err, test.want) {
				t.Errorf("Check = %v, want %v", err, test.want)
			}
		})
	}
}

func TestCreateGroupUniqueMembers(t *testing.T) {
	t.Parallel()

	opts := signal.CreateGroupOptions{Title: createTitle, Members: []signal.Recipient{
		{ACI: selfACI}, {ACI: strings.ToUpper(aliceACI)}, {ACI: aliceACI}, {ACI: bobACI},
	}}

	err := opts.Check()
	if err != nil {
		t.Fatal(err)
	}

	members := opts.UniqueMembers(selfACI)
	if len(members) != 2 || members[0].ACI != aliceACI || members[1].ACI != bobACI {
		t.Errorf("members = %+v", members)
	}

	if opts.Members[1].ACI != strings.ToUpper(aliceACI) {
		t.Error("mutated input")
	}
}
