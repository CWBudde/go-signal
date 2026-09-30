package signaltest

import (
	"slices"

	"github.com/cwbudde/go-signal/internal/signal"
)

func clonePoll(p *signal.Poll) *signal.Poll {
	if p == nil {
		return nil
	}

	copyPoll := *p
	copyPoll.Options = slices.Clone(p.Options)

	return &copyPoll
}

func clonePollRequest(req signal.SendRequest) signal.SendRequest {
	if req.Pin != nil {
		pin := *req.Pin
		req.Pin = &pin
	}

	if req.Unpin != nil {
		unpin := *req.Unpin
		req.Unpin = &unpin
	}

	req.PollCreate = clonePoll(req.PollCreate)
	if req.PollVote != nil {
		vote := *req.PollVote
		vote.OptionIndexes = slices.Clone(vote.OptionIndexes)
		req.PollVote = &vote
	}

	if req.PollClose != nil {
		closePoll := *req.PollClose
		req.PollClose = &closePoll
	}

	return req
}

func clonePollEntry(entry signal.InboxEntry) signal.InboxEntry {
	switch evt := entry.Event.(type) {
	case *signal.Pin:
		pin := *evt
		entry.Event = &pin
	case *signal.Unpin:
		unpin := *evt
		entry.Event = &unpin
	case *signal.Message:
		if evt.Poll == nil {
			return entry
		}

		copyMsg := *evt
		copyMsg.Poll = clonePoll(evt.Poll)
		entry.Event = &copyMsg
	case *signal.PollVote:
		vote := *evt
		vote.OptionIndexes = slices.Clone(evt.OptionIndexes)
		entry.Event = &vote
	case *signal.PollClose:
		closePoll := *evt
		entry.Event = &closePoll
	}

	return entry
}
