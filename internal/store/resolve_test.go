package store_test

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/cwbudde/go-signal/internal/store"
)

// The tests below set XDG_DATA_HOME and HOME, so they can't run in parallel.

func TestDefaultDirXDG(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)

	if got, want := store.DefaultDir(), filepath.Join(xdg, "go-signal"); got != want {
		t.Errorf("DefaultDir() = %q, want %q", got, want)
	}
}

func TestDefaultDirHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", home)

	if got, want := store.DefaultDir(), filepath.Join(home, ".local", "share", "go-signal"); got != want {
		t.Errorf("DefaultDir() = %q, want %q", got, want)
	}
}

func TestOpenDirDefault(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)

	dir, err := store.OpenDir("", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	if want := filepath.Join(xdg, "go-signal"); dir.Path() != want {
		t.Errorf("Path() = %q, want %q", dir.Path(), want)
	}
}

func TestOpenDirTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := store.OpenDir("~/signal-data", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	if want := filepath.Join(home, "signal-data"); dir.Path() != want {
		t.Errorf("Path() = %q, want %q", dir.Path(), want)
	}

	if want := filepath.Join(home, "signal-data", "some-aci"); dir.AccountDir("some-aci") != want {
		t.Errorf("AccountDir() = %q, want %q", dir.AccountDir("some-aci"), want)
	}
}

func TestOpenDirRelative(t *testing.T) { //nolint:paralleltest // changes the working directory
	wd := t.TempDir()
	t.Chdir(wd)

	dir, err := store.OpenDir("data", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	// Symlinked temp dirs (macOS) resolve differently, so compare the last element only.
	if !filepath.IsAbs(dir.Path()) || filepath.Base(dir.Path()) != "data" {
		t.Errorf("Path() = %q, want an absolute path ending in data", dir.Path())
	}
}
