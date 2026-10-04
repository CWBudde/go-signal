//go:build cgo || libsignal_go

package store_test

import (
	"io"
	"testing"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/rs/zerolog"
)

const timerPeer = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func TestChatTimerUnknownAndDisabled(t *testing.T) {
	t.Parallel()
	data := openAccount(t, openDir(t, io.Discard))

	got, found, err := data.ChatTimer(t.Context(), timerPeer)
	if err != nil || found || got != (store.ChatTimerRecord{}) {
		t.Fatalf("unknown timer = %+v, %v, %v", got, found, err)
	}

	for _, rec := range []store.ChatTimerRecord{
		{ACI: timerPeer, Seconds: 60, Version: 1},
		{ACI: timerPeer, Version: 2},
	} {
		err := data.MergeChatTimer(t.Context(), rec)
		if err != nil {
			t.Fatal(err)
		}
	}

	assertChatTimer(t, data, 0, 2)
}

func TestChatTimerHighWater(t *testing.T) {
	t.Parallel()

	data := openAccount(t, openDir(t, io.Discard))
	for _, rec := range []store.ChatTimerRecord{
		{ACI: timerPeer, Seconds: 60, Version: 3},
		{ACI: timerPeer, Seconds: 60, Version: 5},
		{ACI: timerPeer, Version: 4},
	} {
		err := data.MergeChatTimer(t.Context(), rec)
		if err != nil {
			t.Fatal(err)
		}
	}

	assertChatTimer(t, data, 60, 5)
}

func assertChatTimer(t *testing.T, data *store.Store, seconds, version uint32) {
	t.Helper()

	got, found, err := data.ChatTimer(t.Context(), timerPeer)
	if err != nil || !found || got != (store.ChatTimerRecord{ACI: timerPeer, Seconds: seconds, Version: version}) {
		t.Fatalf("timer = %+v, %v, %v; want %d/version%d", got, found, err, seconds, version)
	}
}

func TestChatTimerOrderingAndRange(t *testing.T) {
	t.Parallel()

	data := openAccount(t, openDir(t, io.Discard))
	for _, rec := range []store.ChatTimerRecord{
		{ACI: timerPeer, Seconds: 4294967295, Version: 4294967295},
		{ACI: timerPeer, Seconds: 1, Version: 4294967295},
		{ACI: timerPeer, Version: 4294967294},
	} {
		err := data.MergeChatTimer(t.Context(), rec)
		if err != nil {
			t.Fatal(err)
		}
	}

	assertChatTimer(t, data, 4294967295, 4294967295)
}

func TestChatTimerLegacy(t *testing.T) {
	t.Parallel()
	data := openAccount(t, openDir(t, io.Discard))

	err := data.MergeChatTimer(t.Context(), store.ChatTimerRecord{ACI: timerPeer, Seconds: 60})
	if err != nil {
		t.Fatal(err)
	}

	assertChatTimer(t, data, 60, 0)

	for _, rec := range []store.ChatTimerRecord{
		{ACI: timerPeer},
		{ACI: timerPeer, Seconds: 120, Version: 1},
		{ACI: timerPeer},
	} {
		err := data.MergeChatTimer(t.Context(), rec)
		if err != nil {
			t.Fatal(err)
		}
	}

	assertChatTimer(t, data, 120, 1)
}

func TestChatTimersBatchOrder(t *testing.T) {
	t.Parallel()

	for _, versions := range [][]uint32{{1, 5, 3}, {3, 1, 5}, {5, 3, 1}} {
		data := openAccount(t, openDir(t, io.Discard))

		records := make([]store.ChatTimerRecord, 0, len(versions))
		for _, version := range versions {
			records = append(records, store.ChatTimerRecord{ACI: timerPeer, Seconds: version * 60, Version: version})
		}

		err := data.MergeChatTimers(t.Context(), records)
		if err != nil {
			t.Fatal(err)
		}

		assertChatTimer(t, data, 300, 5)
	}
}

func TestChatTimersBatchAtomic(t *testing.T) {
	t.Parallel()

	const other = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

	for _, sqlFailure := range []bool{false, true} {
		data := openAccount(t, openDir(t, io.Discard))

		err := data.MergeChatTimer(t.Context(), store.ChatTimerRecord{ACI: timerPeer, Seconds: 60, Version: 1})
		if err != nil {
			t.Fatal(err)
		}

		second := other
		if sqlFailure {
			err := data.FailChatTimerWrite(t.Context(), other)
			if err != nil {
				t.Fatal(err)
			}
		} else {
			second = "invalid"
		}

		err = data.MergeChatTimers(t.Context(), []store.ChatTimerRecord{
			{ACI: timerPeer, Version: 2},
			{ACI: second, Seconds: 120, Version: 1},
		})
		if err == nil {
			t.Fatal("batch succeeded despite invalid second row")
		}

		assertChatTimer(t, data, 60, 1)

		_, found, err := data.ChatTimer(t.Context(), other)
		if err != nil || found {
			t.Fatalf("failed row found=%v, err=%v", found, err)
		}
	}
}

func TestChatTimerRestartAndAccounts(t *testing.T) {
	t.Parallel()
	dir := openDir(t, io.Discard)
	data := openAccount(t, dir)

	err := data.MergeChatTimer(t.Context(), store.ChatTimerRecord{ACI: timerPeer, Version: 2})
	if err != nil {
		t.Fatal(err)
	}

	err = data.Close()
	if err != nil {
		t.Fatal(err)
	}

	data = openAccount(t, dir)
	assertChatTimer(t, data, 0, 2)

	other, err := dir.OpenAccount(t.Context(), timerPeer, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		err := other.Close()
		if err != nil {
			t.Error(err)
		}
	})

	_, found, err := other.ChatTimer(t.Context(), timerPeer)
	if err != nil || found {
		t.Fatalf("other account found=%v, err=%v", found, err)
	}

	err = other.MergeChatTimer(t.Context(), store.ChatTimerRecord{ACI: timerPeer, Seconds: 60, Version: 3})
	if err != nil {
		t.Fatal(err)
	}

	assertChatTimer(t, other, 60, 3)
	assertChatTimer(t, data, 0, 2)
}

func TestChatTimerInvalidACI(t *testing.T) {
	t.Parallel()

	data := openAccount(t, openDir(t, io.Discard))
	for _, aci := range []string{
		"", "invalid", "00000000-0000-0000-0000-000000000000",
		"AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", "aaaaaaaaaaaa4aaa8aaaaaaaaaaaaaaa", "PNI:" + timerPeer,
	} {
		t.Run(aci, func(t *testing.T) {
			t.Parallel()

			_, _, err := data.ChatTimer(t.Context(), aci)
			if err == nil {
				t.Fatal("read accepted invalid ACI")
			}

			err = data.MergeChatTimer(t.Context(), store.ChatTimerRecord{ACI: aci})
			if err == nil {
				t.Fatal("merge accepted invalid ACI")
			}
		})
	}
}
