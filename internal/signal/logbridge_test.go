package signal_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

var errBoom = errors.New("boom")

func TestZerologBridge(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	zlog := signal.NewZerologBridge(logger)

	zlog.Info().Str("websocket_type", "authed").Msg("connecting")
	zlog.Error().Err(errBoom).Msg("failed")

	dec := json.NewDecoder(&buf)

	want := []struct{ level, msg, key, value string }{
		{"DEBUG", "connecting", "websocket_type", "authed"},
		{"ERROR", "failed", "error", "boom"},
	}
	for _, line := range want {
		var got map[string]any

		err := dec.Decode(&got)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}

		if got["level"] != line.level || got["msg"] != line.msg || got[line.key] != line.value {
			t.Errorf("got %v, want level=%s msg=%s %s=%s", got, line.level, line.msg, line.key, line.value)
		}
	}
}
