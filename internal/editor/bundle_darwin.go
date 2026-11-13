//go:build darwin

package editor

import "github.com/brettmostert/agent-command/internal/setup"

func openBundle(preset setup.EditorPreset, dir string) ([]string, bool) {
	if len(preset.Bundles) > 0 {
		return []string{"/usr/bin/open", "-b", preset.Bundles[0], dir}, true
	}
	return nil, false
}
