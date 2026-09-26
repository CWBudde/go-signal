package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
)

// ErrUnhealthy means that a health check failed (see DoctorError).
var ErrUnhealthy = errors.New("health check failed")

// CheckStatus is the outcome of a Check.
type CheckStatus string

// Check outcomes. A warning points at something that may be intended; only a failure makes the
// report unhealthy.
const (
	CheckOK   CheckStatus = "ok"
	CheckWarn CheckStatus = "warn"
	CheckFail CheckStatus = "fail"
)

// Check is one item of a health report.
type Check struct {
	// Name identifies the check, e.g. "account" or "server".
	Name   string
	Status CheckStatus
	// Detail says what was found; Hint, if set, what to do about it.
	Detail string
	Hint   string
	// Err is the cause of a failure or warning, if there is one.
	Err error
}

// DoctorRequest selects the checks of Doctor beyond the local ones.
type DoctorRequest struct {
	// Lock checks that no other process holds the account lock.
	Lock bool
	// Server asks Signal's server whether this device is still linked.
	Server bool
}

// Doctor checks the selected account: that it is linked and not marked as unlinked, that the
// account lock is free (req.Lock), that Signal's server still lists this device (req.Server), and
// that the inbox can be read. When there is no usable account, the other checks are left out.
// Doctor doesn't fail: the checks carry the errors (see DoctorError).
func (a *App) Doctor(ctx context.Context, req DoctorRequest) []Check {
	acc, check := a.checkAccount(ctx)
	if check.Status == CheckFail {
		return []Check{check}
	}

	checks := []Check{check}

	if req.Lock {
		checks = append(checks, a.checkLock(ctx))
	}

	if req.Server {
		checks = append(checks, a.checkServer(ctx, acc))
	}

	return append(checks, a.checkInbox(ctx))
}

// DoctorError returns nil if no check failed, else an error wrapping ErrUnhealthy and the
// failures' causes (so that e.g. signal.ErrDeviceUnlinked is found with errors.Is).
func DoctorError(checks []Check) error {
	var causes []error

	failed := false

	for _, check := range checks {
		if check.Status != CheckFail {
			continue
		}

		failed = true

		if check.Err != nil {
			causes = append(causes, check.Err)
		}
	}

	if !failed {
		return nil
	}

	if len(causes) == 0 {
		return ErrUnhealthy
	}

	return fmt.Errorf("%w: %w", ErrUnhealthy, errors.Join(causes...))
}

// PolicyCheck describes the safety settings of the MCP server's tools that send (see
// WithAllowlist and `mcp serve`) as a check. It warns when they send to anyone (allow is nil or
// allows all) or to nobody.
func PolicyCheck(readOnly bool, allow *Allowlist, attachDir string, confirm bool) Check {
	check := Check{Name: "policy", Status: CheckOK}

	if readOnly {
		check.Detail = "read-only: no tool sends"

		return check
	}

	var parts []string

	switch {
	case allow == nil || allow.All():
		check.Status = CheckWarn
		check.Hint = "the MCP client may send to anyone; list the users and groups with --allow-recipient instead of '*'"

		parts = append(parts, "sends to anyone")
	case allow.Empty():
		check.Status = CheckWarn
		check.Hint = "send_message, react and delete_message reject every recipient; " +
			"allow some with --allow-recipient, or use --read-only"

		parts = append(parts, "sends to nobody")
	default:
		if allow.Len() == 1 {
			parts = append(parts, "sends to 1 allowed recipient")
		} else {
			parts = append(parts, fmt.Sprintf("sends to %d allowed recipients", allow.Len()))
		}
	}

	if attachDir == "" {
		parts = append(parts, "no attachments")
	} else {
		parts = append(parts, "attachments from "+attachDir)
	}

	if confirm {
		parts = append(parts, "the user confirms each message")
	}

	check.Detail = strings.Join(parts, "; ")

	return check
}

func (a *App) checkAccount(ctx context.Context) (signal.Account, Check) {
	check := Check{Name: "account"}

	acc, err := a.client.Account(ctx)

	switch {
	case errors.Is(err, signal.ErrNotLinked):
		check.Status, check.Detail, check.Err = CheckFail, "no linked account", err
		check.Hint = "link this device with `go-signal link`"
	case err != nil:
		check.Status, check.Detail, check.Err = CheckFail, err.Error(), err
	case acc.Unlinked():
		check.Status, check.Err = CheckFail, signal.UnlinkedError(acc)
		check.Detail = fmt.Sprintf("%s: this device was unlinked (noticed %s)",
			acc.Number, acc.UnlinkedAt.UTC().Format("2006-01-02 15:04:05 MST"))
		check.Hint = "link again with `go-signal link`; `go-signal account unlink` deletes the old data"
	default:
		check.Status = CheckOK
		check.Detail = fmt.Sprintf("%s (ACI %s, device %d)", acc.Number, acc.ACI, acc.DeviceID)
	}

	return acc, check
}

func (a *App) checkLock(ctx context.Context) Check {
	check := Check{Name: "lock", Status: CheckOK, Detail: "free"}

	err := a.client.CheckLock(ctx)

	switch {
	case errors.Is(err, signal.ErrAccountInUse):
		check.Status, check.Detail, check.Err = CheckWarn, err.Error(), err
		check.Hint = "fine if this is your running MCP server; a second `mcp serve` (or `receive`) " +
			"for this account fails until it stops"
	case err != nil:
		check.Status, check.Detail, check.Err = CheckFail, err.Error(), err
	}

	return check
}

func (a *App) checkServer(ctx context.Context, acc signal.Account) Check {
	check := Check{Name: "server"}

	devices, err := a.client.Devices(ctx)

	switch {
	case errors.Is(err, signal.ErrDeviceUnlinked):
		check.Status, check.Detail, check.Err = CheckFail, "the server rejected this device: it was unlinked", err
		check.Hint = "link again with `go-signal link`"

		return check
	case err != nil:
		check.Status, check.Detail, check.Err = CheckFail, "can't reach Signal's server: "+err.Error(), err
		check.Hint = "check the network connection"

		return check
	}

	for _, dev := range devices {
		if dev.Current {
			check.Status = CheckOK
			check.Detail = fmt.Sprintf("reachable; device %d is linked (%d devices)", dev.ID, len(devices))

			return check
		}
	}

	check.Status, check.Err = CheckFail, signal.ErrDeviceUnlinked
	check.Detail = fmt.Sprintf("device %d is missing from the account's devices", acc.DeviceID)
	check.Hint = "link again with `go-signal link`"

	return check
}

func (a *App) checkInbox(ctx context.Context) Check {
	check := Check{Name: "inbox"}

	chats, err := a.client.InboxChats(ctx)
	if err != nil {
		check.Status, check.Detail, check.Err = CheckFail, "can't read the inbox: "+err.Error(), err

		return check
	}

	entries, unread := 0, 0
	for _, chat := range chats {
		entries += chat.Entries
		unread += chat.Unread
	}

	check.Status = CheckOK
	check.Detail = fmt.Sprintf("%d entries in %d chats, %d unread", entries, len(chats), unread)

	return check
}

// errNotADir means that a path that should be a directory is none.
var errNotADir = errors.New("not a directory")

// DownloadDirCheck checks that attachments can be saved to dir (see Download), which is created
// on the first download if needed.
func DownloadDirCheck(dir string) Check {
	check := Check{Name: "download dir", Status: CheckOK, Detail: dir}

	info, err := os.Stat(dir)

	switch {
	case errors.Is(err, os.ErrNotExist):
		check.Detail = dir + " (created on the first download)"

		return check
	case err != nil:
	case !info.IsDir():
		err = errNotADir
	default:
		var file *os.File

		file, err = os.CreateTemp(dir, ".doctor-*")
		if err == nil {
			_ = file.Close()
			_ = os.Remove(file.Name())
		}
	}

	if err != nil {
		check.Status, check.Err = CheckFail, fmt.Errorf("download dir %s: %w", dir, err)
		check.Detail = fmt.Sprintf("%s: %v", dir, err)
		check.Hint = "attachment_get can't save attachments; fix the directory or set --download-dir"
	}

	return check
}
