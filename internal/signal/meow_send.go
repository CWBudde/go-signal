//go:build cgo || purego

package signal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/types"
	"google.golang.org/protobuf/proto"
)

func (c *meowClient) Upload(ctx context.Context, attachments []OutgoingAttachment) ([]UploadedAttachment, error) {
	if c.cancelLoops == nil {
		return nil, ErrNotConnected
	}

	if !c.begin(&c.sending) {
		return nil, ErrClosed
	}
	defer c.sending.Done()

	err := c.connectionLost()
	if err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}

	c.cliMu.Lock()
	cli := c.cli
	c.cliMu.Unlock()

	ctx = c.zlog.WithContext(ctx)
	out := make([]UploadedAttachment, 0, len(attachments))

	for _, att := range attachments {
		pointer, err := cli.UploadAttachment(ctx, att.Data)
		if err != nil {
			return nil, fmt.Errorf("upload %s: %w", att.Filename, err)
		}

		out = append(out, c.addUpload(pointerMetadata(pointer, att, time.Now())))
	}

	return out, nil
}

// pointerMetadata fills in what signalmeow's UploadAttachment leaves out of pointer.
func pointerMetadata(pointer *signalpb.AttachmentPointer, att OutgoingAttachment, now time.Time,
) *signalpb.AttachmentPointer {
	pointer.ContentType = new(att.ContentType)
	pointer.UploadTimestamp = new(uint64(now.UnixMilli())) //nolint:gosec // the clock is after 1970

	if att.Filename != "" {
		pointer.FileName = new(att.Filename)
	}

	if att.Width > 0 && att.Height > 0 {
		pointer.Width = new(att.Width)
		pointer.Height = new(att.Height)
	}

	return pointer
}

// addUpload keeps pointer for Send and returns its handle.
func (c *meowClient) addUpload(pointer *signalpb.AttachmentPointer) UploadedAttachment {
	c.uploadsMu.Lock()
	defer c.uploadsMu.Unlock()

	if c.uploads == nil {
		c.uploads = make(map[string]*signalpb.AttachmentPointer)
	}

	id := uuid.NewString()
	c.uploads[id] = pointer

	return UploadedAttachment{
		ID: id, ContentType: pointer.GetContentType(), Filename: pointer.GetFileName(), Size: pointer.GetSize(),
	}
}

// uploadedPointers returns the pointers of the attachments of a SendRequest.
func (c *meowClient) uploadedPointers(attachments []UploadedAttachment) ([]*signalpb.AttachmentPointer, error) {
	c.uploadsMu.Lock()
	defer c.uploadsMu.Unlock()

	out := make([]*signalpb.AttachmentPointer, 0, len(attachments))

	for _, att := range attachments {
		pointer, ok := c.uploads[att.ID]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownAttachment, att.Filename)
		}

		out = append(out, pointer)
	}

	return out, nil
}

func (c *meowClient) Send(ctx context.Context, req SendRequest) (SendResult, error) {
	if c.cancelLoops == nil {
		return SendResult{}, ErrNotConnected
	}

	if !c.begin(&c.sending) {
		return SendResult{}, ErrClosed
	}
	defer c.sending.Done()

	if req.Timestamp == 0 {
		req.Timestamp = uint64(time.Now().UnixMilli()) //nolint:gosec // the clock is after 1970
	}

	msg, err := c.message(ctx, req)
	if err != nil {
		return SendResult{}, err
	}

	err = c.connectionLost()
	if err != nil {
		return SendResult{}, fmt.Errorf("send: %w", err)
	}

	c.cliMu.Lock()
	cli := c.cli
	c.cliMu.Unlock()

	ctx = c.zlog.WithContext(ctx)

	if req.GroupID != "" {
		results, err := sendGroup(ctx, cli, req.GroupID, msg())
		if err != nil {
			return SendResult{}, err
		}

		return SendResult{Timestamp: req.Timestamp, Results: results}, nil
	}

	res := SendResult{Timestamp: req.Timestamp, Results: make([]RecipientResult, 0, len(req.Recipients))}

	for _, rcpt := range req.Recipients {
		serviceID, err := aciServiceID(rcpt)
		if err != nil {
			res.Results = append(res.Results, RecipientResult{Recipient: rcpt, Err: err})

			continue
		}

		// SendMessage adds to the content (PNI signature), so every recipient gets its own. For
		// our own ACI it only sends the sync transcript (note-to-self); otherwise it sends the
		// sync transcript after the message.
		sent := cli.SendMessage(ctx, serviceID, signalmeow.WrapDataMessage(msg()))
		res.Results = append(res.Results, recipientResult(rcpt, rcpt.ACI == c.ownACI, sent))
	}

	return res, nil
}

// message checks req and returns a function that builds a fresh DataMessage for it, since
// signalmeow adds to the message of every send.
func (c *meowClient) message(ctx context.Context, req SendRequest) (func() *signalpb.DataMessage, error) {
	err := req.Check()
	if err != nil {
		return nil, err
	}

	pointers, err := c.uploadedPointers(req.Attachments)
	if err != nil {
		return nil, err
	}

	msg, err := dataMessage(req, pointers, c.ownProfileKey(ctx))
	if err != nil {
		return nil, err
	}

	return func() *signalpb.DataMessage {
		return proto.CloneOf(msg)
	}, nil
}

// ownProfileKey returns our profile key to include in messages, so that recipients can decrypt
// our profile (name, avatar), as the official clients and signal-cli do. Linking stores it; nil
// if it is unknown.
func (c *meowClient) ownProfileKey(ctx context.Context) []byte {
	key, err := c.connDevice.RecipientStore.MyProfileKey(ctx)
	if err != nil || key == nil {
		c.log.Debug("sending without profile key", "error", err)

		return nil
	}

	return key.Slice()
}

// dataMessage builds the DataMessage of req with the uploaded attachments.
func dataMessage(req SendRequest, attachments []*signalpb.AttachmentPointer, profileKey []byte,
) (*signalpb.DataMessage, error) {
	msg := &signalpb.DataMessage{
		Timestamp:   new(req.Timestamp),
		Attachments: attachments,
	}

	if req.Body != "" {
		msg.Body = new(req.Body)
	}

	if len(profileKey) > 0 {
		msg.ProfileKey = profileKey
	}

	for _, mention := range req.Mentions {
		aci, err := aciBytes(mention.Recipient)
		if err != nil {
			return nil, fmt.Errorf("mention: %w", err)
		}

		msg.BodyRanges = append(msg.BodyRanges, &signalpb.BodyRange{
			Start:           new(mention.Start),
			Length:          new(mention.Length),
			AssociatedValue: &signalpb.BodyRange_MentionAciBinary{MentionAciBinary: aci},
		})
	}

	err := addReactionOrDelete(msg, req)
	if err != nil {
		return nil, err
	}

	if req.Quote != nil {
		aci, err := aciBytes(req.Quote.Author)
		if err != nil {
			return nil, fmt.Errorf("quote: %w", err)
		}

		msg.Quote = &signalpb.DataMessage_Quote{
			Id:              new(req.Quote.Timestamp),
			AuthorAciBinary: aci,
			Type:            signalpb.DataMessage_Quote_NORMAL.Enum(),
		}

		if req.Quote.Text != "" {
			msg.Quote.Text = new(req.Quote.Text)
		}
	}

	return msg, nil
}

// addReactionOrDelete adds the reaction or remote delete of req to msg, as the official clients
// and signal-cli send them.
func addReactionOrDelete(msg *signalpb.DataMessage, req SendRequest) error {
	if req.DeleteTarget != 0 {
		msg.Delete = &signalpb.DataMessage_Delete{TargetSentTimestamp: new(req.DeleteTarget)}
	}

	reaction := req.Reaction
	if reaction == nil {
		return nil
	}

	aci, err := aciBytes(reaction.TargetAuthor)
	if err != nil {
		return fmt.Errorf("reaction: %w", err)
	}

	msg.RequiredProtocolVersion = new(uint32(signalpb.DataMessage_REACTIONS))
	msg.Reaction = &signalpb.DataMessage_Reaction{
		Emoji:                 new(reaction.Emoji),
		Remove:                new(reaction.Remove),
		TargetAuthorAciBinary: aci,
		TargetSentTimestamp:   new(reaction.TargetTimestamp),
	}

	return nil
}

// aciBytes returns the 16-byte ACI of rcpt.
func aciBytes(rcpt Recipient) ([]byte, error) {
	id, err := aciServiceID(rcpt)
	if err != nil {
		return nil, err
	}

	return id.UUID[:], nil
}

// sendGroup sends msg to the members of the group groupID (with sender keys where possible);
// signalmeow adds the group context and sends the sync transcript.
func sendGroup(
	ctx context.Context, cli *signalmeow.Client, groupID string, msg *signalpb.DataMessage,
) ([]RecipientResult, error) {
	gid := types.GroupIdentifier(groupID)

	_, err := gid.Bytes()
	if err != nil {
		return nil, fmt.Errorf("%w %q: %w", ErrUnknownGroup, groupID, err)
	}

	sent, err := cli.SendGroupMessage(ctx, gid, signalmeow.WrapDataMessage(msg))
	if errors.Is(err, signalmeow.ErrGroupMasterKeyNotFound) {
		return nil, fmt.Errorf("%w %s: go-signal knows a group only after receiving a message from it",
			ErrUnknownGroup, groupID)
	}

	if err != nil {
		return nil, fmt.Errorf("send to group %s: %w", groupID, err)
	}

	return groupResults(sent), nil
}

// aciServiceID returns the service ID messages to rcpt are sent to.
func aciServiceID(rcpt Recipient) (libsignalgo.ServiceID, error) {
	if rcpt.ACI == "" {
		return libsignalgo.ServiceID{}, fmt.Errorf("%s: %w", rcpt, ErrUnresolvable)
	}

	aci, err := uuid.Parse(rcpt.ACI)
	if err != nil {
		return libsignalgo.ServiceID{}, fmt.Errorf("%w: invalid ACI %q: %w", ErrUnresolvable, rcpt.ACI, err)
	}

	return libsignalgo.NewACIServiceID(aci), nil
}

// recipientResult converts signalmeow's result for rcpt. For a note-to-self (self), signalmeow
// only reports whether the sync transcript went out.
func recipientResult(rcpt Recipient, self bool, sent signalmeow.SendMessageResult) RecipientResult {
	out := RecipientResult{Recipient: rcpt}

	switch {
	case sent.WasSuccessful:
		out.Unidentified = sent.Unidentified
	case sent.Error != nil:
		out.Err = sendError(rcpt, sent.Error)
	case self:
		out.Err = ErrSyncFailed
	default:
		out.Err = ErrSendFailed
	}

	return out
}

// groupResults converts signalmeow's result of a group send into one entry per member.
func groupResults(sent *signalmeow.GroupMessageSendResult) []RecipientResult {
	if sent == nil {
		return nil
	}

	out := make([]RecipientResult, 0, len(sent.SuccessfullySentTo)+len(sent.FailedToSendTo))

	for _, ok := range sent.SuccessfullySentTo {
		out = append(out, RecipientResult{Recipient: serviceRecipient(ok.Recipient), Unidentified: ok.Unidentified})
	}

	for _, failed := range sent.FailedToSendTo {
		rcpt := serviceRecipient(failed.Recipient)

		err := failed.Error
		if err == nil {
			err = ErrSendFailed
		}

		out = append(out, RecipientResult{Recipient: rcpt, Err: sendError(rcpt, err)})
	}

	return out
}

// sendError converts the error of sending to rcpt: libsignal refuses to encrypt for an identity
// key that isn't trusted (see identityTrust), which becomes UntrustedError.
func sendError(rcpt Recipient, err error) error {
	if errors.Is(err, libsignalgo.ErrorCodeUntrustedIdentity) {
		return UntrustedError(rcpt)
	}

	return err
}

// serviceRecipient converts a service ID (ACI or PNI) into a Recipient.
func serviceRecipient(id libsignalgo.ServiceID) Recipient {
	if id.Type == libsignalgo.ServiceIDTypePNI {
		return Recipient{PNI: id.UUID.String()}
	}

	return Recipient{ACI: id.UUID.String()}
}

// noteConnection records the error of a Connection event that ends the connection for good.
func (c *meowClient) noteConnection(evt Event) {
	conn, ok := evt.(*Connection)
	if !ok || (conn.State != StateLoggedOut && conn.State != StateFailed) {
		return
	}

	err := conn.Err
	if err == nil {
		err = ErrConnectionFailed
		if conn.State == StateLoggedOut {
			err = ErrDeviceUnlinked
		}
	}

	c.lostMu.Lock()
	defer c.lostMu.Unlock()

	if c.lost == nil {
		c.lost = err
	}
}

// connectionLost returns the error of a connection lost for good in send-only mode, or nil.
func (c *meowClient) connectionLost() error {
	c.lostMu.Lock()
	defer c.lostMu.Unlock()

	return c.lost
}
