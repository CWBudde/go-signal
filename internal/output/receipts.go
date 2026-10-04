package output

import (
	"fmt"

	"github.com/cwbudde/go-signal/internal/app"
)

type receiptSentJSON struct {
	Type       string        `json:"type"`
	Sender     recipientJSON `json:"sender"`
	Timestamps []uint64      `json:"timestamps"`
}

type receiptSentDoc struct {
	Version int             `json:"version"`
	Receipt receiptSentJSON `json:"receipt"`
}

// ViewedReceipt prints the successful submission of an explicit viewed receipt.
func (p *Printer) ViewedReceipt(res app.ViewedReceiptResult) error {
	if p.format == JSON {
		return p.writeJSON(receiptSentDoc{Version: SchemaVersion, Receipt: receiptSentJSON{
			Type: "viewed", Sender: p.recipient(res.Sender), Timestamps: res.Timestamps,
		}})
	}

	return p.writeLine(fmt.Sprintf("Submitted viewed receipt to %s for timestamps %v.", p.who(res.Sender), res.Timestamps))
}
