package signal_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	levelDebug = "DEBUG"
	keyError   = "error"
)

var (
	errBoom          = errors.New("boom")
	errHandlerFailed = errors.New("event handler returned non-success status")
	// errNoEndorsements is how signalmeow fails to cache a group we are only invited to.
	errNoEndorsements = errors.New("failed to get endorsement expiration: 3: unexpected panic: should have been " +
		"parsed previously: ZkGroupDeserializationFailure(...)")
)

func TestZerologBridge(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	zlog := signal.NewZerologBridge(logger)

	zlog.Info().Str("websocket_type", "authed").Msg("connecting")
	zlog.Error().Err(errBoom).Msg("failed")
	zlog.Error().Err(errBoom).Msg("Authed websocket logged out")
	zlog.Error().Err(errHandlerFailed).Msg("Error handling request")
	zlog.Warn().Msg("Not clearing buffered event plaintext due to handler failure")
	zlog.Error().Err(errNoEndorsements).Msg("Failed to cache group response")
	zlog.Error().Err(errBoom).Msg("Failed to cache group response")
	zlog.Warn().Msg("Group not found in cache after fetching")

	dec := json.NewDecoder(&buf)

	want := []struct{ level, msg, key, value string }{
		{levelDebug, "connecting", "websocket_type", "authed"},
		{"ERROR", "failed", keyError, "boom"},
		{levelDebug, "Authed websocket logged out", keyError, "boom"},
		{levelDebug, "Error handling request", keyError, "event handler returned non-success status"},
		{levelDebug, "Not clearing buffered event plaintext due to handler failure", "", ""},
		{levelDebug, "Failed to cache group response", keyError, errNoEndorsements.Error()},
		{"ERROR", "Failed to cache group response", keyError, errBoom.Error()},
		{levelDebug, "Group not found in cache after fetching", "", ""},
	}
	for _, line := range want {
		var got map[string]any

		err := dec.Decode(&got)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}

		if got["level"] != line.level || got["msg"] != line.msg || (line.key != "" && got[line.key] != line.value) {
			t.Errorf("got %v, want level=%s msg=%s %s=%s", got, line.level, line.msg, line.key, line.value)
		}
	}
}
