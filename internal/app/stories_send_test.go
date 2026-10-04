//nolint:goconst // Literal expectations are independent story fixtures.
package app_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

func storyDirectory() *signaltest.Fake {
	fake := directory()
	fake.GroupInfo = map[string]signal.Group{groupID: {ID: groupID, Members: []signal.GroupMember{
		{Recipient: signal.Recipient{ACI: testAccount().ACI}},
		{Recipient: signal.Recipient{ACI: aliceACI}},
	}}}

	return fake
}

func storyImage(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "story.png")

	data := pngImage(t, 1, 1)

	err := os.WriteFile(path, data, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return path
}

//nolint:cyclop // Assertions cover independent story payload and preflight boundaries.
func TestStorySendMedia(t *testing.T) {
	t.Parallel()

	fake := storyDirectory()

	res, err := sender(t, fake).StorySend(t.Context(), app.StorySendRequest{
		GroupID: groupID, Attachment: storyImage(t), NoReplies: true,
	})
	if err != nil || res.Timestamp != sentAt || res.AllowsReplies || res.Failed() != 0 {
		t.Fatalf("result: %+v %v", res, err)
	}

	sent := fake.Sent()
	if len(sent) != 1 || len(fake.Uploaded()) != 1 || sent[0].Story == nil || sent[0].Story.File == nil ||
		sent[0].Story.File.ContentType != pngType || sent[0].Body != "" || len(sent[0].Attachments) != 0 {
		t.Fatalf("media: %+v", sent)
	}

	if len(fake.Receipts()) != 0 {
		t.Fatal("story send emitted receipts")
	}
}

//nolint:cyclop // Assertions cover independent story payload and preflight boundaries.
func TestStorySendPreflight(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"spoofed image", "missing file", "nonmember", "invited member", "upload failure"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fake := storyDirectory()
			req := app.StorySendRequest{GroupID: groupID, Attachment: storyImage(t)}
			want := signal.ErrInvalidStory
			connects := 0

			switch name {
			case "spoofed image":
				err := os.WriteFile(req.Attachment, []byte("not an image"), 0o600)
				if err != nil {
					t.Fatal(err)
				}
			case "missing file":
				req.Attachment += ".missing"
				want = os.ErrNotExist
			case "nonmember", "invited member":
				group := fake.GroupInfo[groupID]

				group.Members = group.Members[1:]
				if name == "invited member" {
					group.Pending = []signal.PendingMember{{Recipient: signal.Recipient{ACI: testAccount().ACI}}}
				}

				fake.GroupInfo[groupID] = group
				want, connects = signal.ErrNotAMember, 1
			case "upload failure":
				want, connects = signal.ErrSendFailed, 1
				fake.UploadErr = want
			}

			_, err := sender(t, fake).StorySend(t.Context(), req)
			if !errors.Is(err, want) || len(fake.Sent()) != 0 || len(fake.Uploaded()) != 0 || len(fake.Connects()) != connects {
				t.Fatalf("preflight: %v sent%d uploads%d connects%d",
					err, len(fake.Sent()), len(fake.Uploaded()), len(fake.Connects()))
			}
		})
	}
}

func TestStorySendAllowlist(t *testing.T) {
	t.Parallel()

	fake := storyDirectory()

	client, err := fake.Factory(t.Context(), signal.Options{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	list, err := app.ParseAllowlist(nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = app.New(client, app.WithAllowlist(list)).StorySend(t.Context(), app.StorySendRequest{
		GroupID: groupID, Attachment: storyImage(t),
	})
	if !errors.Is(err, app.ErrRecipientNotAllowed) || len(fake.Uploaded()) != 0 || len(fake.Sent()) != 0 {
		t.Fatalf("allowlist: %v", err)
	}
}
