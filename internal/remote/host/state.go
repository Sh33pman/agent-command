package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// state is what Hosting keeps between runs that isn't config: the port the
// fallback picked, so clients find it there next time, and pending requests.
type state struct {
	Port     *rememberedPort      `json:"port,omitempty"`
	Requests []*ipc.RemoteRequest `json:"requests,omitempty"`
}

// rememberedPort is the port the fallback picked for a listen setting and
// port.
type rememberedPort struct {
	Listen     string `json:"listen"`
	Configured int    `json:"configured"`
	Port       int    `json:"port"`
}

func loadState(path string) (state, error) {
	var st state
	if path == "" {
		return st, nil
	}
	b, err := os.ReadFile(path) // #nosec G304 -- acmd's own state file
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return state{}, fmt.Errorf("%s: %w", path, err)
	}
	return st, nil
}

// saveLocked writes the state (h.mu is held). A failure is logged: the state
// in memory is still right.
func (h *Host) saveLocked() {
	if h.o.StateFile == "" {
		return
	}
	if err := writeState(h.o.StateFile, h.st); err != nil {
		h.o.Log.Printf("remote: saving %s: %v", h.o.StateFile, err)
	}
}

func writeState(path string, st state) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
