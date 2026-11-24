//go:build linux

package linuxui

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/brettmostert/agent-command/internal/paths"
)

const (
	defaultWindowWidth  = 1200
	defaultWindowHeight = 760
)

// WindowState stores the last window dimensions and maximized state.
type WindowState struct {
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximized bool `json:"maximized,omitempty"`
}

// DefaultWindowState returns the initial window dimensions.
func DefaultWindowState() WindowState {
	return WindowState{Width: defaultWindowWidth, Height: defaultWindowHeight}
}

// WindowStatePath returns ~/.config/agent-command/linux-window.json.
func WindowStatePath() (string, error) {
	p, err := paths.Default()
	if err != nil {
		return "", err
	}
	return filepath.Join(p.ConfigDir, "linux-window.json"), nil
}

// LoadWindowState reads WindowState from path, falling back to DefaultWindowState.
func LoadWindowState(path string) WindowState {
	ws := DefaultWindowState()
	if path == "" {
		return ws
	}
	b, err := os.ReadFile(path) // #nosec G304 -- user window state path
	if err != nil {
		return ws
	}
	var loaded WindowState
	if err := json.Unmarshal(b, &loaded); err != nil {
		return ws
	}
	if loaded.Width > 0 {
		ws.Width = loaded.Width
	}
	if loaded.Height > 0 {
		ws.Height = loaded.Height
	}
	ws.Maximized = loaded.Maximized
	return ws
}

// SaveWindowState writes ws to path.
func SaveWindowState(path string, ws WindowState) error {
	if path == "" {
		return nil
	}
	if ws.Width <= 0 {
		ws.Width = defaultWindowWidth
	}
	if ws.Height <= 0 {
		ws.Height = defaultWindowHeight
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(ws, "", "  ")
	if err != nil {
		return err
	}
	err = os.WriteFile(path, append(b, '\n'), 0o600)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
