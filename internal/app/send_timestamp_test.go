package app_test

import (
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

type timestampOutcome struct {
	result app.SendResult
	err    error
}

func TestSendTimestampFixedClock(t *testing.T) {
	t.Parallel()

	fake := directory()
	sender := sender(t, fake)

	for i, want := range []uint64{sentAt, sentAt + 1, sentAt + 2} {
		res, err := sender.Send(t.Context(), app.SendRequest{Recipients: []string{aliceNumber}, Body: body})
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}

		if res.Timestamp != want {
			t.Errorf("send %d timestamp = %d; want %d", i, res.Timestamp, want)
		}

		if sent := fake.Sent(); sent[i].Timestamp != res.Timestamp {
			t.Errorf("send %d request timestamp = %d; result = %d", i, sent[i].Timestamp, res.Timestamp)
		}
	}
}

func TestSendTimestampConcurrent(t *testing.T) {
	t.Parallel()

	fake := directory()
	sender := sender(t, fake)
	ctx := t.Context()

	const sends = 32

	outcomes := make(chan timestampOutcome, sends)
	start := make(chan struct{})

	for range sends {
		go func() {
			<-start

			res, err := sender.Send(ctx, app.SendRequest{Recipients: []string{aliceNumber}, Body: body})
			outcomes <- timestampOutcome{result: res, err: err}
		}()
	}

	close(start)

	seen := make(map[uint64]bool, sends)

	for range sends {
		got := <-outcomes
		if got.err != nil {
			t.Fatalf("concurrent send: %v", got.err)
		}

		if seen[got.result.Timestamp] {
			t.Errorf("concurrent sends reused timestamp %d", got.result.Timestamp)
		}

		seen[got.result.Timestamp] = true
	}

	for offset := range sends {
		want := uint64(sentAt + offset)
		if !seen[want] {
			t.Errorf("missing timestamp %d", want)
		}
	}

	requests := fake.Sent()
	if len(requests) != sends {
		t.Fatalf("sent %d requests; want %d", len(requests), sends)
	}

	for _, req := range requests {
		if !seen[req.Timestamp] {
			t.Errorf("request timestamp %d missing from send results", req.Timestamp)
		}

		delete(seen, req.Timestamp)
	}
}

func TestSendTimestampClockRollback(t *testing.T) {
	t.Parallel()

	fake := directory()

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	clock := time.UnixMilli(sentAt)
	sender := app.New(client, app.WithClock(func() time.Time { return clock }))

	for i, step := range []struct {
		now  int64
		want uint64
	}{
		{sentAt, sentAt},
		{sentAt - 1000, sentAt + 1},
		{sentAt + 1000, sentAt + 1000},
	} {
		clock = time.UnixMilli(step.now)

		res, err := sender.Send(t.Context(), app.SendRequest{Recipients: []string{aliceNumber}, Body: body})
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}

		if res.Timestamp != step.want {
			t.Errorf("send %d timestamp = %d; want %d", i, res.Timestamp, step.want)
		}
	}
}

func TestSendTimestampSharedAcrossTargets(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.Groups = map[string][]signal.Recipient{groupID: {{ACI: aliceACI}}}
	sender := sender(t, fake)

	_, err := sender.Send(t.Context(), app.SendRequest{Recipients: []string{aliceNumber}, Body: body})
	if err != nil {
		t.Fatalf("first send: %v", err)
	}

	res, err := sender.Send(t.Context(), app.SendRequest{
		Recipients: []string{aliceNumber, bobUsername, app.GroupPrefix + groupID}, Body: body,
	})
	if err != nil {
		t.Fatalf("multi-target send: %v", err)
	}

	if res.Timestamp != sentAt+1 || len(res.Results) != 3 {
		t.Fatalf("multi-target result = %+v; want three targets at timestamp %d", res, sentAt+1)
	}

	requests := fake.Sent()
	if len(requests) != 3 || len(requests[1].Recipients) != 2 || requests[2].GroupID != groupID {
		t.Fatalf("sent %+v; want first send, then two users and a group", requests)
	}

	for _, req := range requests[1:] {
		if req.Timestamp != res.Timestamp {
			t.Errorf("target request timestamp = %d; result = %d", req.Timestamp, res.Timestamp)
		}
	}
}
