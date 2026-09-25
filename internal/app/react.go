package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cwbudde/go-signal/internal/signal"
)

var (
	// ErrInvalidTarget means that the target of a reaction is not <author>:<timestamp>, or the
	// target of a remote delete is not a timestamp.
	ErrInvalidTarget = errors.New("invalid target message")
	// ErrInvalidEmoji means that a reaction's emoji is empty or doesn't look like one emoji.
	ErrInvalidEmoji = errors.New("invalid emoji")
)

// maxEmojiRunes bounds an emoji: the longest ones (ZWJ sequences such as families, or
// subdivision flags with tag characters) have about ten code points.
const maxEmojiRunes = 16

// Code points that are not printable but occur in emoji sequences.
const (
	zeroWidthJoiner = '‍'
	tagFirst        = '\U000E0020'
	tagLast         = '\U000E007F'
)

// ReactRequest is the input of React.
type ReactRequest struct {
	// Recipients are the chats of the target message (see ParseRecipient): users, group:<id> and
	// self. The reaction goes to all of them.
	Recipients []string
	// Target is the message to react to, <author>:<timestamp> (see ParseTarget).
	Target string
	// Emoji is the reaction, a single emoji. To remove a reaction, it must be the one sent.
	Emoji string
	// Remove takes back our reaction Emoji on the target.
	Remove bool
}

// ReactResult is the output of React. Its SendResult has the reaction message's own timestamp.
type ReactResult struct {
	SendResult

	Emoji  string
	Remove bool
	// TargetAuthor is the resolved author of the target message (Self for our own).
	TargetAuthor Target
	// TargetTimestamp is the sent timestamp of the target message.
	TargetTimestamp uint64
}

// ParseTarget parses the target of a reaction, <author>:<timestamp>: the author of the message as
// a user recipient argument (see ParseRecipient; self for our own messages) and its sent
// timestamp in milliseconds, as receive -o json shows them.
func ParseTarget(arg string) (Target, uint64, error) {
	return parseMessageRef(arg, ErrInvalidTarget)
}

// React sends an emoji reaction on the message req.Target (or takes it back with req.Remove) to
// every chat in req.Recipients, like Send: in send-only mode, all arguments checked before
// connecting and all users resolved before anything is sent. The result and errors are those of
// Send (ErrSendFailed if some targets failed).
func (a *App) React(ctx context.Context, req ReactRequest) (ReactResult, error) {
	emoji := strings.TrimSpace(req.Emoji)

	errs := checkRecipients(req.Recipients)

	_, timestamp, err := ParseTarget(req.Target)
	if err != nil {
		errs = append(errs, err)
	}

	err = checkEmoji(emoji)
	if err != nil {
		errs = append(errs, err)
	}

	err = errors.Join(errs...)
	if err != nil {
		return ReactResult{}, fmt.Errorf("react: %w", err)
	}

	out := ReactResult{Emoji: emoji, Remove: req.Remove, TargetTimestamp: timestamp}

	out.SendResult, err = a.sendContent(ctx, "react", req.Recipients, func(ctx context.Context) (content, error) {
		author, _, err := ParseTarget(req.Target)
		if err != nil {
			return content{}, err
		}

		authors := []Target{author}

		err = a.resolveUsers(ctx, authors)
		if err != nil {
			return content{}, fmt.Errorf("resolve target author: %w", err)
		}

		out.TargetAuthor = authors[0]

		return content{reaction: &signal.OutgoingReaction{
			Emoji: emoji, Remove: req.Remove, TargetAuthor: authors[0].Recipient, TargetTimestamp: timestamp,
		}}, nil
	})

	return out, err
}

// checkEmoji reports whether emoji looks like a single emoji. It doesn't know Unicode's emoji
// list: it rejects empty or overlong strings, letters, white space and invisible characters (other
// than those that join emoji sequences), and anything purely ASCII.
func checkEmoji(emoji string) error {
	if emoji == "" {
		return fmt.Errorf("%w: it is empty", ErrInvalidEmoji)
	}

	if utf8.RuneCountInString(emoji) > maxEmojiRunes {
		return fmt.Errorf("%w %q: want a single emoji", ErrInvalidEmoji, emoji)
	}

	nonASCII := false

	for _, char := range emoji {
		switch {
		case char == zeroWidthJoiner, char >= tagFirst && char <= tagLast:
		case unicode.IsLetter(char), unicode.IsSpace(char), !unicode.IsPrint(char):
			return fmt.Errorf("%w %q: want a single emoji", ErrInvalidEmoji, emoji)
		}

		nonASCII = nonASCII || char > unicode.MaxASCII
	}

	if !nonASCII {
		return fmt.Errorf("%w %q: want a single emoji", ErrInvalidEmoji, emoji)
	}

	return nil
}

// DeleteRequest is the input of Delete.
type DeleteRequest struct {
	// Recipients are the chats the message went to (see ParseRecipient): users, group:<id> and
	// self.
	Recipients []string
	// Target is the sent timestamp (ms) of our own message to delete, as send printed it.
	Target uint64
}

// DeleteResult is the output of Delete. Its SendResult has the delete message's own timestamp.
type DeleteResult struct {
	SendResult

	// TargetTimestamp is the sent timestamp of the deleted message.
	TargetTimestamp uint64
}

// Delete deletes our own message with the sent timestamp req.Target for everyone in the chats
// req.Recipients (a remote delete), like Send: in send-only mode, all arguments checked before
// connecting. Only our own messages can be deleted; the recipients' clients ignore deletes of
// other messages, and ones that come too long after the message (the official apps only offer
// "delete for everyone" for a limited time). The result and errors are those of Send
// (ErrSendFailed if some targets failed).
func (a *App) Delete(ctx context.Context, req DeleteRequest) (DeleteResult, error) {
	errs := checkRecipients(req.Recipients)
	if req.Target == 0 {
		errs = append(errs, fmt.Errorf("%w: %w", ErrInvalidTarget, errBadTimestamp))
	}

	err := errors.Join(errs...)
	if err != nil {
		return DeleteResult{}, fmt.Errorf("delete: %w", err)
	}

	out := DeleteResult{TargetTimestamp: req.Target}

	out.SendResult, err = a.sendContent(ctx, "delete", req.Recipients, func(context.Context) (content, error) {
		return content{deleteTarget: req.Target}, nil
	})

	return out, err
}
