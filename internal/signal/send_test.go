package signal_test

import (
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestSendRequestCheck(t *testing.T) {
	t.Parallel()

	const aci = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

	users := []signal.Recipient{{ACI: aci}}
	group := "Z3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXAtaWQtZ3JvdXA="
	withReaction := func(change func(*signal.SendRequest)) signal.SendRequest {
		req := signal.SendRequest{Recipients: users, Reaction: &signal.OutgoingReaction{
			Emoji: "👍", TargetAuthor: signal.Recipient{ACI: aci}, TargetTimestamp: 1,
		}}
		change(&req)

		return req
	}

	tests := []struct {
		name string
		req  signal.SendRequest
		want error
	}{
		{"users", signal.SendRequest{Recipients: users, Body: "hi"}, nil},
		{"group", signal.SendRequest{GroupID: group, Body: "hi"}, nil},
		{"neither", signal.SendRequest{Body: "hi"}, signal.ErrInvalidSendRequest},
		{"both", signal.SendRequest{Recipients: users, GroupID: group}, signal.ErrInvalidSendRequest},
		{"reaction", withReaction(func(*signal.SendRequest) {}), nil},
		{"group reaction", withReaction(func(r *signal.SendRequest) { r.Recipients, r.GroupID = nil, group }), nil},
		{"reaction with body", withReaction(func(r *signal.SendRequest) { r.Body = "hi" }), signal.ErrInvalidContent},
		{
			"reaction with quote",
			withReaction(func(r *signal.SendRequest) { r.Quote = &signal.Quote{} }),
			signal.ErrInvalidContent,
		},
		{"reaction and delete", withReaction(func(r *signal.SendRequest) { r.DeleteTarget = 1 }), signal.ErrInvalidContent},
		{"no emoji", withReaction(func(r *signal.SendRequest) { r.Reaction.Emoji = "" }), signal.ErrInvalidContent},
		{"no target", withReaction(func(r *signal.SendRequest) { r.Reaction.TargetTimestamp = 0 }), signal.ErrInvalidContent},
		{
			"author without ACI",
			withReaction(func(r *signal.SendRequest) { r.Reaction.TargetAuthor = signal.Recipient{Number: "+15550101"} }),
			signal.ErrUnresolvable,
		},
		{"delete", signal.SendRequest{Recipients: users, DeleteTarget: 1}, nil},
		{"delete with body", signal.SendRequest{Recipients: users, DeleteTarget: 1, Body: "hi"}, signal.ErrInvalidContent},
		{
			"delete with mention",
			signal.SendRequest{GroupID: group, DeleteTarget: 1, Mentions: []signal.Mention{{}}},
			signal.ErrInvalidContent,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.req.Check()
			if !errors.Is(err, test.want) || (test.want == nil && err != nil) {
				t.Errorf("got %v, want %v", err, test.want)
			}
		})
	}
}
