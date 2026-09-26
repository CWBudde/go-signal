//go:build cgo

package store_test

import (
	"io"
	"slices"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/store"
	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/types"
)

func TestBlockOverrides(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := openAccount(t, openDir(t, io.Discard))
	setAt := time.Date(2026, 9, 20, 12, 30, 0, 0, time.UTC)

	for _, override := range []store.BlockOverride{
		{ACI: "b", Blocked: true, SetAt: setAt},
		{ACI: "a", Blocked: true, SetAt: setAt},
		// Replaces the first one.
		{ACI: "b", Blocked: false, SetAt: setAt.Add(time.Minute), StorageVersion: 7},
	} {
		err := data.SetBlockOverride(ctx, override)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := data.DeleteBlockOverride(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}

	err = data.DeleteBlockOverride(ctx, "missing")
	if err != nil {
		t.Fatal(err)
	}

	got, err := data.BlockOverrides(ctx)
	want := []store.BlockOverride{{ACI: "b", Blocked: false, SetAt: setAt.Add(time.Minute), StorageVersion: 7}}

	if err != nil || !slices.Equal(got, want) {
		t.Errorf("BlockOverrides = %+v, %v; want %+v", got, err, want)
	}
}

func TestBlockedACIs(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := openAccount(t, openDir(t, io.Discard))

	own := uuid.MustParse(testACI)

	err := data.Devices.PutDevice(ctx, newDevice(t, own))
	if err != nil {
		t.Fatal(err)
	}

	device, err := data.Devices.DeviceByACI(ctx, own)
	if err != nil {
		t.Fatal(err)
	}

	blocked := []uuid.UUID{
		uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"), uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
	}

	for _, rcpt := range []*types.Recipient{
		{ACI: blocked[0], Blocked: true},
		{ACI: blocked[1], Blocked: true, E164: "+15550101"},
		{ACI: uuid.New(), E164: "+15550102"},
		// Blocked, but only known by PNI.
		{PNI: uuid.New(), E164: "+15550103", Blocked: true},
	} {
		err = device.RecipientStore.StoreRecipient(ctx, rcpt)
		if err != nil {
			t.Fatal(err)
		}
	}

	got, err := data.BlockedACIs(ctx, testACI)
	want := []string{blocked[1].String(), blocked[0].String()}

	if err != nil || !slices.Equal(got, want) {
		t.Errorf("BlockedACIs = %v, %v; want %v", got, err, want)
	}
}
