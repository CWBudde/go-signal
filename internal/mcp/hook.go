package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	// hookQueue is how many messages wait for the hook; more are not handed to it.
	hookQueue = 64
	// hookOutputLimit caps what is logged of a run's stdout and stderr each.
	hookOutputLimit = 64 << 10
	// hookWaitDelay is how long a run may keep its output open after it was killed.
	hookWaitDelay = 5 * time.Second
)

// Environment variables that a hook run gets besides the server's own.
const (
	envEntryID = "GOSIGNAL_ENTRY_ID"
	envChat    = "GOSIGNAL_CHAT"
	envSender  = "GOSIGNAL_SENDER"
)

// hook runs Options.OnMessage for the incoming messages of the chats that Options.HookFrom
// allows, one run at a time in the order the messages arrived.
type hook struct {
	app     *app.App
	program string
	from    *app.Allowlist
	timeout time.Duration
	logger  *slog.Logger
	queue   chan signal.InboxEntry
}

// newHook returns the hook of opts, nil without Options.OnMessage.
func newHook(a *app.App, opts Options, logger *slog.Logger) *hook {
	if opts.OnMessage == "" {
		return nil
	}

	return &hook{
		app: a, program: opts.OnMessage, from: opts.HookFrom, timeout: opts.HookTimeout, logger: logger,
		queue: make(chan signal.InboxEntry, hookQueue),
	}
}

// offer queues entry for a run if it is an incoming message (no sync transcript of our own) from
// a chat that the hook allows. It doesn't wait for the run; when the queue is full, the entry is
// left out. Either way the entry stays in the inbox.
func (h *hook) offer(ctx context.Context, entry signal.InboxEntry) {
	msg, ok := entry.Event.(*signal.Message)
	if !ok || msg.Sync || !entry.Unread {
		return
	}

	logger := h.logger.With("entry", app.FormatCursor(entry.ID))

	allowed, err := h.app.ChatAllowed(ctx, h.from, entry.Chat)
	switch {
	case err != nil:
		logger.WarnContext(ctx, "hook: chat not checked, message left out", "error", err)

		return
	case !allowed:
		logger.DebugContext(ctx, "hook: chat not in --hook-from", "chat", entry.Chat.Key())

		return
	}

	select {
	case h.queue <- entry:
	default:
		logger.WarnContext(ctx, "hook: queue full, message left out")
	}
}

// run runs the hook for the queued entries until ctx ends; a run in progress is killed then.
func (h *hook) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case entry := <-h.queue:
			h.exec(ctx, entry)
		}
	}
}

// exec runs the program once for entry, with the entry as a line of JSON (as messages_list shows
// it) on stdin, and logs its output and failure.
func (h *hook) exec(ctx context.Context, entry signal.InboxEntry) {
	entryID := app.FormatCursor(entry.ID)
	logger := h.logger.With("entry", entryID)

	names, err := h.app.Names(ctx)
	if err != nil {
		logger.DebugContext(ctx, "hook: names not loaded", "error", err)
	}

	payload, err := json.Marshal(output.NewInboxEntryJSON(entry, names))
	if err != nil {
		logger.WarnContext(ctx, "hook: encode message", "error", err)

		return
	}

	if h.timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, h.timeout)
		defer cancel()
	}

	var stdout, stderr limitedBuffer

	cmd := exec.CommandContext(ctx, h.program)
	cmd.Stdin = bytes.NewReader(append(payload, '\n'))
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	cmd.Env = append(os.Environ(),
		envEntryID+"="+entryID, envChat+"="+entry.Chat.Key(), envSender+"="+senderArg(entry.Event))
	cmd.WaitDelay = hookWaitDelay
	killGroup(cmd)

	start := time.Now()
	err = cmd.Run()

	logLines(ctx, logger, "stdout", stdout.String())
	logLines(ctx, logger, "stderr", stderr.String())

	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		logger.WarnContext(ctx, "hook: killed after --on-message-timeout", "timeout", h.timeout)
	case ctx.Err() != nil:
		logger.InfoContext(ctx, "hook: killed, the server stops")
	case err != nil:
		logger.WarnContext(ctx, "hook failed", "error", err, "duration", time.Since(start))
	default:
		logger.InfoContext(ctx, "hook ran", "duration", time.Since(start))
	}
}

// senderArg is the sender of a message as send_message takes it: the number if known, else the ACI.
func senderArg(evt signal.Event) string {
	msg, ok := evt.(*signal.Message)
	if !ok {
		return ""
	}

	if msg.Sender.Number != "" {
		return msg.Sender.Number
	}

	return msg.Sender.String()
}

// logLines logs every non-empty line of out.
func logLines(ctx context.Context, logger *slog.Logger, stream, out string) {
	for line := range strings.Lines(out) {
		line = strings.TrimRight(line, "\r\n")
		if line != "" {
			logger.InfoContext(ctx, "hook output", "stream", stream, "line", line)
		}
	}
}

// limitedBuffer keeps the first hookOutputLimit bytes written to it and drops the rest.
type limitedBuffer struct {
	bytes.Buffer
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := hookOutputLimit - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}

	return len(p), nil
}
