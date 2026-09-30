//nolint:lll // Dense protocol fixtures keep input and expected results together.
package signal_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	stickerWebPType = "image/webp"
	stickerGIFType  = "image/gif"
	stickerAccount  = "sticker-account"
)

func TestStickerReferenceCheck(t *testing.T) {
	t.Parallel()

	valid := signal.StickerReference{PackID: strings.Repeat("ab", 16), PackKey: make([]byte, 32)}
	for _, testCase := range []struct {
		name string
		ref  signal.StickerReference
		bad  bool
	}{
		{"zero ID", valid, false},
		{"empty reference", signal.StickerReference{}, true},
		{"short pack key", signal.StickerReference{PackID: valid.PackID, PackKey: make([]byte, 31)}, true},
		{"uppercase", signal.StickerReference{PackID: strings.ToUpper(valid.PackID), PackKey: valid.PackKey}, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := testCase.ref.Check()
			if errors.Is(err, signal.ErrInvalidSticker) != testCase.bad {
				t.Fatalf("Check = %v", err)
			}
		})
	}
}

func TestStickerStandalone(t *testing.T) {
	t.Parallel()

	sticker := &signal.OutgoingSticker{Reference: signal.StickerReference{PackID: strings.Repeat("ab", 16), PackKey: make([]byte, 32)}}

	base := signal.SendRequest{Recipients: []signal.Recipient{{ACI: stickerAccount}}, Sticker: sticker}

	err := base.Check()
	if err != nil {
		t.Fatal(err)
	}

	for _, mutate := range []func(*signal.SendRequest){
		func(r *signal.SendRequest) { r.Body = "text" }, func(r *signal.SendRequest) { r.Attachments = []signal.UploadedAttachment{{ID: "1"}} },
		func(r *signal.SendRequest) { r.Quote = &signal.Quote{} }, func(r *signal.SendRequest) { r.Mentions = []signal.Mention{{}} },
		func(r *signal.SendRequest) { r.Reaction = &signal.OutgoingReaction{} }, func(r *signal.SendRequest) { r.DeleteTarget = 1 }, func(r *signal.SendRequest) { r.EditTarget = 1 },
	} {
		r := base
		mutate(&r)

		if !errors.Is(r.Check(), signal.ErrInvalidSticker) {
			t.Errorf("combination accepted: %+v", r)
		}
	}
}

func TestFakeStickerFetch(t *testing.T) {
	t.Parallel()

	ref := signal.StickerReference{PackID: strings.Repeat("ab", 16), PackKey: make([]byte, 32)}
	fake := &signaltest.Fake{}

	client, err := fake.Factory(context.Background(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	_, err = client.FetchSticker(context.Background(), ref)
	if !errors.Is(err, signal.ErrStickerNotFound) {
		t.Fatalf("missing = %v", err)
	}
}

func TestFakeStickerCopiesAndLifecycle(t *testing.T) {
	t.Parallel()

	ref := signal.StickerReference{PackID: strings.Repeat("ab", 16), PackKey: make([]byte, 32)}
	fake := &signaltest.Fake{Stickers: map[string]signal.StickerData{ref.PackID + ":0": {Emoji: "x", Image: signal.OutgoingAttachment{Data: []byte("image"), ContentType: stickerGIFType}}}}
	client, _ := fake.Factory(context.Background(), signal.Options{})

	got, err := client.FetchSticker(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}

	got.Image.Data[0] = 'X'
	ref.PackKey[0] = 1

	recorded := fake.FetchedStickers()
	if recorded[0].PackKey[0] != 0 {
		t.Fatal("record shares reference key")
	}

	recorded[0].PackKey[0] = 2
	if fake.FetchedStickers()[0].PackKey[0] != 0 {
		t.Fatal("returned record shares key")
	}

	got, err = client.FetchSticker(context.Background(), ref)
	if err != nil || string(got.Image.Data) != "image" {
		t.Fatal("fetch shares image", err)
	}

	client.Close()

	_, err = client.FetchSticker(context.Background(), ref)
	if !errors.Is(err, signal.ErrClosed) {
		t.Fatal(err)
	}
}

func TestStickerInboxImageRoundTrip(t *testing.T) {
	t.Parallel()

	image := &signal.Attachment{ContentType: stickerWebPType, Remote: signal.RemoteAttachment{CDNKey: "sticker-image-key", Key: []byte{1, 2}, Digest: []byte{3}}}
	evt := &signal.Message{Sticker: &signal.Sticker{PackID: "ab", Image: image}}

	blob, err := signal.MarshalEvent(evt, signal.Chat{})
	if err != nil {
		t.Fatal(err)
	}

	decoded, _ := signal.UnmarshalEvent(blob)

	got := decoded.(*signal.Message).Sticker.Image //nolint:forcetypeassert // fixture marshals a Message
	if got == nil || got.Remote.CDNKey != "sticker-image-key" || len(got.Remote.Key) != 2 {
		t.Fatalf("image %+v", got)
	}

	evt.Sticker.Image = nil

	blob, err = signal.MarshalEvent(evt, signal.Chat{})
	if err != nil {
		t.Fatal(err)
	}

	decoded, _ = signal.UnmarshalEvent(blob)

	message, ok := decoded.(*signal.Message)
	if !ok {
		t.Fatal("decoded event is not a Message")
	}

	if message.Sticker.Image != nil {
		t.Fatal("old event has image")
	}
}

func TestFakeStickerUploadOwnershipAndRecordedCopies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fake := &signaltest.Fake{Linked: []signal.Account{{ACI: stickerAccount}}}

	client, _ := fake.Factory(ctx, signal.Options{})
	defer client.Close()

	err := client.Connect(ctx, signal.SendOnly())
	if err != nil {
		t.Fatal(err)
	}

	image := signal.OutgoingAttachment{Data: []byte("image"), ContentType: stickerWebPType}

	uploaded, err := client.Upload(ctx, []signal.OutgoingAttachment{image})
	if err != nil {
		t.Fatal(err)
	}

	req := signal.SendRequest{Recipients: []signal.Recipient{{ACI: stickerAccount}}, Sticker: &signal.OutgoingSticker{Reference: signal.StickerReference{PackID: strings.Repeat("ab", 16), PackKey: make([]byte, 32)}, Image: uploaded[0]}}

	_, err = client.Send(ctx, req)
	if err != nil {
		t.Fatal(err)
	}

	req.Sticker.Reference.PackKey[0] = 1
	recorded := fake.Sent()

	recorded[0].Sticker.Reference.PackKey[0] = 2
	if fake.Sent()[0].Sticker.Reference.PackKey[0] != 0 {
		t.Fatal("recorded send shares sticker key")
	}

	image.Data[0] = 'X'
	recordedUploads := fake.Uploaded()

	recordedUploads[0].Data[0] = 'Y'
	if string(fake.Uploaded()[0].Data) != "image" {
		t.Fatal("recorded upload shares image")
	}

	req.Sticker.Image.ID = "other-client"

	_, err = client.Send(ctx, req)
	if !errors.Is(err, signal.ErrUnknownAttachment) {
		t.Fatal(err)
	}
}
