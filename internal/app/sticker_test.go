package app_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	stickerMIME   = "image/webp"
	stickerSelf   = "self"
	noStickerCase = "no sticker"
)

func stickerReference() signal.StickerReference {
	return signal.StickerReference{PackID: strings.Repeat("ab", 16), PackKey: make([]byte, 32), StickerID: 7}
}

func TestParseStickerPackURL(t *testing.T) {
	t.Parallel()

	packID, key := strings.Repeat("AB", 16), strings.Repeat("CD", 32)
	valid := "https://signal.art/addstickers/#pack_id=" + packID + "&pack_key=" + key

	ref, err := app.ParseStickerPackURL(valid)
	if err != nil || ref.PackID != strings.ToLower(packID) || len(ref.PackKey) != 32 {
		t.Fatalf("reference %+v, error %v", ref, err)
	}

	for _, raw := range []string{
		strings.Replace(valid, "https:", "http:", 1),
		strings.Replace(valid, "signal.art", "evil.test", 1),
		strings.Replace(valid, "signal.art", "user@signal.art", 1),
		strings.Replace(valid, "signal.art", "signal.art:444", 1),
		strings.Replace(valid, "addstickers/", "other/", 1),
		valid + "&pack_id=" + packID,
		valid + "&pack_key=" + key,
		strings.Replace(valid, key, "SECRET", 1),
		"%",
	} {
		_, err := app.ParseStickerPackURL(raw)
		if !errors.Is(err, signal.ErrInvalidSticker) || strings.Contains(err.Error(), key) ||
			strings.Contains(err.Error(), "SECRET") {
			t.Errorf("invalid link error %v", err)
		}
	}
}

func TestSendStickerPreflight(t *testing.T) {
	t.Parallel()

	for name, change := range map[string]func(*app.SendRequest){
		"body":              func(r *app.SendRequest) { r.Body = "hi" },
		"whitespace":        func(r *app.SendRequest) { r.Body = " " },
		"attachment":        func(r *app.SendRequest) { r.Attachments = []string{"missing"} },
		"quote":             func(r *app.SendRequest) { r.Quote = "self:1" },
		"quote text":        func(r *app.SendRequest) { r.QuoteText = "quote" },
		"edit":              func(r *app.SendRequest) { r.EditTarget = 1 },
		"invalid reference": func(r *app.SendRequest) { r.Sticker.PackID = "not-hex" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fake := directory()
			ref := stickerReference()
			req := app.SendRequest{Recipients: []string{aliceNumber}, Sticker: &ref}
			change(&req)

			_, err := sender(t, fake).Send(t.Context(), req)
			if !errors.Is(err, signal.ErrInvalidSticker) {
				t.Errorf("error %v", err)
			}

			if len(fake.Connects()) != 0 {
				t.Fatal("connected before validation")
			}
		})
	}
}

func TestSaveStickerMedia(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.Attachments = map[string][]byte{"sticker": []byte("image"), "regular": []byte("regular")}
	image := attachment("sticker", "../../outside")
	image.ContentType = "image/apng"
	msg := &signal.Message{
		Envelope:    signal.Envelope{Timestamp: msgTS},
		Attachments: []signal.Attachment{attachment("regular", "photo.jpg")},
		Sticker:     &signal.Sticker{StickerID: 7, Image: &image},
	}
	dir := t.TempDir()
	name := filepath.Join(dir, "1789907401000-sticker-7.apng")

	outside := filepath.Join(t.TempDir(), "outside")

	err := os.WriteFile(outside, []byte("keep"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Symlink(outside, name)
	if err != nil {
		t.Fatal(err)
	}

	got := open(t, fake).SaveMessageMedia(t.Context(), app.SaveAttachmentsRequest{Dir: dir, Message: msg})
	if len(got.Attachments) != 1 || got.Sticker == nil {
		t.Fatalf("media %+v", got)
	}

	wantFile(t, got.Attachments[0], filepath.Join(dir, "1789907401000-1-photo.jpg"), "regular")
	wantFile(t, *got.Sticker, filepath.Join(dir, "1789907401000-sticker-7-2.apng"), "image")

	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "keep" {
		t.Fatal("overwrote symlink target")
	}

	msg.Sticker.Image = nil

	got = open(t, fake).SaveMessageMedia(t.Context(), app.SaveAttachmentsRequest{Dir: dir, Message: msg})
	if got.Sticker != nil {
		t.Fatal("historical metadata downloaded")
	}
}

//nolint:cyclop // verify content and partial outcomes together
func TestSendStickerReusesUpload(t *testing.T) {
	t.Parallel()

	fake := directory()
	ref := stickerReference()
	fake.Stickers = map[string]signal.StickerData{
		ref.PackID + ":7": {Image: signal.OutgoingAttachment{Data: []byte("image"), ContentType: stickerMIME}},
	}
	fake.Groups = map[string][]signal.Recipient{groupID: {{ACI: testAccount().ACI}, {ACI: aliceACI}, {ACI: bobACI}}}
	fake.SendFailures = map[string]error{bobACI: errBoom}

	res, err := sender(t, fake).Send(t.Context(), app.SendRequest{
		Recipients: []string{aliceNumber, bobUsername, stickerSelf, app.GroupPrefix + groupID},
		Sticker:    &ref,
	})
	if !errors.Is(err, app.ErrSendFailed) || len(res.Results) != 4 || res.Failed() != 2 {
		t.Fatalf("result %+v, error %v", res, err)
	}

	if len(fake.FetchedStickers()) != 1 || len(fake.Uploaded()) != 1 {
		t.Fatal("sticker must be fetched and uploaded once")
	}

	sent := fake.Sent()
	if len(sent) != 2 {
		t.Fatalf("sent %+v", sent)
	}

	for _, req := range sent {
		if req.Sticker == nil || req.Sticker.Reference.StickerID != 7 || req.Sticker.Emoji != "" || req.Timestamp != sentAt ||
			len(req.Attachments) != 0 {
			t.Fatalf("sent %+v", req)
		}
	}

	if sent[0].Sticker.Image != sent[1].Sticker.Image {
		t.Fatal("upload was not reused")
	}
}

//nolint:cyclop // table covers each send phase
func TestSendStickerGuardsBeforeFetch(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"unresolved", "allowlist", "fetch failure", "upload", "invalid target", "missing targets",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fake := directory()
			ref := stickerReference()
			fake.Stickers = map[string]signal.StickerData{
				ref.PackID + ":7": {Image: signal.OutgoingAttachment{Data: []byte("image"), ContentType: stickerMIME}},
			}
			a := sender(t, fake)
			recipients := []string{aliceNumber}
			want := errBoom
			fetches := 1

			switch name {
			case "unresolved":
				recipients = append(recipients, "+4915100000000")
				want = signal.ErrNotOnSignal
				fetches = 0
			case "allowlist":
				a = restricted(t, fake, bobACI)
				want = app.ErrRecipientNotAllowed
				fetches = 0
			case "fetch failure":
				fake.FetchStickerErr = errBoom
			case "upload":
				fake.UploadErr = errBoom
			case "invalid target":
				recipients = []string{"invalid"}
				want = app.ErrInvalidRecipient
				fetches = 0
			case "missing targets":
				recipients = nil
				want = app.ErrNoRecipients
				fetches = 0
			}

			_, err := a.Send(t.Context(), app.SendRequest{Recipients: recipients, Sticker: &ref})
			if !errors.Is(err, want) {
				t.Fatalf("error %v, want %v", err, want)
			}

			if len(fake.FetchedStickers()) != fetches || len(fake.Sent()) != 0 || len(fake.Uploaded()) != 0 {
				t.Fatal("unexpected fetch, upload, or send")
			}
		})
	}
}

//nolint:cyclop // distinct download failure cases
func TestSaveStickerMediaFailures(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"download", "invalid download directory", noStickerCase} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fake := directory()
			fake.Attachments = map[string][]byte{"regular": []byte("ok")}
			image := attachment("missing", "evil")
			msg := &signal.Message{
				Envelope:    signal.Envelope{Timestamp: msgTS},
				Attachments: []signal.Attachment{attachment("regular", "regular")},
				Sticker:     &signal.Sticker{Image: &image},
			}

			dir := t.TempDir()
			if name == "invalid download directory" {
				dir = filepath.Join(dir, "file")

				err := os.WriteFile(dir, nil, 0o600)
				if err != nil {
					t.Fatal(err)
				}
			}

			if name == noStickerCase {
				msg.Sticker = nil
			}

			got := open(t, fake).SaveMessageMedia(t.Context(), app.SaveAttachmentsRequest{Dir: dir, Message: msg})
			if name == noStickerCase {
				if got.Sticker != nil {
					t.Fatal("unexpected sticker")
				}

				return
			}

			if got.Sticker == nil || got.Sticker.Err == nil || got.Sticker.Path != "" {
				t.Fatalf("sticker %+v", got.Sticker)
			}

			if name == "download" && got.Attachments[0].Err != nil {
				t.Fatal("sticker failure affected regular attachment")
			}
		})
	}
}
