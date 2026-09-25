package app_test

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

// writeFile writes data to name in a temporary directory and returns the path.
func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)

	err := os.WriteFile(path, data, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return path
}

// pngImage returns a PNG of width × height pixels.
func pngImage(t *testing.T, width, height int) []byte {
	t.Helper()

	var buf bytes.Buffer

	err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, width, height)))
	if err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

// groupFake is directory() with a group of us, alice and bob.
func groupFake() *signaltest.Fake {
	fake := directory()
	fake.Groups = map[string][]signal.Recipient{
		groupID: {{ACI: testAccount().ACI}, {ACI: aliceACI}, {ACI: bobACI}},
	}

	return fake
}

func TestSendAttachments(t *testing.T) {
	t.Parallel()

	fake := groupFake()
	photo := pngImage(t, 64, 48)
	paths := []string{
		// The content decides over a misleading extension.
		writeFile(t, "photo.jpg", photo),
		// Plain text with a known extension gets the extension's type.
		writeFile(t, "data.json", []byte(`{"a": 1}`)),
		writeFile(t, "notes", []byte("just text\n")),
	}

	res, err := sender(t, fake).Send(t.Context(), app.SendRequest{
		Recipients:  []string{aliceNumber, app.GroupPrefix + groupID},
		Attachments: paths,
	})
	if err != nil || res.Failed() != 0 {
		t.Fatalf("send: %+v, %v", res, err)
	}

	want := []signal.OutgoingAttachment{
		{Data: photo, ContentType: "image/png", Filename: "photo.jpg", Width: 64, Height: 48},
		{Data: []byte(`{"a": 1}`), ContentType: "application/json", Filename: "data.json"},
		{Data: []byte("just text\n"), ContentType: "text/plain", Filename: "notes"},
	}
	if got := fake.Uploaded(); !reflect.DeepEqual(got, want) {
		t.Errorf("uploaded\n%+v\nwant\n%+v", got, want)
	}

	// Uploaded once, sent to the user and the group without a body.
	sent := fake.Sent()
	if len(sent) != 2 {
		t.Fatalf("sent %+v", sent)
	}

	for _, req := range sent {
		if req.Body != "" || len(req.Attachments) != 3 || req.Attachments[0].Filename != "photo.jpg" {
			t.Errorf("sent %+v", req)
		}
	}

	if !reflect.DeepEqual(sent[0].Attachments, sent[1].Attachments) {
		t.Errorf("attachments differ: %+v, %+v", sent[0].Attachments, sent[1].Attachments)
	}
}

func TestSendAttachmentErrors(t *testing.T) {
	t.Parallel()

	large := writeFile(t, "large.bin", nil)

	err := os.Truncate(large, app.MaxAttachmentSize+1)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
		want error
	}{
		{"too large", large, app.ErrAttachmentTooLarge},
		{"directory", t.TempDir(), app.ErrNotAFile},
		{"missing", filepath.Join(t.TempDir(), "missing.png"), os.ErrNotExist},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := directory()

			_, err := sender(t, fake).Send(t.Context(), app.SendRequest{
				Recipients: []string{aliceNumber}, Body: body, Attachments: []string{test.path},
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}

			// Checked before connecting.
			if len(fake.Connects()) != 0 || len(fake.Uploaded()) != 0 {
				t.Errorf("connected %v, uploaded %d", fake.Connects(), len(fake.Uploaded()))
			}
		})
	}
}

func TestSendUploadFails(t *testing.T) {
	t.Parallel()

	fake := directory()
	fake.UploadErr = errBoom

	_, err := sender(t, fake).Send(t.Context(), app.SendRequest{
		Recipients: []string{aliceNumber}, Attachments: []string{writeFile(t, "a.txt", []byte("a"))},
	})
	if !errors.Is(err, errBoom) || len(fake.Sent()) != 0 {
		t.Errorf("got %v and sent %+v, want errBoom and nothing sent", err, fake.Sent())
	}
}

func TestSendMentions(t *testing.T) {
	t.Parallel()

	fake := groupFake()
	own := testAccount()

	_, err := sender(t, fake).Send(t.Context(), app.SendRequest{
		Recipients: []string{app.GroupPrefix + groupID},
		// An emoji (two UTF-16 units) before the mentions; HEAD@{1} and a group are no mentions.
		Body: "😀 @{" + aliceNumber + "} and @{@bob.42}, not HEAD@{1} or @{group:" + groupID + "}, cc @{self}",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	sent := fake.Sent()
	if len(sent) != 1 {
		t.Fatalf("sent %+v", sent)
	}

	wantBody := "😀 \uFFFC and \uFFFC, not HEAD@{1} or @{group:" + groupID + "}, cc \uFFFC"
	if sent[0].Body != wantBody {
		t.Errorf("body %q, want %q", sent[0].Body, wantBody)
	}

	want := []signal.Mention{
		{Start: 3, Length: 1, Recipient: signal.Recipient{ACI: aliceACI, PNI: carolACI, Number: aliceNumber}},
		{Start: 9, Length: 1, Recipient: signal.Recipient{ACI: bobACI, Username: bobUsername[1:]}},
		{Start: 86, Length: 1, Recipient: signal.Recipient{ACI: own.ACI, Number: own.Number}},
	}
	if !reflect.DeepEqual(sent[0].Mentions, want) {
		t.Errorf("mentions\n%+v\nwant\n%+v", sent[0].Mentions, want)
	}
}

func TestSendMentionNotOnSignal(t *testing.T) {
	t.Parallel()

	fake := directory()

	_, err := sender(t, fake).Send(t.Context(), app.SendRequest{
		Recipients: []string{aliceNumber}, Body: "hi @{+4915100000000}",
	})
	if !errors.Is(err, signal.ErrNotOnSignal) || len(fake.Sent()) != 0 {
		t.Errorf("got %v and sent %+v, want ErrNotOnSignal and nothing sent", err, fake.Sent())
	}
}

func TestSendQuote(t *testing.T) {
	t.Parallel()

	own := testAccount()

	tests := []struct {
		quote string
		want  signal.Recipient
	}{
		{"@bob.42:1789999999000", signal.Recipient{ACI: bobACI, Username: bobUsername[1:]}},
		{"self:1789999999000", signal.Recipient{ACI: own.ACI, Number: own.Number}},
	}

	for _, test := range tests {
		t.Run(test.quote, func(t *testing.T) {
			t.Parallel()

			fake := directory()

			_, err := sender(t, fake).Send(t.Context(), app.SendRequest{
				Recipients: []string{bobUsername}, Body: body, Quote: test.quote, QuoteText: "earlier",
			})
			if err != nil {
				t.Fatalf("send: %v", err)
			}

			want := &signal.Quote{Author: test.want, Timestamp: 1789999999000, Text: "earlier"}
			if sent := fake.Sent(); len(sent) != 1 || !reflect.DeepEqual(sent[0].Quote, want) {
				t.Errorf("sent %+v, want quote %+v", sent, want)
			}
		})
	}
}

func TestParseQuote(t *testing.T) {
	t.Parallel()

	author, timestamp, err := app.ParseQuote(aliceACI + ":1790000000000")
	if err != nil || author.Recipient.ACI != aliceACI || timestamp != 1790000000000 {
		t.Errorf("got %+v, %d, %v", author, timestamp, err)
	}

	for _, arg := range []string{
		"", aliceACI, aliceACI + ":", aliceACI + ":0", aliceACI + ":-1", aliceACI + ":1.5", "bob:1",
		"group:" + groupID + ":1",
	} {
		_, _, err := app.ParseQuote(arg)
		if !errors.Is(err, app.ErrInvalidQuote) {
			t.Errorf("ParseQuote(%q) error = %v, want ErrInvalidQuote", arg, err)
		}
	}
}

func TestSendInvalidQuote(t *testing.T) {
	t.Parallel()

	for _, req := range []app.SendRequest{
		{Recipients: []string{aliceNumber}, Body: body, Quote: "alice:1"},
		{Recipients: []string{aliceNumber}, Body: body, QuoteText: "without quote"},
	} {
		fake := directory()

		_, err := sender(t, fake).Send(t.Context(), req)
		if !errors.Is(err, app.ErrInvalidQuote) || len(fake.Connects()) != 0 {
			t.Errorf("%+v: got %v (connects %v), want ErrInvalidQuote before connecting", req, err, fake.Connects())
		}
	}
}
