package signal

import "fmt"

// Check reports whether req can be sent: it needs either recipients or a group
// (ErrInvalidSendRequest), a reaction or remote delete comes without other content and with what
// it needs (ErrInvalidContent), and a reaction's target author has an ACI (ErrUnresolvable). It
// doesn't check recipients, attachments, the quote or mentions.
func (req SendRequest) Check() error {
	if (req.GroupID == "") == (len(req.Recipients) == 0) {
		return ErrInvalidSendRequest
	}

	switch {
	case req.Reaction != nil:
		return req.checkReaction()
	case req.DeleteTarget != 0:
		if req.hasContent() {
			return fmt.Errorf("%w: a remote delete has no other content", ErrInvalidContent)
		}
	}

	return nil
}

func (req SendRequest) checkReaction() error {
	reaction := req.Reaction

	switch {
	case req.hasContent() || req.DeleteTarget != 0:
		return fmt.Errorf("%w: a reaction has no other content", ErrInvalidContent)
	case reaction.Emoji == "":
		return fmt.Errorf("%w: a reaction needs an emoji", ErrInvalidContent)
	case reaction.TargetTimestamp == 0:
		return fmt.Errorf("%w: a reaction needs the target's timestamp", ErrInvalidContent)
	case reaction.TargetAuthor.ACI == "":
		return fmt.Errorf("reaction target author %s: %w", reaction.TargetAuthor, ErrUnresolvable)
	}

	return nil
}

// hasContent reports whether req has a body, attachments, a quote or mentions.
func (req SendRequest) hasContent() bool {
	return req.Body != "" || len(req.Attachments) > 0 || req.Quote != nil || len(req.Mentions) > 0
}
