package signal_test

import (
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestRecipientString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   signal.Recipient
		want string
	}{
		{signal.Recipient{ACI: "aci", Number: "+1"}, "aci"},
		{signal.Recipient{PNI: "pni", Number: "+1"}, "PNI:pni"},
		{signal.Recipient{Number: "+1", Username: "bob.01"}, "+1"},
		{signal.Recipient{Username: "bob.01"}, "@bob.01"},
		{signal.Recipient{}, ""},
	}

	for _, tc := range tests {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("%+v: got %q, want %q", tc.in, got, tc.want)
		}
	}

	if !(signal.Recipient{}).IsZero() || (signal.Recipient{Number: "+1"}).IsZero() {
		t.Error("IsZero is wrong")
	}
}
