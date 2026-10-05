package cmd_test

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func packFixture(t *testing.T, fake *signaltest.Fake) {
	t.Helper()

	key, err := hex.DecodeString(stickerPackKey)
	if err != nil {
		t.Fatal(err)
	}

	fake.StickerPackFixtures = map[string]signal.StickerPack{stickerPackID: {
		Reference: signal.StickerReference{PackID: stickerPackID, PackKey: key},
		Title:     "Animals", Author: "Artist", CoverID: new(uint32(0)),
		Stickers: []signal.StickerPackItem{
			{
				ID: 0,
				Data: signal.StickerData{
					Emoji: "😀",
					Image: signal.OutgoingAttachment{Data: []byte("WEBP"), ContentType: stickerWebP},
				},
			},
		},
	}}
}

func TestStickerPacksCommands(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := sendFake()
			packFixture(t, fake)

			out, err := runSend(t, fake, "", "-o", format, "stickers", "install", stickerPackURL)
			if err != nil {
				t.Fatalf("install %v", err)
			}

			if strings.Contains(out, stickerPackKey) {
				t.Fatal("key exposed")
			}

			golden(t, "stickers_install_"+format, out)

			out, err = runSend(t, fake, "", "-o", format, "stickers", "list")
			if err != nil {
				t.Fatal(err)
			}

			golden(t, "stickers_list_"+format, out)

			fake.FetchStickerErr = signal.ErrStickerNotFound

			_, err = runSend(t, fake, "", sendCmd, aliceNumber, "--sticker-pack", stickerPackURL, "--sticker-id", "0")
			if err != nil || len(fake.FetchedStickers()) != 0 || len(fake.Uploaded()) != 1 {
				t.Fatalf("cached send %v", err)
			}
		})
	}
}

func TestStickerPacksInvalidLinkPreflight(t *testing.T) {
	t.Parallel()

	fake := sendFake()

	_, err := runSend(t, fake, "", "stickers", "install", "SECRET")
	if err == nil || strings.Contains(err.Error(), "SECRET") || len(fake.Opened()) != 0 {
		t.Fatalf("preflight %v opened %d", err, len(fake.Opened()))
	}
}

func TestStickerPacksAccountSelection(t *testing.T) {
	t.Parallel()

	fake := sendFake()
	packFixture(t, fake)
	other := fake.Linked[0]
	other.ACI = "aaaaaaaa-9999-4999-8999-aaaaaaaaaaaa"
	other.Number = "+4915100000099"
	fake.Linked = append(fake.Linked, other)

	_, err := runSend(t, fake, "", "-a", other.Number, "stickers", "install", stickerPackURL)
	if err != nil {
		t.Fatal(err)
	}

	first, err := runSend(t, fake, "", "-a", fake.Linked[0].Number, "-o", formatJSON, "stickers", "list")
	if err != nil || strings.Contains(first, stickerPackID) {
		t.Fatalf("wrong account cache %s %v", first, err)
	}

	second, err := runSend(t, fake, "", "-a", other.Number, "-o", formatJSON, "stickers", "list")
	if err != nil || !strings.Contains(second, stickerPackID) {
		t.Fatalf("selected cache %s %v", second, err)
	}
}
