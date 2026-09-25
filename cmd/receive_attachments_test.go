package cmd_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

var errCDN = errors.New("CDN unreachable")

const receiveCmd = "receive"

// attachmentEvents are messages with attachments: saved, not found, failing, view-once.
func attachmentEvents() []signal.Event {
	alice := signal.Recipient{ACI: aliceACI}
	env := func(ts uint64) signal.Envelope {
		return signal.Envelope{Sender: alice, Chat: signal.Chat{Recipient: alice}, Timestamp: ts, ServerTimestamp: ts + 500}
	}
	att := func(key, contentType, filename string) signal.Attachment {
		return signal.Attachment{
			ContentType: contentType, Filename: filename, Size: 3, Remote: signal.RemoteAttachment{CDNKey: key},
		}
	}

	return []signal.Event{
		&signal.Message{
			Envelope: env(at(1)),
			Body:     "two photos",
			Attachments: []signal.Attachment{
				att("photo", "image/jpeg", "../../photo.jpg"), att("voice", "audio/aac", ""),
			},
		},
		&signal.Message{
			Envelope:    env(at(2)),
			Attachments: []signal.Attachment{att("gone", "application/pdf", "doc.pdf"), att("broken", "image/png", "")},
		},
		&signal.Message{
			Envelope: env(at(3)), Attachments: []signal.Attachment{att("once", "image/jpeg", "")}, ViewOnce: true,
		},
		&signal.Message{Envelope: env(at(4)), Body: "no attachments"},
	}
}

// receiveAttachments runs `receive --follow --download-attachments <dir>` with args until all
// attachmentEvents are printed, and returns the output with dir replaced by $DIR.
func receiveAttachments(t *testing.T, dir string, args ...string) string {
	t.Helper()

	events := attachmentEvents()
	fake := &signaltest.Fake{
		Linked:   []signal.Account{*testAccount()},
		Incoming: events,
		Attachments: map[string][]byte{
			"photo": []byte("JPG"), "voice": []byte("AAC"), "broken": []byte("PNG"), "once": []byte("ONE"),
		},
		DownloadErrs: map[string]error{"broken": errCDN},
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		for fake.Delivered() < len(events) {
			time.Sleep(time.Millisecond)
		}

		cancel()
	}()

	args = append([]string{receiveCmd, "-f", "--download-attachments", dir}, args...)

	out, err := runContext(t, ctx, fake, args...)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}

	return strings.ReplaceAll(out, dir, "$DIR")
}

// wantDownloads checks the files in dir.
func wantDownloads(t *testing.T, dir string) {
	t.Helper()

	want := map[string]string{
		"1789907401000-1-photo.jpg":      "JPG",
		"1789907401000-2-attachment.aac": "AAC",
		"1789907403000-1-attachment.jpg": "ONE",
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != len(want) {
		t.Errorf("download dir has %d files, want %d: %v", len(entries), len(want), entries)
	}

	for name, content := range want {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(data) != content {
			t.Errorf("%s contains %q (%v), want %q", name, data, err, content)
		}
	}
}

func TestReceiveDownloadAttachmentsPlain(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "downloads")

	golden(t, "receive_attachments", receiveAttachments(t, dir))
	wantDownloads(t, dir)
}

func TestReceiveDownloadAttachmentsJSON(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "downloads")

	golden(t, "receive_attachments_json", receiveAttachments(t, dir, "-o", "json"))
	wantDownloads(t, dir)
}

func TestReceiveDownloadAttachmentsBadDir(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "file")

	err := os.WriteFile(file, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}, Incoming: attachmentEvents()}

	_, err = run(t, fake, receiveCmd, "--download-attachments", file)
	if err == nil || !strings.Contains(err.Error(), "--download-attachments") {
		t.Errorf("receive = %v, want a --download-attachments error", err)
	}

	if len(fake.Connects()) != 0 {
		t.Error("receive connected despite the bad download dir")
	}
}
