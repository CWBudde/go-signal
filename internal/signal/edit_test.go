package signal_test

import (
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestEditRequestCheck(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		change func(*signal.SendRequest)
		want   error
	}{
		{"valid", func(*signal.SendRequest) {}, nil},
		{"auto timestamp", func(r *signal.SendRequest) { r.Timestamp = 0 }, nil},
		{"blank", func(r *signal.SendRequest) { r.Body = " " }, signal.ErrInvalidContent},
		{"delete", func(r *signal.SendRequest) { r.DeleteTarget = 1 }, signal.ErrInvalidContent},
		{
			"reaction mixed with edit",
			func(r *signal.SendRequest) { r.Reaction = &signal.OutgoingReaction{} }, signal.ErrInvalidContent,
		},
		{"same timestamp", func(r *signal.SendRequest) { r.Timestamp = 1 }, signal.ErrInvalidContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			req := signal.SendRequest{
				Recipients: []signal.Recipient{{ACI: selfACI}}, Body: "replacement", Timestamp: 2, EditTarget: 1,
			}
			test.change(&req)

			err := req.Check()
			if !errors.Is(err, test.want) {
				t.Errorf("Check = %v, want %v", err, test.want)
			}
		})
	}
}
