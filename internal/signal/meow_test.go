//go:build cgo

package signal_test

import (
	"errors"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestOpenWithoutAccount(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	client, err := signal.Open(ctx, signal.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	_, err = client.Account(ctx)
	if !errors.Is(err, signal.ErrNotLinked) {
		t.Errorf("Account: got %v, want ErrNotLinked", err)
	}

	err = client.Connect(ctx)
	if !errors.Is(err, signal.ErrNotLinked) {
		t.Errorf("Connect: got %v, want ErrNotLinked", err)
	}

	_, err = client.Send(ctx, signal.SendRequest{})
	if !errors.Is(err, signal.ErrNotConnected) {
		t.Errorf("Send: got %v, want ErrNotConnected", err)
	}

	err = client.Close()
	if err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, ok := <-client.Events(); ok {
		t.Error("Events is not closed after Close")
	}

	err = client.Close()
	if err != nil {
		t.Errorf("second close: %v", err)
	}
}

func TestOpenUnknownAccount(t *testing.T) {
	t.Parallel()

	client, err := signal.Open(t.Context(), signal.Options{DataDir: t.TempDir(), Account: "+15550199"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer client.Close()

	_, err = client.Account(t.Context())
	if !errors.Is(err, signal.ErrAccountNotFound) {
		t.Errorf("got %v, want ErrAccountNotFound", err)
	}
}
