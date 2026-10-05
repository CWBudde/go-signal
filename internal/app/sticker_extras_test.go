package app_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

const expiredStickerImage = "expired-sticker-image"

//nolint:cyclop,funlen,gocognit // exercises fallback and non-fallback download cases together.
func TestStickerExpiredFallback(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"expired", "cover-only expired", "no embedded pointer", "bad digest", "canceled"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fake := directory()

			ref := stickerReference()
			if name == "cover-only expired" {
				ref.StickerID = 8
			}

			fake.Stickers = map[string]signal.StickerData{
				fmt.Sprintf("%s:%d", ref.PackID, ref.StickerID): {
					Image: signal.OutgoingAttachment{
						Data:        []byte("fallback"),
						ContentType: "image/gif",
					},
				},
			}
			image := attachment(expiredStickerImage, "ignored")
			msg := &signal.Message{
				Envelope: signal.Envelope{Timestamp: msgTS},
				Sticker: &signal.Sticker{
					PackID:    ref.PackID,
					PackKey:   ref.PackKey,
					StickerID: ref.StickerID,
					Image:     &image,
				},
			}

			var want error

			switch name {
			case "no embedded pointer":
				msg.Sticker.Image = nil
			case "bad digest":
				want = signal.ErrAttachmentInvalid
			case "canceled":
				want = context.Canceled
			}

			if want != nil {
				fake.DownloadErrs = map[string]error{expiredStickerImage: want}
			}

			dir := t.TempDir()

			got := open(t, fake).SaveMessageMedia(t.Context(), app.SaveAttachmentsRequest{Dir: dir, Message: msg})
			if want != nil {
				if got.Sticker == nil || !errors.Is(got.Sticker.Err, want) || len(fake.FetchedStickers()) != 0 {
					t.Fatalf("failure %+v", got.Sticker)
				}

				return
			}

			if got.Sticker == nil || got.Sticker.Err != nil {
				t.Fatalf("fallback %+v", got.Sticker)
			}

			if len(fake.FetchedStickers()) != 1 || fake.FetchedStickers()[0].StickerID != ref.StickerID {
				t.Fatal("fallback selected wrong sticker ID")
			}

			if filepath.Ext(got.Sticker.Path) != ".gif" {
				t.Fatalf("wrong MIME extension %s", got.Sticker.Path)
			}

			data, err := os.ReadFile(got.Sticker.Path)
			if err != nil || string(data) != "fallback" {
				t.Fatalf("saved %q %v", data, err)
			}
		})
	}
}
