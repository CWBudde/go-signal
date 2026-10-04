//nolint:goconst // Literal expectations are independent story fixtures.
package signaltest_test

import (
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestStoryFakeBoundaries(t *testing.T) {
	t.Parallel()

	fake := profileFake()
	groupID := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	fake.GroupInfo = map[string]signal.Group{groupID: {ID: groupID, Members: []signal.GroupMember{
		{Recipient: signal.Recipient{ACI: profileAliceACI}},
		{Recipient: signal.Recipient{ACI: profileBobACI}},
	}}}
	client := profileClient(t, fake, "+12025550101", true)
	story := &signal.OutgoingStory{Text: "Original"}

	_, err := client.Send(t.Context(), signal.SendRequest{GroupID: groupID, Story: story})
	if err != nil {
		t.Fatal(err)
	}

	story.Text = "changed"

	sent := fake.Sent()
	if sent[0].Story.Text != "Original" {
		t.Error("Send aliases story input")
	}

	sent[0].Story.Text = "read changed"
	if fake.Sent()[0].Story.Text != "Original" {
		t.Error("Sent aliases stored story")
	}

	_, err = client.Send(t.Context(), signal.SendRequest{GroupID: groupID, Story: &signal.OutgoingStory{
		File: &signal.UploadedAttachment{ID: "unowned", ContentType: "image/png"},
	}})
	if !errors.Is(err, signal.ErrUnknownAttachment) {
		t.Errorf("unowned media: %v", err)
	}

	group := fake.GroupInfo[groupID]
	group.Members = group.Members[1:]
	fake.GroupInfo[groupID] = group

	_, err = client.Send(t.Context(), signal.SendRequest{
		GroupID: groupID, Story: &signal.OutgoingStory{Text: "not a member"},
	})
	if !errors.Is(err, signal.ErrNotAMember) {
		t.Errorf("nonmember: %v", err)
	}
}
