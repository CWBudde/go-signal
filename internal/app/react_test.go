package app_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

const targetAt = 1789999999000

func TestReact(t *testing.T) {
	t.Parallel()

	fake := directory()

	res, err := sender(t, fake).React(t.Context(), app.ReactRequest{
		Recipients: []string{aliceNumber},
		Target:     aliceNumber + ":1789999999000",
		Emoji:      " 👍 ",
	})
	if err != nil {
		t.Fatalf("react: %v", err)
	}

	if res.Timestamp != sentAt || res.TargetTimestamp != targetAt || res.Emoji != "👍" || res.Remove ||
		res.TargetAuthor.Recipient.ACI != aliceACI || len(res.Results) != 1 || !res.Results[0].OK() {
		t.Fatalf("got %+v", res)
	}

	want := []signal.SendRequest{{
		Recipients: []signal.Recipient{{ACI: aliceACI, PNI: carolACI, Number: aliceNumber}},
		Timestamp:  sentAt,
		Reaction: &signal.OutgoingReaction{
			Emoji:           "👍",
			TargetAuthor:    signal.Recipient{ACI: aliceACI, PNI: carolACI, Number: aliceNumber},
			TargetTimestamp: targetAt,
		},
	}}
	if got := fake.Sent(); !reflect.DeepEqual(got, want) {
		t.Errorf("sent %+v, want %+v", got, want)
	}
}

func TestReactRemoveOwnMessageInGroup(t *testing.T) {
	t.Parallel()

	fake := directory()
	own := testAccount()
	fake.Groups = map[string][]signal.Recipient{groupID: {{ACI: own.ACI}, {ACI: aliceACI}, {ACI: bobACI}}}

	res, err := sender(t, fake).React(t.Context(), app.ReactRequest{
		Recipients: []string{app.GroupPrefix + groupID},
		Target:     app.SelfRecipient + ":1789999999000",
		Emoji:      "❤️",
		Remove:     true,
	})
	if err != nil {
		t.Fatalf("react: %v", err)
	}

	if !res.TargetAuthor.Self || !res.Remove || len(res.Results) != 1 || len(res.Results[0].Members) != 2 {
		t.Fatalf("got %+v", res)
	}

	sent := fake.Sent()
	if len(sent) != 1 || sent[0].GroupID != groupID {
		t.Fatalf("sent %+v, want one group send", sent)
	}

	want := &signal.OutgoingReaction{
		Emoji: "❤️", Remove: true, TargetTimestamp: targetAt,
		TargetAuthor: signal.Recipient{ACI: own.ACI, Number: own.Number},
	}
	if !reflect.DeepEqual(sent[0].Reaction, want) {
		t.Errorf("reaction %+v, want %+v", sent[0].Reaction, want)
	}
}

func TestReactPartialFailure(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.SendFailures = map[string]error{bobACI: errBoom}

	res, err := sender(t, fake).React(t.Context(), app.ReactRequest{
		Recipients: []string{aliceNumber, bobUsername},
		Target:     bobUsername + ":1789999999000",
		Emoji:      "😂",
	})
	if !errors.Is(err, app.ErrSendFailed) || res.Failed() != 1 {
		t.Fatalf("got %+v, %v; want one failure", res, err)
	}
}

func TestReactInvalid(t *testing.T) {
	t.Parallel()

	valid := app.ReactRequest{Recipients: []string{aliceNumber}, Target: "self:1", Emoji: "👍"}
	with := func(change func(*app.ReactRequest)) app.ReactRequest {
		req := valid
		change(&req)

		return req
	}

	tests := []struct {
		name string
		req  app.ReactRequest
		want error
	}{
		{"without recipients", with(func(r *app.ReactRequest) { r.Recipients = nil }), app.ErrNoRecipients},
		{"bad recipient", with(func(r *app.ReactRequest) { r.Recipients = []string{"alice"} }), app.ErrInvalidRecipient},
		{"no target", with(func(r *app.ReactRequest) { r.Target = "" }), app.ErrInvalidTarget},
		{"target without author", with(func(r *app.ReactRequest) { r.Target = "1790000000000" }), app.ErrInvalidTarget},
		{"target timestamp", with(func(r *app.ReactRequest) { r.Target = "self:yesterday" }), app.ErrInvalidTarget},
		{
			"group author", with(func(r *app.ReactRequest) { r.Target = app.GroupPrefix + groupID + ":1" }),
			app.ErrInvalidTarget,
		},
		{"no emoji", with(func(r *app.ReactRequest) { r.Emoji = " " }), app.ErrInvalidEmoji},
		{"text", with(func(r *app.ReactRequest) { r.Emoji = "+1" }), app.ErrInvalidEmoji},
		{"word", with(func(r *app.ReactRequest) { r.Emoji = "ok👍" }), app.ErrInvalidEmoji},
		{"two", with(func(r *app.ReactRequest) { r.Emoji = "👍 👍" }), app.ErrInvalidEmoji},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := directory()

			_, err := sender(t, fake).React(t.Context(), test.req)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}

			if len(fake.Connects()) != 0 {
				t.Error("connected before validating the request")
			}
		})
	}
}

func TestReactEmoji(t *testing.T) {
	t.Parallel()

	// Emoji sequences: skin tone, ZWJ family, keycap, flag, subdivision flag with tags.
	for _, emoji := range []string{"👍🏽", "👨‍👩‍👧‍👦", "1️⃣", "🇩🇪", "🏴󠁧󠁢󠁳󠁣󠁴󠁿", "♥"} {
		fake := directory()

		_, err := sender(t, fake).React(t.Context(), app.ReactRequest{
			Recipients: []string{app.SelfRecipient}, Target: aliceNumber + ":1", Emoji: emoji,
		})
		if err != nil {
			t.Errorf("%q: %v", emoji, err)
		}
	}
}

func TestReactUnknownAuthor(t *testing.T) {
	t.Parallel()

	fake := directory()

	_, err := sender(t, fake).React(t.Context(), app.ReactRequest{
		Recipients: []string{aliceNumber}, Target: "+4915100000000:1", Emoji: "👍",
	})
	if !errors.Is(err, signal.ErrNotOnSignal) {
		t.Fatalf("got %v, want ErrNotOnSignal", err)
	}

	if len(fake.Sent()) != 0 {
		t.Errorf("sent %+v, want nothing", fake.Sent())
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.Groups = map[string][]signal.Recipient{groupID: {{ACI: aliceACI}}}

	res, err := sender(t, fake).Delete(t.Context(), app.DeleteRequest{
		Recipients: []string{aliceNumber, app.SelfRecipient, app.GroupPrefix + groupID},
		Target:     targetAt,
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	if res.Timestamp != sentAt || res.TargetTimestamp != targetAt || len(res.Results) != 3 || res.Failed() != 0 {
		t.Fatalf("got %+v", res)
	}

	sent := fake.Sent()
	if len(sent) != 2 || sent[1].GroupID != groupID {
		t.Fatalf("sent %+v, want users then the group", sent)
	}

	for _, req := range sent {
		want := signal.SendRequest{
			Recipients: req.Recipients, GroupID: req.GroupID, Timestamp: sentAt, DeleteTarget: targetAt,
		}
		if !reflect.DeepEqual(req, want) {
			t.Errorf("sent %+v, want %+v", req, want)
		}
	}
}

func TestDeleteInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  app.DeleteRequest
		want error
	}{
		{"no target", app.DeleteRequest{Recipients: []string{aliceNumber}}, app.ErrInvalidTarget},
		{"without recipients", app.DeleteRequest{Target: 1}, app.ErrNoRecipients},
		{"bad group", app.DeleteRequest{Recipients: []string{"group:x"}, Target: 1}, app.ErrInvalidRecipient},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := directory()

			_, err := sender(t, fake).Delete(t.Context(), test.req)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}

			if len(fake.Connects()) != 0 {
				t.Error("connected before validating the request")
			}
		})
	}
}
