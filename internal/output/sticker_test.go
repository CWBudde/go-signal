package output_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
)

var (
	errStickerImage = errors.New("bad\nimage")
	errRegularImage = errors.New("regular failed")
)

func stickerMessage() *signal.Message {
	msg := incoming("")
	msg.Sticker = &signal.Sticker{
		PackID: "public-pack", PackKey: []byte("secret-pack-key"), StickerID: 7, Emoji: "🙂",
		Image: &signal.Attachment{
			ContentType: "image/webp", Filename: "sticker.webp", Size: 123, Caption: "caption",
			Remote: signal.RemoteAttachment{
				CDNKey: "secret-cdn", CDNID: 91, CDNNumber: 3, Key: []byte("secret-key"), Digest: []byte("secret-digest"),
			},
		},
	}

	return msg
}

func assertStickerJSON(t *testing.T, got, want string) {
	t.Helper()

	var doc struct {
		Version int             `json:"version"`
		Sticker json.RawMessage `json:"sticker"`
	}

	err := json.Unmarshal([]byte(got), &doc)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Version != 1 || string(doc.Sticker) != want {
		t.Errorf("got version %d sticker %s, want version 1 sticker %s", doc.Version, doc.Sticker, want)
	}

	for _, secret := range []string{
		"secret-pack-key", "secret-cdn", "secret-key", "secret-digest", "Remote", "cdn", "digest", "packKey",
	} {
		if strings.Contains(got, secret) {
			t.Errorf("leaked %q: %s", secret, got)
		}
	}
}

func TestStickerEventImageMetadata(t *testing.T) {
	t.Parallel()

	msg := stickerMessage()
	assertStickerJSON(t, renderEvent(t, output.JSON, msg), `{"packId":"public-pack","stickerId":7,"emoji":"🙂",`+
		`"image":{"contentType":"image/webp","filename":"sticker.webp","size":123,"caption":"caption"}}`)

	if got := renderEvent(t, output.Plain, msg); !strings.HasSuffix(got, ": [sticker 🙂]\n") {
		t.Errorf("unexpected plain: %q", got)
	}

	msg.Sticker.Image = nil
	assertStickerJSON(t, renderEvent(t, output.JSON, msg), `{"packId":"public-pack","stickerId":7,"emoji":"🙂"}`)
}

type savedStickerCase struct {
	name        string
	regular     app.SavedAttachment
	sticker     *app.SavedAttachment
	image       bool
	wantImage   string
	wantPlain   string
	wantRegular string
}

func savedStickerCases() []savedStickerCase {
	return []savedStickerCase{
		{
			"success",
			app.SavedAttachment{Path: "/regular"},
			&app.SavedAttachment{Path: "/sticker\n.webp"},
			true,
			`,"path":"/sticker\n.webp"`,
			`[sticker 🙂 → /sticker\n.webp]`,
			`"path":"/regular"`,
		},
		{
			"sticker failure",
			app.SavedAttachment{Path: "/regular"},
			&app.SavedAttachment{Err: errStickerImage},
			true,
			`,"downloadError":"bad\nimage"`,
			`[sticker 🙂 (download failed: bad\nimage)]`,
			`"path":"/regular"`,
		},
		{
			"regular failure",
			app.SavedAttachment{Err: errRegularImage},
			&app.SavedAttachment{Path: "/sticker"},
			true,
			`,"path":"/sticker"`,
			`[sticker 🙂 → /sticker]`,
			`"downloadError":"regular failed"`,
		},
		{
			"both failures",
			app.SavedAttachment{Err: errRegularImage},
			&app.SavedAttachment{Err: errStickerImage},
			true,
			`,"downloadError":"bad\nimage"`,
			`[sticker 🙂 (download failed: bad\nimage)]`,
			`"downloadError":"regular failed"`,
		},
		{
			"no result",
			app.SavedAttachment{},
			nil,
			true, "",
			`[sticker 🙂]`,
			`"contentType":"text/plain"`,
		},
		{
			"no image",
			app.SavedAttachment{},
			&app.SavedAttachment{Path: "/ignored"},
			false, "",
			`[sticker 🙂]`,
			`"contentType":"text/plain"`,
		},
	}
}

func TestSavedStickerMedia(t *testing.T) {
	t.Parallel()

	for _, testCase := range savedStickerCases() {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			msg := stickerMessage()

			msg.Attachments = []signal.Attachment{{ContentType: "text/plain"}}
			if !testCase.image {
				msg.Sticker.Image = nil
			}

			saved := app.MessageMediaResult{
				Attachments: []app.SavedAttachment{testCase.regular},
				Sticker:     testCase.sticker,
			}
			if testCase.name == "no result" {
				saved.Attachments = nil
			}

			checkSavedStickerMedia(t, msg, saved, testCase.image, testCase.wantImage, testCase.wantPlain, testCase.wantRegular)
		})
	}
}

func TestSavedMessagePreservesStickerMetadata(t *testing.T) {
	t.Parallel()

	msg := stickerMessage()

	var buf bytes.Buffer

	err := output.New(&buf, output.JSON, time.UTC).SavedMessage(msg, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := buf.String(), renderEvent(t, output.JSON, msg); got != want {
		t.Errorf("legacy saved message %s differs from event %s", got, want)
	}
}

func checkSavedStickerMedia(t *testing.T, msg *signal.Message, saved app.MessageMediaResult,
	image bool, wantImage, wantPlain, wantRegular string,
) {
	t.Helper()

	for _, format := range []output.Format{output.JSON, output.Plain} {
		var buf bytes.Buffer

		err := output.New(&buf, format, time.UTC).SavedMessageMedia(msg, saved)
		if err != nil {
			t.Fatal(err)
		}

		got := buf.String()
		if format == output.JSON {
			want := `{"packId":"public-pack","stickerId":7,"emoji":"🙂"`
			if image {
				want += `,"image":{"contentType":"image/webp","filename":"sticker.webp",` +
					`"size":123,"caption":"caption"` + wantImage + `}`
			}

			want += `}`
			assertStickerJSON(t, got, want)

			if !strings.Contains(got, wantRegular) {
				t.Errorf("missing regular outcome: %s", got)
			}
		} else if !strings.HasSuffix(got, wantPlain+"\n") || strings.Count(got, "\n") != 1 {
			t.Errorf("unexpected plain: %q", got)
		}
	}
}

func TestSavedStickerFallbackMetadata(t *testing.T) {
	t.Parallel()

	for _, pointer := range []bool{true, false} {
		msg := stickerMessage()
		if !pointer {
			msg.Sticker.Image = nil
		}

		saved := app.MessageMediaResult{
			Sticker: &app.SavedAttachment{
				Path:  "/fallback.gif",
				Image: &signal.Attachment{ContentType: "image/gif", Size: 8},
			},
		}

		var jsonBuf bytes.Buffer

		err := output.New(&jsonBuf, output.JSON, time.UTC).SavedMessageMedia(msg, saved)
		if err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(jsonBuf.String(), `"image":{"contentType":"image/gif","size":8,"path":"/fallback.gif"}`) {
			t.Fatalf("fallback metadata %s", jsonBuf.String())
		}

		var plainBuf bytes.Buffer

		err = output.New(&plainBuf, output.Plain, time.UTC).SavedMessageMedia(msg, saved)
		if err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(plainBuf.String(), "→ /fallback.gif") {
			t.Fatalf("fallback path %s", plainBuf.String())
		}
	}
}
