//go:build cgo || libsignal_go

package store_test

import (
	"errors"
	"io"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
)

//nolint:cyclop // Independent persistence, maximum and key-isolation assertions.
func TestPollVoteCounterPersistence(t *testing.T) {
	t.Parallel()
	dir := openDir(t, io.Discard)

	data := openAccount(t, dir)
	for _, step := range []struct{ explicit, want uint32 }{{0, 1}, {0, 2}, {9, 9}, {3, 3}, {0, 10}} {
		got, err := data.ReservePollVote(t.Context(), aliceChat, testACI, 42, step.explicit)
		if err != nil || got != step.want {
			t.Fatalf("counter = %d, %v; want %d", got, err, step.want)
		}
	}

	_, err := data.PruneInbox(t.Context(), time.Now().Add(time.Hour), 1)
	if err != nil {
		t.Fatal(err)
	}

	err = data.Close()
	if err != nil {
		t.Fatal(err)
	}

	data = openAccount(t, dir)

	got, err := data.ReservePollVote(t.Context(), aliceChat, testACI, 42, 0)
	if err != nil || got != 11 {
		t.Fatalf("reopened counter = %d, %v", got, err)
	}

	for _, key := range []struct {
		chat, author string
		timestamp    uint64
	}{
		{groupChat, testACI, 42},
		{aliceChat, aliceChat, 42},
		{aliceChat, testACI, 43},
		{aliceChat, testACI, math.MaxUint64},
		{aliceChat, testACI, math.MaxInt64},
	} {
		got, err = data.ReservePollVote(t.Context(), key.chat, key.author, key.timestamp, 0)
		if err != nil || got != 1 {
			t.Fatalf("isolated counter = %d, %v", got, err)
		}
	}
}

func TestPollVoteCounterConcurrent(t *testing.T) {
	t.Parallel()
	data := openAccount(t, openDir(t, io.Discard))

	var workers sync.WaitGroup

	counts := make(chan uint32, 24)

	for range 24 {
		workers.Go(func() {
			got, err := data.ReservePollVote(t.Context(), aliceChat, testACI, 42, 0)
			if err != nil {
				t.Error(err)
				return
			}

			counts <- got
		})
	}

	workers.Wait()
	close(counts)

	seen := make(map[uint32]bool)
	for count := range counts {
		if count < 1 || count > 24 || seen[count] {
			t.Errorf("duplicate/outside counter %d", count)
		}

		seen[count] = true
	}

	if len(seen) != 24 {
		t.Fatalf("got %d unique counters", len(seen))
	}
}

func TestPollVoteCounterOverflow(t *testing.T) {
	t.Parallel()
	data := openAccount(t, openDir(t, io.Discard))

	got, err := data.ReservePollVote(t.Context(), aliceChat, testACI, 42, math.MaxUint32)
	if err != nil || got != math.MaxUint32 {
		t.Fatalf("maximum = %d, %v", got, err)
	}

	_, err = data.ReservePollVote(t.Context(), aliceChat, testACI, 42, 0)
	if !errors.Is(err, store.ErrPollVoteExhausted) {
		t.Fatalf("overflow = %v", err)
	}
}
