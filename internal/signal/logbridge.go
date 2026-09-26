package signal

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/rs/zerolog"
)

// NewZerologBridge returns a zerolog logger (what signalmeow logs through) that forwards every
// entry to slog. signalmeow's info logs are routine connection chatter, so they are demoted to
// debug and only show up with -v. So are its errors about the websocket state ("Authed websocket
// logged out", ...): the client reports those as Connection events, and a command turns them into
// one clear error (e.g. ErrDeviceUnlinked) instead of a stack of websocket errors.
func NewZerologBridge(logger *slog.Logger) zerolog.Logger {
	return zerolog.New(slogWriter{logger: logger}).Level(zerolog.DebugLevel)
}

type slogWriter struct {
	logger *slog.Logger
}

// Write decodes one JSON log line from zerolog and re-emits it via slog.
func (w slogWriter) Write(line []byte) (int, error) {
	var fields map[string]any

	err := json.Unmarshal(line, &fields)
	if err != nil {
		w.logger.Debug(string(line))

		return len(line), nil //nolint:nilerr // a malformed line is logged verbatim, not an error
	}

	level := slogLevel(fields[zerolog.LevelFieldName])
	msg, _ := fields[zerolog.MessageFieldName].(string)

	if isWebsocketStatus(msg) || isDeferredEnvelope(msg, fields) || isGroupCacheMiss(msg, fields) {
		level = slog.LevelDebug
	}

	delete(fields, zerolog.LevelFieldName)
	delete(fields, zerolog.MessageFieldName)

	attrs := make([]slog.Attr, 0, len(fields))
	for k, v := range fields {
		attrs = append(attrs, slog.Any(k, v))
	}

	w.logger.LogAttrs(context.Background(), level, msg, attrs...)

	return len(line), nil
}

func slogLevel(level any) slog.Level {
	name, _ := level.(string)

	parsed, err := zerolog.ParseLevel(name)
	if err != nil {
		return slog.LevelInfo
	}

	switch {
	case parsed >= zerolog.ErrorLevel:
		return slog.LevelError
	case parsed == zerolog.WarnLevel:
		return slog.LevelWarn
	default:
		return slog.LevelDebug
	}
}

// handlerFailed is the text of signalmeow.ErrHandlerFailed (this file also builds without cgo,
// so it can't import signalmeow).
const handlerFailed = "event handler returned non-success status"

// isDeferredEnvelope reports whether an entry is about an envelope that our handler left on the
// server on purpose (send-only mode, or while closing): signalmeow logs that as an error ("Error
// handling request") and a warning, but the envelope just comes again next time.
func isDeferredEnvelope(msg string, fields map[string]any) bool {
	errText, _ := fields[zerolog.ErrorFieldName].(string)

	return errText == handlerFailed ||
		msg == "Not clearing buffered event plaintext due to handler failure"
}

// isWebsocketStatus reports whether msg is one of signalmeow's websocket status logs, e.g.
// "Authed websocket logged out" or "Unauthed websocket disconnected".
func isWebsocketStatus(msg string) bool {
	return strings.HasPrefix(msg, "Authed websocket ") || strings.HasPrefix(msg, "Unauthed websocket ")
}

// noEndorsements is how signalmeow's GroupCache.Put fails when the group response carries no
// (valid) send endorsements: GetExpiration is the first thing that parses them.
const noEndorsements = "failed to get endorsement expiration: "

// isGroupCacheMiss reports whether an entry is signalmeow's complaint that it couldn't cache a
// fetched group because the server sent no send endorsements with it. That is expected for a
// group we are only invited to; the group itself was fetched fine. Other failures to cache a
// group keep their level. The follow-up "Group not found in cache after fetching" (a warning
// without an error) is always demoted: the entry before it tells what went wrong.
func isGroupCacheMiss(msg string, fields map[string]any) bool {
	errText, _ := fields[zerolog.ErrorFieldName].(string)

	return (msg == "Failed to cache group response" && strings.HasPrefix(errText, noEndorsements)) ||
		msg == "Group not found in cache after fetching"
}
