package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type doctorInput struct {
	CheckServer bool `json:"checkServer,omitempty" jsonschema:"also ask Signal's server if this device is still linked"`
}

// addDoctor registers the doctor tool, also in read-only mode.
func addDoctor(server *sdk.Server, handlers *tools) {
	sdk.AddTool(server, &sdk.Tool{
		Name:  "doctor",
		Title: "Check health",
		Description: "Check whether this server works: its version and uptime, the account, the connection to " +
			"Signal (reconnecting or not), the inbox, the download directory and which recipients the tools may " +
			"send to. Each check is ok, warn or fail, with a hint what to do. Call it when tools fail or no messages " +
			"arrive; with checkServer it also asks Signal's server whether this device is still linked.",
		Annotations: readOnly(),
	}, handlers.doctor)
}

func (t *tools) doctor(
	ctx context.Context, _ *sdk.CallToolRequest, in doctorInput,
) (*sdk.CallToolResult, output.DoctorJSON, error) {
	server := app.Check{
		Name: "mcp", Status: app.CheckOK,
		Detail: fmt.Sprintf("go-signal %s, up %s", t.version, time.Since(t.started).Round(time.Second)),
	}

	checks := append([]app.Check{server}, t.app.Doctor(ctx, app.DoctorRequest{Server: in.CheckServer})...)
	checks = append(checks,
		t.connectionCheck(),
		app.DownloadDirCheck(t.dir),
		app.PolicyCheck(t.readOnly, t.app.Allowlist(), t.attachDir, t.confirmer != nil),
	)

	res, err := t.plain(app.Names{}, func(p *output.Printer) error { return p.Doctor(checks) })

	return res, output.NewDoctorJSON(checks), err
}

// connectionCheck reports the connection state that the inbox has seen.
func (t *tools) connectionCheck() app.Check {
	status := t.inbox.Connection()
	check := app.Check{Name: "connection", Status: app.CheckOK, Err: status.Err}

	switch status.State {
	case 0:
		check.Detail = "connected at startup, no change since"
	case signal.StateConnected:
		check.Detail = "connected since " + t.time(status.Since)
	case signal.StateDisconnected, signal.StateError:
		check.Status = app.CheckWarn
		check.Detail = fmt.Sprintf("%s since %s", status.State, t.time(status.Since))
		check.Hint = "go-signal reconnects on its own; check the network if this lasts"
	case signal.StateLoggedOut, signal.StateFailed: // the receiving ends on these, and with it the server
		check.Status = app.CheckFail
		check.Detail = fmt.Sprintf("%s since %s", status.State, t.time(status.Since))
		check.Hint = "restart the server; run `go-signal mcp doctor` if it doesn't start"
	}

	if status.Err != nil {
		check.Detail += ": " + status.Err.Error()
	}

	if !status.LastEvent.IsZero() {
		check.Detail += "; last event " + t.time(status.LastEvent)
	}

	return check
}

// time formats a time of the text output.
func (t *tools) time(at time.Time) string {
	return at.In(t.loc).Format("2006-01-02 15:04:05 MST")
}
