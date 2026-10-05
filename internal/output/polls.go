package output

import (
	"fmt"
	"strings"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/signal"
)

type pollJSON struct {
	Question      string   `json:"question"`
	Options       []string `json:"options"`
	AllowMultiple bool     `json:"allowMultiple"`
}

func pollToJSON(poll *signal.Poll) *pollJSON {
	if poll == nil {
		return nil
	}

	return &pollJSON{Question: poll.Question, Options: poll.Options, AllowMultiple: poll.AllowMultiple}
}

type pollVoteDoc struct {
	eventHead
	envelopeJSON

	TargetAuthor    recipientJSON `json:"targetAuthor"`
	TargetTimestamp uint64        `json:"targetTimestamp"`
	OptionIndexes   []uint32      `json:"optionIndexes"`
	VoteCount       uint32        `json:"voteCount"`
}

type pollCloseDoc struct {
	eventHead
	envelopeJSON

	TargetTimestamp uint64 `json:"targetTimestamp"`
}

func pollIndexes(indexes []uint32) []uint32 {
	if indexes == nil {
		return []uint32{}
	}

	return indexes
}

type pollSendJSON struct {
	SendJSON

	Operation       string        `json:"operation"`
	TargetAuthor    recipientJSON `json:"targetAuthor"`
	TargetTimestamp uint64        `json:"targetTimestamp"`
	Creation        *pollJSON     `json:"creation,omitempty"`
	OptionIndexes   *[]uint32     `json:"optionIndexes,omitempty"`
	VoteCount       *uint32       `json:"voteCount,omitempty"`
}

// PollSend prints poll delivery outcomes, preserving partial group results.
func (p *Printer) PollSend(res app.PollSendResult) error {
	if p.format != JSON {
		return p.sendTable(res.SendResult)
	}

	doc := pollSendJSON{
		SendJSON:        p.sendToJSON(res.SendResult),
		Operation:       res.Operation,
		TargetAuthor:    p.recipient(res.TargetAuthor),
		TargetTimestamp: res.TargetTimestamp,
		Creation:        pollToJSON(res.Poll),
	}
	if res.Operation == "vote" {
		indexes := pollIndexes(res.OptionIndexes)
		doc.OptionIndexes = &indexes
		doc.VoteCount = &res.VoteCount
	}

	return p.writeJSON(struct {
		Version int          `json:"version"`
		Poll    pollSendJSON `json:"poll"`
	}{SchemaVersion, doc})
}

type pollStateVoteJSON struct {
	Voter         recipientJSON `json:"voter"`
	OptionIndexes []uint32      `json:"optionIndexes"`
	VoteCount     uint32        `json:"voteCount"`
	Timestamp     uint64        `json:"timestamp"`
}

type pollStateJSON struct {
	Chat            ChatJSON            `json:"chat"`
	Author          recipientJSON       `json:"author"`
	Timestamp       uint64              `json:"timestamp"`
	CreationPresent bool                `json:"creationPresent"`
	Creation        *pollJSON           `json:"creation,omitempty"`
	Tally           *[]int              `json:"tally,omitempty"`
	Votes           []pollStateVoteJSON `json:"votes"`
	ClosureObserved bool                `json:"closureObserved"`
	ClosedAt        uint64              `json:"closedAt,omitempty"`
	Deleted         bool                `json:"deleted"`
	Completeness    string              `json:"completeness"`
	Scanned         int                 `json:"scanned"`
	FirstEntryID    int64               `json:"firstEntryId"`
	LastEntryID     int64               `json:"lastEntryId"`
	Truncated       bool                `json:"truncated"`
	IgnoredInvalid  int                 `json:"ignoredInvalid"`
	Conflicts       int                 `json:"conflicts"`
}

// PollState prints retained observations, never claiming complete poll history.
func (p *Printer) PollState(state app.PollState) error {
	if p.format != JSON {
		return p.pollStatePlain(state)
	}

	doc := pollStateJSON{
		Chat:            NewChatJSON(state.Chat, p.names),
		Author:          p.recipient(state.Author),
		Timestamp:       state.Timestamp,
		CreationPresent: state.Creation != nil,
		Creation:        pollToJSON(state.Creation),
		Votes:           make([]pollStateVoteJSON, 0, len(state.Votes)),
		ClosureObserved: state.ClosureObserved,
		Deleted:         state.Deleted,
		Completeness:    "unknown",
		Scanned:         state.Scanned,
		FirstEntryID:    state.FirstEntryID,
		LastEntryID:     state.LastEntryID,
		Truncated:       state.Truncated,
		IgnoredInvalid:  state.IgnoredInvalid,
		Conflicts:       state.Conflicts,
	}
	if state.ClosureObserved {
		doc.ClosedAt = state.ClosedAt
	}

	if state.Creation != nil && !state.Deleted && state.Tally != nil {
		doc.Tally = &state.Tally
	}

	for _, vote := range state.Votes {
		doc.Votes = append(doc.Votes, pollStateVoteJSON{
			Voter:         p.recipient(vote.Voter),
			OptionIndexes: pollIndexes(vote.OptionIndexes),
			VoteCount:     vote.VoteCount,
			Timestamp:     vote.Timestamp,
		})
	}

	return p.writeJSON(struct {
		Version   int           `json:"version"`
		PollState pollStateJSON `json:"pollState"`
	}{SchemaVersion, doc})
}

func (p *Printer) pollStatePlain(state app.PollState) error {
	chat := p.who(state.Chat.Recipient)
	if state.Chat.IsGroup() {
		chat = p.groupLabel(state.Chat.GroupID)
	}

	lines := []string{
		fmt.Sprintf("Poll %s:%d in %s", p.who(state.Author), state.Timestamp, chat),
		"Based on retained observations; completeness: unknown.",
		fmt.Sprintf("Scanned %d entries (IDs %d–%d); truncated: %t; ignored invalid: %d; conflicts: %d.",
			state.Scanned, state.FirstEntryID, state.LastEntryID, state.Truncated, state.IgnoredInvalid, state.Conflicts),
	}
	if state.Creation == nil {
		lines = append(lines, "Poll creation unavailable; tally unavailable.")
	} else {
		lines = append(lines, pollText(state.Creation))
	}

	if state.Deleted {
		lines = append(lines, "Deletion observed; tally unavailable.")
	} else if state.Creation != nil && state.Tally != nil {
		lines = append(lines, fmt.Sprintf("Observed tally: %v", state.Tally))
	}

	if state.ClosureObserved {
		lines = append(lines, fmt.Sprintf("Poll closure observed at %d.", state.ClosedAt))
	} else {
		lines = append(lines, "Poll closure not observed.")
	}

	for _, vote := range state.Votes {
		lines = append(lines, fmt.Sprintf("%s: options %v (counter %d, timestamp %d)",
			p.who(vote.Voter), pollIndexes(vote.OptionIndexes), vote.VoteCount, vote.Timestamp))
	}

	return p.writeLine(strings.Join(lines, "\n"))
}

func pollText(poll *signal.Poll) string {
	parts := make([]string, 0, 1+len(poll.Options))

	parts = append(parts, "[poll "+oneLine(poll.Question))
	for i, option := range poll.Options {
		parts = append(parts, fmt.Sprintf("%d: %s", i, oneLine(option)))
	}

	return strings.Join(parts, "; ") + "]"
}
