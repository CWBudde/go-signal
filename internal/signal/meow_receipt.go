//go:build cgo

package signal

import (
	"context"
	"fmt"

	"go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
)

func (c *meowClient) SendReceipt(ctx context.Context, sender Recipient, typ ReceiptType, timestamps []uint64) error {
	if c.cancelLoops == nil {
		return ErrNotConnected
	}

	content, err := receiptContent(typ, timestamps)
	if err != nil {
		return err
	}

	serviceID, err := aciServiceID(sender)
	if err != nil {
		return fmt.Errorf("%s receipt: %w", typ, err)
	}

	if !c.begin(&c.sending) {
		return ErrClosed
	}
	defer c.sending.Done()

	err = c.connectionLost()
	if err != nil {
		return fmt.Errorf("%s receipt: %w", typ, err)
	}

	c.cliMu.Lock()
	cli := c.cli
	c.cliMu.Unlock()

	// For a read receipt, signalmeow also sends the read sync to our other devices. It skips
	// receipts to senders we haven't accepted a message request from and reports success.
	sent := cli.SendMessage(c.zlog.WithContext(ctx), serviceID, content)

	res := recipientResult(sender, false, sent)
	if res.Err != nil {
		return fmt.Errorf("%s receipt to %s: %w", typ, sender, res.Err)
	}

	return nil
}

// receiptContent builds the receipt message for timestamps.
func receiptContent(typ ReceiptType, timestamps []uint64) (*signalpb.Content, error) {
	var pbType signalpb.ReceiptMessage_Type

	switch typ {
	case ReceiptDelivery:
		pbType = signalpb.ReceiptMessage_DELIVERY
	case ReceiptRead:
		pbType = signalpb.ReceiptMessage_READ
	case ReceiptViewed:
		pbType = signalpb.ReceiptMessage_VIEWED
	default:
		return nil, fmt.Errorf("%w: type %d", ErrInvalidReceipt, typ)
	}

	if len(timestamps) == 0 {
		return nil, fmt.Errorf("%w: no timestamps", ErrInvalidReceipt)
	}

	return &signalpb.Content{Content: &signalpb.Content_ReceiptMessage{ReceiptMessage: &signalpb.ReceiptMessage{
		Type:      pbType.Enum(),
		Timestamp: append([]uint64(nil), timestamps...),
	}}}, nil
}
