//go:build linux

package linuxui

import (
	"path/filepath"
	"testing"
)

func TestLoadAndSaveWindowState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "linux-window.json")

	// Missing file returns defaults.
	ws := LoadWindowState(path)
	if ws.Width != 1200 || ws.Height != 760 || ws.Maximized {
		t.Fatalf("unexpected default window state: %+v", ws)
	}

	want := WindowState{Width: 1440, Height: 900, Maximized: true}
	if err := SaveWindowState(path, want); err != nil {
		t.Fatalf("SaveWindowState: %v", err)
	}
	got := LoadWindowState(path)
	if got != want {
		t.Fatalf("LoadWindowState = %+v, want %+v", got, want)
	}

	if p, err := WindowStatePath(); err != nil || p == "" {
		t.Fatalf("WindowStatePath = %q, %v", p, err)
	}
}
