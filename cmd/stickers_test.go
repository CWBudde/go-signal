package cmd_test

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	stickerPackID  = "0123456789abcdef0123456789abcdef"
	stickerPackKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	stickerPackURL = "https://signal.art/addstickers/#pack_id=" + stickerPackID + "&pack_key=" + stickerPackKey
	stickerWebP    = "image/webp"
	brokenSticker  = "sticker-broken"
)

func TestSendSticker(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := sendFake()
			fake.Stickers = map[string]signal.StickerData{stickerPackID + ":0": {
				Emoji: "😀", Image: signal.OutgoingAttachment{Data: []byte("WEBP"), ContentType: stickerWebP},
			}}

			out, err := runSend(t, fake, "", "-o", format, sendCmd, aliceNumber, app.SelfRecipient, "--group", groupID,
				"--sticker-pack", stickerPackURL, "--sticker-id", "0")
			if err != nil {
				t.Fatalf("sticker send: %v", err)
			}

			sent := fake.Sent()
			if len(sent) != 2 || len(fake.FetchedStickers()) != 1 || len(fake.Uploaded()) != 1 {
				t.Fatalf("sent %d requests; want two requests sharing one fetch/upload", len(sent))
			}

			checkStickerRequests(t, sent)

			if strings.Contains(out, stickerPackKey) {
				t.Fatal("send output exposed the pack key")
			}

			golden(t, "send_sticker_"+format, out)
		})
	}
}

func checkStickerRequests(t *testing.T, requests []signal.SendRequest) {
	t.Helper()

	key, err := hex.DecodeString(stickerPackKey)
	if err != nil {
		t.Fatal(err)
	}

	want := &signal.OutgoingSticker{
		Reference: signal.StickerReference{PackID: stickerPackID, PackKey: key, StickerID: 0},
		Emoji:     "😀",
		Image:     signal.UploadedAttachment{ID: "upload-1", ContentType: stickerWebP, Size: 4},
	}
	for _, req := range requests {
		if !reflect.DeepEqual(req.Sticker, want) || req.Timestamp != sentAt || req.Body != "" || len(req.Attachments) != 0 {
			t.Fatal("sticker content or ID zero was not preserved")
		}
	}
}

func TestSendStickerPartialFailure(t *testing.T) {
	t.Parallel()

	fake := sendFake()
	fake.Stickers = map[string]signal.StickerData{stickerPackID + ":0": {
		Image: signal.OutgoingAttachment{Data: []byte("WEBP"), ContentType: stickerWebP},
	}}
	fake.SendFailures = map[string]error{aliceACI: errUnreachable}

	out, err := runSend(t, fake, "", "-o", formatJSON, sendCmd, aliceNumber, app.SelfRecipient,
		"--sticker-pack", stickerPackURL, "--sticker-id", "0")
	if !errors.Is(err, app.ErrSendFailed) ||
		!strings.Contains(out, `"success":false`) || !strings.Contains(out, `"success":true`) {
		t.Fatalf("partial result %q, %v", out, err)
	}

	golden(t, "send_sticker_failed_json", out)
}

// Invalid sticker flags must fail before opening even an unavailable account.
func TestSendStickerPreflight(t *testing.T) {
	t.Parallel()

	for _, extra := range [][]string{
		{"--message="},
		{"--stdin"},
		{"--attach", "/missing"},
		{"--quote="},
		{"--quote-text="},
		{"--edit", "0"},
		{"--style", "0:1:bold"},
	} {
		t.Run(extra[0], func(t *testing.T) {
			t.Parallel()

			fake := &signaltest.Fake{OpenErr: errUnreachable}

			args := append([]string{sendCmd, app.SelfRecipient, "--sticker-pack", stickerPackURL, "--sticker-id", "0"}, extra...)

			out, err := runSend(t, fake, "", args...)
			if !errors.Is(err, signal.ErrInvalidSticker) || out != "" {
				t.Fatalf("send = %q, %v; want invalid sticker without output", out, err)
			}
		})
	}
}

func TestSendStickerInvalidLink(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{OpenErr: errUnreachable}

	invalidURL := "https://example.org/#pack_key=" + stickerPackKey

	out, err := runSend(t, fake, "", sendCmd, app.SelfRecipient, "--sticker-pack", invalidURL,
		"--sticker-id", "0")
	if !errors.Is(err, signal.ErrInvalidSticker) || out != "" || strings.Contains(err.Error(), stickerPackKey) {
		t.Fatalf("invalid link: output %q, error %v", out, err)
	}
}

func TestSendStickerRequiresBothFlags(t *testing.T) {
	t.Parallel()

	for _, flags := range [][]string{{"--sticker-pack", stickerPackURL}, {"--sticker-id", "0"}} {
		t.Run(flags[0], func(t *testing.T) {
			t.Parallel()

			fake := &signaltest.Fake{OpenErr: errUnreachable}

			out, err := runSend(t, fake, "", append([]string{sendCmd, app.SelfRecipient}, flags...)...)
			if err == nil || errors.Is(err, errUnreachable) || out != "" {
				t.Fatalf("missing companion flag: output %q, error %v", out, err)
			}
		})
	}
}

func stickerMessages() []signal.Event {
	image := func(key string) *signal.Attachment {
		return &signal.Attachment{
			ContentType: stickerWebP, Size: 3,
			Remote: signal.RemoteAttachment{CDNKey: key, Key: []byte("private-image-key")},
		}
	}
	env := func(n int) signal.Envelope {
		return signal.Envelope{Sender: signal.Recipient{ACI: aliceACI}, Timestamp: at(n)}
	}

	return []signal.Event{
		&signal.Message{Envelope: env(1), Sticker: &signal.Sticker{
			PackID: stickerPackID, StickerID: 0,
			Emoji: "😀", Image: image("good"),
		}},
		&signal.Message{Envelope: env(2), Sticker: &signal.Sticker{
			PackID: stickerPackID, StickerID: 7,
			Image: image(brokenSticker),
		}},
		&signal.Message{Envelope: env(3), Sticker: &signal.Sticker{PackID: stickerPackID, StickerID: 9}},
		&signal.Message{Envelope: env(4), Body: "still receiving"},
	}
}

func TestReceiveStickerDownloads(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "stickers")
			fake := &signaltest.Fake{
				Linked:      []signal.Account{*testAccount()},
				Attachments: map[string][]byte{"good": []byte("IMG")}, DownloadErrs: map[string]error{brokenSticker: errCDN},
			}
			out := receiveAllFrom(t, fake, stickerMessages(), "-o", format, "--download-attachments", dir)

			data, err := os.ReadFile(filepath.Join(dir, "1789907401000-sticker-0.webp"))
			if err != nil || string(data) != "IMG" {
				t.Fatalf("sticker download = %q, %v", data, err)
			}

			if strings.Contains(out, "private-image-key") || !strings.Contains(out, "still receiving") {
				t.Fatalf("unsafe or incomplete receive output: %q", out)
			}

			golden(t, "receive_stickers_"+format, strings.ReplaceAll(out, dir, "$DIR"))
		})
	}
}

func TestReceiveStickerMetadataWithoutDownloads(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}, DownloadErrs: map[string]error{"good": errCDN}}

	out := receiveAllFrom(t, fake, stickerMessages()[:1], "-o", formatJSON)
	if !strings.Contains(out, `"image":{"contentType":"image/webp"`) || strings.Contains(out, "downloadError") ||
		strings.Contains(out, "private-image-key") {
		t.Fatalf("metadata without download: %q", out)
	}
}
