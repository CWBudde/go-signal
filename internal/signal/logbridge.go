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

	if isWebsocketStatus(msg) {
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

// isWebsocketStatus reports whether msg is one of signalmeow's websocket status logs, e.g.
// "Authed websocket logged out" or "Unauthed websocket disconnected".
func isWebsocketStatus(msg string) bool {
	return strings.HasPrefix(msg, "Authed websocket ") || strings.HasPrefix(msg, "Unauthed websocket ")
}
