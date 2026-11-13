//go:build !darwin

package editor

import "github.com/brettmostert/agent-command/internal/setup"

func openBundle(setup.EditorPreset, string) ([]string, bool) { return nil, false }
