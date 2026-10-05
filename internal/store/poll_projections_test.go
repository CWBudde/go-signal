//go:build cgo || libsignal_go

package store_test

import (
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
)

var errProjection = errors.New("projection failed")

func projectionSeed(rec store.InboxRecord) (*store.PollObservation, error) {
	return &store.PollObservation{PollKey: projectionKey(), Hash: string(rec.Event), Event: rec.Event}, nil
}

func projectionKey() store.PollKey {
	return store.PollKey{Chat: aliceChat, Author: testACI, Timestamp: ^uint64(0)}
}

func projectionReduce(_ store.PollKey, events [][]byte) ([]byte, error) {
	var out []byte
	for _, event := range events {
		out = append(out, event...)
	}

	return out, nil
}

func TestPollProjectionLifecycle(t *testing.T) {
	t.Parallel()
	dir := openDir(t, io.Discard)
	data := openAccount(t, dir)

	_, err := data.AddInboxRecord(t.Context(), store.InboxRecord{Chat: aliceChat, Event: []byte("a")})
	if err != nil {
		t.Fatal(err)
	}

	next := &store.PollObservation{PollKey: projectionKey(), Hash: "b", Event: []byte("b")}
	for range 2 {
		err = data.ProjectPolls(t.Context(), projectionSeed, projectionReduce, next)
		if err != nil {
			t.Fatal(err)
		}
	}

	_, err = data.PruneInbox(t.Context(), time.Now().Add(time.Hour), 0)
	if err != nil {
		t.Fatal(err)
	}

	err = data.Close()
	if err != nil {
		t.Fatal(err)
	}

	data = openAccount(t, dir)

	got, err := data.PollProjectionRecord(t.Context(), projectionKey())
	if err != nil || string(got) != "ab" {
		t.Fatalf("after prune/reopen = %s, %v", got, err)
	}

	other := projectionKey()
	other.Timestamp--

	got, err = data.PollProjectionRecord(t.Context(), other)
	if err != nil || got != nil {
		t.Fatalf("key isolation = %s, %v", got, err)
	}
}

func TestPollProjectionRollback(t *testing.T) {
	t.Parallel()
	data := openAccount(t, openDir(t, io.Discard))

	_, err := data.AddInboxRecord(t.Context(), store.InboxRecord{Chat: aliceChat, Event: []byte("a")})
	if err != nil {
		t.Fatal(err)
	}

	next := &store.PollObservation{PollKey: projectionKey(), Hash: "b", Event: []byte("b")}
	fail := func(store.PollKey, [][]byte) ([]byte, error) { return nil, errProjection }

	err = data.ProjectPolls(t.Context(), projectionSeed, fail, next)
	if !errors.Is(err, errProjection) {
		t.Fatalf("failure = %v", err)
	}

	got, err := data.PollProjectionRecord(t.Context(), projectionKey())
	if err != nil || got != nil {
		t.Fatalf("partial projection = %s, %v", got, err)
	}

	err = data.ProjectPolls(t.Context(), projectionSeed, projectionReduce, next)
	if err != nil {
		t.Fatal(err)
	}

	got, err = data.PollProjectionRecord(t.Context(), projectionKey())
	if err != nil || string(got) != "ab" {
		t.Fatalf("retry lost event = %s, %v", got, err)
	}
}

func TestPollProjectionConcurrent(t *testing.T) {
	t.Parallel()
	dir := openDir(t, io.Discard)
	stores := []*store.Store{openAccount(t, dir), openAccount(t, dir)}

	var workers sync.WaitGroup

	for index, event := range []string{"a", "b", "c", "a", "b", "c"} {
		data := stores[index%len(stores)]

		workers.Go(func() {
			err := data.ProjectPolls(context.Background(), projectionSeed, projectionReduce,
				&store.PollObservation{PollKey: projectionKey(), Hash: event, Event: []byte(event)})
			if err != nil {
				t.Error(err)
			}
		})
	}

	workers.Wait()

	got, err := stores[0].PollProjectionRecord(t.Context(), projectionKey())
	slices.Sort(got)

	if err != nil || string(got) != "abc" {
		t.Fatalf("concurrent = %s, %v", got, err)
	}
}
