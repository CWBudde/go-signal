//go:build cgo || libsignal_go

package signal

import (
	"fmt"
	"slices"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/google/uuid"
)

const (
	pollProtocolVersion = 8
	invalidPollType     = "invalidPoll"
)

func addPoll(msg *signalpb.DataMessage, req SendRequest) error {
	if p := req.PollCreate; p != nil {
		msg.PollCreate = &signalpb.DataMessage_PollCreate{
			Question: new(p.Question), AllowMultiple: new(p.AllowMultiple), Options: slices.Clone(p.Options),
		}
		msg.RequiredProtocolVersion = new(uint32(pollProtocolVersion))
	}

	if vote := req.PollVote; vote != nil {
		aci, err := aciBytes(vote.TargetAuthor)
		if err != nil {
			return fmt.Errorf("poll target: %w", err)
		}

		msg.PollVote = &signalpb.DataMessage_PollVote{
			TargetAuthorAciBinary: aci, TargetSentTimestamp: new(vote.TargetTimestamp),
			OptionIndexes: slices.Clone(vote.OptionIndexes), VoteCount: new(vote.VoteCount),
		}
	}

	if p := req.PollClose; p != nil {
		msg.PollTerminate = &signalpb.DataMessage_PollTerminate{TargetSentTimestamp: new(p.TargetTimestamp)}
	}

	return nil
}

func hasWirePoll(msg *signalpb.DataMessage) bool { return wirePollCount(msg) > 0 }

func wirePollCount(msg *signalpb.DataMessage) int {
	count := 0
	if msg.GetPollCreate() != nil {
		count++
	}

	if msg.GetPollVote() != nil {
		count++
	}

	if msg.GetPollTerminate() != nil {
		count++
	}

	return count
}

func pollHasWireContent(msg *signalpb.DataMessage) bool {
	return pollHasOrdinaryContent(msg) || pollHasControlContent(msg)
}

func pollHasOrdinaryContent(msg *signalpb.DataMessage) bool {
	return msg.Body != nil || len(msg.GetAttachments()) > 0 || msg.GetSticker() != nil || msg.GetQuote() != nil ||
		len(msg.GetBodyRanges()) > 0 || len(msg.GetPreview()) > 0 || msg.GetIsViewOnce()
}

func pollHasControlContent(msg *signalpb.DataMessage) bool {
	const incompatibleFlags = endSessionFlag | uint32(signalpb.DataMessage_EXPIRATION_TIMER_UPDATE) |
		uint32(signalpb.DataMessage_PROFILE_KEY_UPDATE)

	return msg.GetReaction() != nil || msg.GetDelete() != nil || len(unsupportedParts(msg)) > 0 ||
		msg.GetFlags()&incompatibleFlags != 0
}

func convertPoll(env Envelope, msg *signalpb.DataMessage) Event {
	invalid := &Unsupported{Envelope: env, Type: invalidPollType}
	if wirePollCount(msg) != 1 || env.Timestamp == 0 || pollHasWireContent(msg) {
		return invalid
	}

	if p := msg.GetPollCreate(); p != nil {
		poll := &Poll{Question: p.GetQuestion(), Options: slices.Clone(p.GetOptions()), AllowMultiple: p.GetAllowMultiple()}
		if poll.check(pollIncomingQuestionLimit) != nil {
			return invalid
		}

		return &Message{Envelope: env, Poll: poll}
	}

	if vote := msg.GetPollVote(); vote != nil {
		return convertPollVote(env, vote)
	}

	closePoll := OutgoingPollClose{TargetTimestamp: msg.GetPollTerminate().GetTargetSentTimestamp()}
	if closePoll.Check() != nil {
		return invalid
	}

	return &PollClose{Envelope: env, OutgoingPollClose: closePoll}
}

func convertPollVote(env Envelope, wire *signalpb.DataMessage_PollVote) Event {
	authorID, err := uuid.FromBytes(wire.GetTargetAuthorAciBinary())
	if err != nil {
		return &Unsupported{Envelope: env, Type: invalidPollType}
	}

	vote := OutgoingPollVote{
		TargetAuthor: Recipient{ACI: authorID.String()}, TargetTimestamp: wire.GetTargetSentTimestamp(),
		OptionIndexes: slices.Clone(wire.GetOptionIndexes()), VoteCount: wire.GetVoteCount(),
	}
	if vote.check() != nil {
		return &Unsupported{Envelope: env, Type: invalidPollType}
	}

	return &PollVote{Envelope: env, OutgoingPollVote: vote}
}
