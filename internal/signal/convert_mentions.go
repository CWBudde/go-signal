//go:build cgo || libsignal_go

package signal

import (
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"github.com/google/uuid"
)

// convertMentions preserves wire offsets; renderers validate them against the raw text.
// Styles and malformed identities aren't mentions.
func convertMentions(ranges []*signalpb.BodyRange) []Mention {
	var mentions []Mention

	for _, bodyRange := range ranges {
		aci, err := signalmeow.ParseStringOrBinaryUUID(bodyRange.GetMentionAci(), bodyRange.GetMentionAciBinary())
		if err != nil || aci == uuid.Nil {
			continue
		}

		mentions = append(mentions, Mention{
			Start: bodyRange.GetStart(), Length: bodyRange.GetLength(), Recipient: aciRecipient(aci),
		})
	}

	return mentions
}

func convertQuote(quote *signalpb.DataMessage_Quote) *Quote {
	if quote == nil {
		return nil
	}

	author, _ := signalmeow.ParseStringOrBinaryUUID(quote.GetAuthorAci(), quote.GetAuthorAciBinary())

	return &Quote{
		Author: aciRecipient(author), Timestamp: quote.GetId(), Text: quote.GetText(),
		Mentions: convertMentions(quote.GetBodyRanges()),
	}
}
