package daemon

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/brettmostert/agent-command/internal/config"
)

// writeSetting edits config.toml as a person would: the value written into
// the file, whether config.set would take it or not.
func writeSetting(path, section, key string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	text, err := config.Set(string(b), section, key, v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o600)
}
