//nolint:cyclop,funlen,lll,goconst // independent literal expectations and copy assertions
package signaltest_test

import (
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestPollFakeCopies(t *testing.T) {
	t.Parallel()

	fake := profileFake()
	groupID := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	fake.Groups = map[string][]signal.Recipient{groupID: {{ACI: profileBobACI}}}
	cli := profileClient(t, fake, "+12025550101", true)
	p := &signal.Poll{Question: "q", Options: []string{"a", "b"}}

	_, err := cli.Send(t.Context(), signal.SendRequest{GroupID: groupID, PollCreate: p})
	if err != nil {
		t.Fatal(err)
	}

	p.Options[0] = "changed"

	sent := fake.Sent()
	if sent[0].PollCreate.Options[0] != "a" {
		t.Fatal("send aliases input")
	}

	sent[0].PollCreate.Options[0] = "another"
	if fake.Sent()[0].PollCreate.Options[0] != "a" {
		t.Fatal("Sent aliases stored poll")
	}

	vote := &signal.OutgoingPollVote{TargetAuthor: signal.Recipient{ACI: profileAliceACI}, TargetTimestamp: 42, VoteCount: 1, OptionIndexes: []uint32{1}}

	_, err = cli.Send(t.Context(), signal.SendRequest{GroupID: groupID, PollVote: vote})
	if err != nil {
		t.Fatal(err)
	}

	vote.OptionIndexes[0] = 0
	if fake.Sent()[1].PollVote.OptionIndexes[0] != 1 {
		t.Fatal("send aliases vote")
	}

	entry, err := cli.InboxAdd(t.Context(), signal.InboxEntry{Chat: signal.Chat{GroupID: groupID}, Event: &signal.Message{Poll: p}})
	if err != nil {
		t.Fatal(err)
	}

	p.Options[0] = "mutated"

	entries, err := cli.InboxList(t.Context(), signal.InboxQuery{})
	if err != nil {
		t.Fatal(err)
	}

	msg, ok := entries[0].Event.(*signal.Message)
	if !ok || msg.Poll.Options[0] == "mutated" {
		t.Fatal("inbox aliases input")
	}

	msg.Poll.Options[0] = "from list"

	returned, ok := entry.Event.(*signal.Message)
	if !ok {
		t.Fatalf("entry=%v", entry)
	}

	returned.Poll.Options[0] = "from add"
	snapshot := fake.Inbox()

	stored, ok := snapshot[0].Event.(*signal.Message)
	if !ok || stored.Poll.Options[0] != "changed" {
		t.Fatal("inbox exposes retained poll")
	}

	chats, err := cli.InboxChats(t.Context())
	if err != nil || len(chats) != 1 {
		t.Fatalf("chats=%v, %v", chats, err)
	}

	chatMsg, ok := chats[0].Last.Event.(*signal.Message)
	if !ok {
		t.Fatalf("chat message=%v", chats[0].Last.Event)
	}

	chatMsg.Poll.Options[0] = "from chat"

	stored.Poll.Options[0] = "from snapshot"

	entries, err = cli.InboxList(t.Context(), signal.InboxQuery{})
	if err != nil {
		t.Fatal(err)
	}

	msg, ok = entries[0].Event.(*signal.Message)
	if !ok || msg.Poll.Options[0] != "changed" {
		t.Fatal("snapshot exposes retained poll")
	}
}
