//go:build cgo

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
)

func (c *meowClient) Send(ctx context.Context, req SendRequest) (SendResult, error) {
	if c.cancelLoops == nil {
		return SendResult{}, ErrNotConnected
	}

	if !c.begin(&c.sending) {
		return SendResult{}, ErrClosed
	}
	defer c.sending.Done()

	err := checkSendRequest(req)
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

	timestamp := req.Timestamp
	if timestamp == 0 {
		timestamp = uint64(time.Now().UnixMilli()) //nolint:gosec // the clock is after 1970
	}

	profileKey := c.ownProfileKey(ctx)

	if req.GroupID != "" {
		results, err := sendGroup(ctx, cli, req.GroupID, dataMessage(req.Body, timestamp, profileKey))
		if err != nil {
			return SendResult{}, err
		}

		return SendResult{Timestamp: timestamp, Results: results}, nil
	}

	res := SendResult{Timestamp: timestamp, Results: make([]RecipientResult, 0, len(req.Recipients))}

	for _, rcpt := range req.Recipients {
		serviceID, err := aciServiceID(rcpt)
		if err != nil {
			res.Results = append(res.Results, RecipientResult{Recipient: rcpt, Err: err})

			continue
		}

		// SendMessage adds to the content (PNI signature), so every recipient gets its own. For
		// our own ACI it only sends the sync transcript (note-to-self); otherwise it sends the
		// sync transcript after the message.
		sent := cli.SendMessage(ctx, serviceID, signalmeow.WrapDataMessage(dataMessage(req.Body, timestamp, profileKey)))
		res.Results = append(res.Results, recipientResult(rcpt, rcpt.ACI == c.ownACI, sent))
	}

	return res, nil
}

// checkSendRequest rejects requests that 3.3 can't send yet or that have no single target.
func checkSendRequest(req SendRequest) error {
	if (req.GroupID == "") == (len(req.Recipients) == 0) {
		return ErrInvalidSendRequest
	}

	if len(req.Attachments) > 0 || req.Quote != nil {
		return fmt.Errorf("send attachments or quotes: %w (Phase 3.4)", ErrNotImplemented)
	}

	return nil
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

// dataMessage builds the DataMessage of a text message.
func dataMessage(body string, timestamp uint64, profileKey []byte) *signalpb.DataMessage {
	msg := &signalpb.DataMessage{
		Body:      new(body),
		Timestamp: new(timestamp),
	}

	if len(profileKey) > 0 {
		msg.ProfileKey = profileKey
	}

	return msg
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
		out.Err = sent.Error
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
		err := failed.Error
		if err == nil {
			err = ErrSendFailed
		}

		out = append(out, RecipientResult{Recipient: serviceRecipient(failed.Recipient), Err: err})
	}

	return out
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
