package app_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	msgTS      = 1789907401000
	photoName  = "photo.jpg"
	noFileName = "attachment"
)

func TestAttachmentFilename(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("ä", 100) + ".jpeg"

	tests := []struct {
		name, filename, contentType, want string
	}{
		{"plain", photoName, "image/jpeg", photoName},
		{"unicode kept", "Grüße 2026.pdf", "", "Grüße_2026.pdf"},
		{"no name, known type", "", "image/JPEG", "attachment.jpg"},
		{"no name, unknown type", "", "application/x-foo", noFileName},
		{"long message text", "", "text/x-signal-plain", "attachment.txt"},
		{"unix traversal", "../../etc/passwd", "", "passwd"},
		{"windows traversal", `..\..\boot.ini`, "", "boot.ini"},
		{"only dots", "..", "image/png", "attachment.png"},
		{"trailing separator", "dir/", "", noFileName},
		{"hidden file", ".bashrc", "", "bashrc"},
		{"control and shell characters", "a\x00b\n$(rm -rf ~);.sh", "", "a_b_rm_-rf_.sh"},
		{"bidi override", "evil\u202egpj.exe", "", "evil_gpj.exe"},
		{"only dashes", "---", "", noFileName},
		{"shortened keeps extension", long, "", strings.Repeat("ä", 57) + ".jpeg"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			att := signal.Attachment{Filename: test.filename, ContentType: test.contentType}

			got := app.AttachmentFilename(msgTS, 2, att)
			if want := "1789907401000-2-" + test.want; got != want {
				t.Errorf("AttachmentFilename(%q) = %q, want %q", test.filename, got, want)
			}
		})
	}
}

func attachment(key, filename string) signal.Attachment {
	return signal.Attachment{
		ContentType: "image/jpeg", Filename: filename, Size: 3, Remote: signal.RemoteAttachment{CDNKey: key},
	}
}

func TestSaveAttachments(t *testing.T) {
	t.Parallel()

	fake := &signaltest.Fake{
		Attachments:  map[string][]byte{"a": []byte("AAA"), "c": []byte("CCC")},
		DownloadErrs: map[string]error{"b": errBoom},
	}
	dir := filepath.Join(t.TempDir(), "new", "downloads")
	msg := &signal.Message{
		Envelope: signal.Envelope{Timestamp: msgTS},
		Attachments: []signal.Attachment{
			attachment("a", "../photo.jpg"), attachment("b", "b.jpg"), attachment("gone", ""), attachment("c", ""),
		},
	}

	got := open(t, fake).SaveAttachments(t.Context(), app.SaveAttachmentsRequest{Dir: dir, Message: msg})
	if len(got) != 4 {
		t.Fatalf("SaveAttachments returned %d results, want 4", len(got))
	}

	wantFile(t, got[0], filepath.Join(dir, "1789907401000-1-photo.jpg"), "AAA")
	wantFile(t, got[3], filepath.Join(dir, "1789907401000-4-attachment.jpg"), "CCC")

	if got[1].Path != "" || !errors.Is(got[1].Err, errBoom) {
		t.Errorf("failed download = %+v, want %v", got[1], errBoom)
	}

	if got[2].Path != "" || !errors.Is(got[2].Err, signal.ErrAttachmentNotFound) {
		t.Errorf("missing attachment = %+v, want %v", got[2], signal.ErrAttachmentNotFound)
	}

	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("download dir: %v, %v; want mode 0700", info, err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Errorf("download dir has %v (%v), want the 2 saved files", entries, err)
	}
}

// wantFile checks that saved is the file path with content, mode 0600.
func wantFile(t *testing.T, saved app.SavedAttachment, path, content string) {
	t.Helper()

	if saved.Err != nil || saved.Path != path {
		t.Errorf("saved = %+v, want %s", saved, path)

		return
	}

	data, err := os.ReadFile(path)
	if err != nil || string(data) != content {
		t.Errorf("%s contains %q (%v), want %q", path, data, err, content)
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("%s: %v, %v; want mode 0600", path, info, err)
	}
}

func TestSaveAttachmentsKeepsExistingFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	taken := filepath.Join(dir, "1789907401000-1-photo.jpg")

	err := os.WriteFile(taken, []byte("old"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	fake := &signaltest.Fake{Attachments: map[string][]byte{"a": []byte("new")}}
	msg := &signal.Message{
		Envelope: signal.Envelope{Timestamp: msgTS}, Attachments: []signal.Attachment{attachment("a", photoName)},
	}
	a := open(t, fake)

	got := a.SaveAttachments(t.Context(), app.SaveAttachmentsRequest{Dir: dir, Message: msg})
	wantFile(t, got[0], filepath.Join(dir, "1789907401000-1-photo-2.jpg"), "new")

	got = a.SaveAttachments(t.Context(), app.SaveAttachmentsRequest{Dir: dir, Message: msg})
	wantFile(t, got[0], filepath.Join(dir, "1789907401000-1-photo-3.jpg"), "new")

	data, err := os.ReadFile(taken)
	if err != nil || string(data) != "old" {
		t.Errorf("existing file was changed: %q, %v", data, err)
	}
}

func TestSaveAttachmentsSymlinkNotFollowed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "target")

	err := os.Symlink(outside, filepath.Join(dir, "1789907401000-1-photo.jpg"))
	if err != nil {
		t.Fatal(err)
	}

	fake := &signaltest.Fake{Attachments: map[string][]byte{"a": []byte("new")}}
	msg := &signal.Message{
		Envelope: signal.Envelope{Timestamp: msgTS}, Attachments: []signal.Attachment{attachment("a", photoName)},
	}

	got := open(t, fake).SaveAttachments(t.Context(), app.SaveAttachmentsRequest{Dir: dir, Message: msg})
	wantFile(t, got[0], filepath.Join(dir, "1789907401000-1-photo-2.jpg"), "new")

	_, err = os.Stat(outside)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("symlink target was created: %v", err)
	}
}

func TestSaveAttachmentsBadDir(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "file")

	err := os.WriteFile(file, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	fake := &signaltest.Fake{Attachments: map[string][]byte{"a": []byte("new")}}
	msg := &signal.Message{Attachments: []signal.Attachment{attachment("a", ""), attachment("a", "")}}

	got := open(t, fake).SaveAttachments(t.Context(), app.SaveAttachmentsRequest{Dir: file, Message: msg})
	for i, saved := range got {
		if saved.Err == nil || saved.Path != "" {
			t.Errorf("attachment %d = %+v, want an error", i, saved)
		}
	}
}

func TestPrepareDownloadDir(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "file")

	err := os.WriteFile(file, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = app.PrepareDownloadDir(file)
	if err == nil {
		t.Error("PrepareDownloadDir on a file succeeded")
	}

	err = app.PrepareDownloadDir(filepath.Join(t.TempDir(), "a", "b"))
	if err != nil {
		t.Errorf("PrepareDownloadDir: %v", err)
	}
}
