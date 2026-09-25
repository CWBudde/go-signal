package output

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/cwbudde/go-signal/internal/signal"
)

// columnGap separates the columns of plain tables.
const columnGap = 2

// AccountJSON is the "account" object of docs/json.md. The MCP server returns it as structured
// content.
type AccountJSON struct {
	Number     string    `json:"number"`
	ACI        string    `json:"aci"`
	PNI        string    `json:"pni,omitempty"`
	DeviceID   int       `json:"deviceId"`
	DeviceName string    `json:"deviceName,omitempty"`
	LinkedAt   time.Time `json:"linkedAt,omitzero"`
	UnlinkedAt time.Time `json:"unlinkedAt,omitzero"`
}

type accountDoc struct {
	Version int         `json:"version"`
	Account AccountJSON `json:"account"`
}

// deviceJSON is the "device" object of docs/json.md.
type deviceJSON struct {
	ID       int       `json:"id"`
	Name     string    `json:"name,omitempty"`
	Created  time.Time `json:"created,omitzero"`
	LastSeen time.Time `json:"lastSeen,omitzero"`
	Current  bool      `json:"current"`
}

type devicesDoc struct {
	Version int          `json:"version"`
	Devices []deviceJSON `json:"devices"`
}

type unlinkedJSON struct {
	Number     string    `json:"number"`
	ACI        string    `json:"aci"`
	LocalOnly  bool      `json:"localOnly"`
	UnlinkedAt time.Time `json:"unlinkedAt,omitzero"`
}

type unlinkedDoc struct {
	Version  int          `json:"version"`
	Unlinked unlinkedJSON `json:"unlinked"`
}

// NewAccountJSON converts acc to its JSON form.
func NewAccountJSON(acc signal.Account) AccountJSON {
	return AccountJSON{
		Number:     acc.Number,
		ACI:        acc.ACI,
		PNI:        acc.PNI,
		DeviceID:   acc.DeviceID,
		DeviceName: acc.DeviceName,
		LinkedAt:   utc(acc.LinkedAt),
		UnlinkedAt: utc(acc.UnlinkedAt),
	}
}

// Account prints a linked account (`account show`).
func (p *Printer) Account(acc signal.Account) error {
	if p.format == JSON {
		return p.writeJSON(accountDoc{Version: SchemaVersion, Account: NewAccountJSON(acc)})
	}

	table := tabwriter.NewWriter(p.w, 0, 0, 1, ' ', 0)
	fmt.Fprintf(table, "Number:\t%s\n", orDash(acc.Number))
	fmt.Fprintf(table, "ACI:\t%s\n", orDash(acc.ACI))
	fmt.Fprintf(table, "PNI:\t%s\n", orDash(acc.PNI))
	fmt.Fprintf(table, "Device ID:\t%d\n", acc.DeviceID)
	fmt.Fprintf(table, "Device name:\t%s\n", orDash(acc.DeviceName))
	fmt.Fprintf(table, "Linked at:\t%s\n", p.dateTime(acc.LinkedAt))
	fmt.Fprintf(table, "Status:\t%s\n", p.status(acc))

	return flush(table)
}

// Devices prints the devices of an account (`devices list`); plain output marks ours with *.
func (p *Printer) Devices(devices []signal.Device) error {
	if p.format == JSON {
		doc := devicesDoc{Version: SchemaVersion, Devices: make([]deviceJSON, 0, len(devices))}
		for _, dev := range devices {
			doc.Devices = append(doc.Devices, deviceJSON{
				ID:       dev.ID,
				Name:     dev.Name,
				Created:  utc(dev.Created),
				LastSeen: utc(dev.LastSeen),
				Current:  dev.Current,
			})
		}

		return p.writeJSON(doc)
	}

	table := tabwriter.NewWriter(p.w, 0, 0, columnGap, ' ', 0)
	fmt.Fprintln(table, "\tID\tNAME\tCREATED\tLAST SEEN")

	for _, dev := range devices {
		mark := ""
		if dev.Current {
			mark = "*"
		}

		fmt.Fprintf(table, "%s\t%d\t%s\t%s\t%s\n",
			mark, dev.ID, orDash(dev.Name), p.dateTime(dev.Created), p.date(dev.LastSeen))
	}

	return flush(table)
}

// Unlinked confirms `account unlink`.
func (p *Printer) Unlinked(acc signal.Account, localOnly bool) error {
	if p.format == JSON {
		return p.writeJSON(unlinkedDoc{Version: SchemaVersion, Unlinked: unlinkedJSON{
			Number: acc.Number, ACI: acc.ACI, LocalOnly: localOnly, UnlinkedAt: utc(acc.UnlinkedAt),
		}})
	}

	var err error

	switch {
	case acc.Unlinked():
		_, err = fmt.Fprintf(p.w, "Deleted the local data of %s (ACI %s); the device had already been unlinked.\n",
			acc.Number, acc.ACI)
	case localOnly:
		_, err = fmt.Fprintf(p.w, "Deleted the local data of %s (ACI %s). The device may still be listed on your phone.\n",
			acc.Number, acc.ACI)
	default:
		_, err = fmt.Fprintf(p.w, "Unlinked %s (ACI %s) and deleted its local data.\n", acc.Number, acc.ACI)
	}

	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

// status describes whether the account is still linked, as far as go-signal knows.
func (p *Printer) status(acc signal.Account) string {
	if acc.Unlinked() {
		return "unlinked (noticed " + p.dateTime(acc.UnlinkedAt) + ")"
	}

	return "linked"
}

func flush(table *tabwriter.Writer) error {
	err := table.Flush()
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}
