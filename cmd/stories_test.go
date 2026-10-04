//nolint:goconst // Literal expectations are independent story fixtures.
package cmd_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

const (
	storyStdinFlag  = "--stdin"
	storyAttachFlag = "--attach"
)

func storiesFake() *signaltest.Fake {
	fake := sendFake()
	fake.Groups = nil
	fake.GroupInfo = map[string]signal.Group{groupID: {ID: groupID, Members: []signal.GroupMember{
		{Recipient: signal.Recipient{ACI: testAccount().ACI}},
		{Recipient: signal.Recipient{ACI: aliceACI}},
		{Recipient: signal.Recipient{ACI: carolACI}},
	}}}

	return fake
}

func TestStoriesSend(t *testing.T) {
	t.Parallel()

	for _, format := range []string{formatPlain, formatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			fake := storiesFake()

			out, err := runSend(t, fake, "", "-o", format, "stories", "send", groupFlag, groupID, "-m", "A good day")
			if err != nil {
				t.Fatal(err)
			}

			golden(t, "stories_send_"+format, out)

			if len(fake.Sent()) != 1 {
				t.Fatalf("sent %+v", fake.Sent())
			}

			story := fake.Sent()[0].Story
			if story == nil || story.Text != "A good day" || !story.AllowsReplies || len(fake.Receipts()) != 0 {
				t.Fatalf("story: %+v", story)
			}
		})
	}
}

func TestStoriesSendPartial(t *testing.T) {
	t.Parallel()

	fake := storiesFake()
	fake.SendFailures = map[string]error{carolACI: errUnreachable}

	out, err := runSend(t, fake, "Story from stdin\n", "-o", formatJSON,
		"stories", "send", groupFlag, groupID, storyStdinFlag, "--no-replies")
	if !errors.Is(err, app.ErrSendFailed) {
		t.Fatalf("got %v", err)
	}

	if !strings.Contains(out, "recipient unreachable") {
		t.Fatalf("missing partial result: %s", out)
	}

	golden(t, "stories_send_partial_json", out)

	if story := fake.Sent()[0].Story; story.Text != "Story from stdin" || story.AllowsReplies {
		t.Fatalf("stdin story: %+v", story)
	}
}

func TestStoriesSendMediaAndSelectedAccount(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "story.png")

	err := os.WriteFile(path, groupAvatarImage(t, 1), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	fake := storiesFake()
	account := signal.Account{ACI: bobACI, Number: "+15550102"}
	fake.Linked = append(fake.Linked, account)
	group := fake.GroupInfo[groupID]
	group.Members = append(group.Members, signal.GroupMember{Recipient: signal.Recipient{ACI: account.ACI}})
	fake.GroupInfo[groupID] = group

	_, err = runSend(t, fake, "", "--account", account.Number,
		"stories", "send", groupFlag, groupID, storyAttachFlag, path)
	if err != nil {
		t.Fatal(err)
	}

	if connects := fake.Connects(); len(connects) != 1 || connects[0] != account.ACI || len(fake.Uploaded()) != 1 ||
		fake.Sent()[0].Story.File == nil || fake.Sent()[0].Story.Text != "" {
		t.Fatalf("selected account: %v uploads%v sent%v", connects, fake.Uploaded(), fake.Sent())
	}
}

func TestStoriesSelectedNonmember(t *testing.T) {
	t.Parallel()

	fake := storiesFake()
	fake.Linked = append(fake.Linked, signal.Account{ACI: bobACI, Number: "+15550102"})

	_, err := runSend(t, fake, "", "--account", "+15550102", "stories", "send", groupFlag, groupID, "-m", "Story")
	if !errors.Is(err, signal.ErrNotAMember) || len(fake.Sent()) != 0 || len(fake.Uploaded()) != 0 {
		t.Fatalf("selected nonmember: %v", err)
	}
}

func TestStoriesSendInvalidFlags(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"-m", "test card"},
		{groupFlag, "invalid", "-m", "test card"},
		{groupFlag, groupID, "-m", "test card", storyStdinFlag},
		{groupFlag, groupID, "-m", "test card", storyAttachFlag, "file.png"},
		{groupFlag, groupID, "-m", " "},
		{groupFlag, groupID, "self", "-m", "test card"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()

			fake := sendFake()

			_, err := runSend(t, fake, "", append([]string{"stories", "send"}, args...)...)
			if err == nil || len(fake.Connects()) != 0 || len(fake.Sent()) != 0 {
				t.Fatalf("invalid request connected/sent: %v", err)
			}
		})
	}
}
