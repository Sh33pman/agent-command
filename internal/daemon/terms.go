package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/term"
)

// terms tracks the sessions whose terminal server runs (SPEC §8.7). The
// servers outlive the daemon, so a new daemon finds them again (rescan);
// whether a terminal is shown isn't kept across that.
type terms struct {
	mu sync.Mutex
	m  map[string]ipc.Terminal
}

// set records whether session id's terminal runs. A terminal that just
// started is shown (the user asked for it); one already known keeps its
// state.
func (t *terms) set(id string, running bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !running {
		delete(t.m, id)
		return
	}
	if t.m == nil {
		t.m = map[string]ipc.Terminal{}
	}
	if _, ok := t.m[id]; !ok {
		t.m[id] = ipc.Terminal{Shown: true}
	}
}

// show sets whether session id's terminal pane is shown; false when it
// has no terminal.
func (t *terms) show(id string, shown bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	cur, ok := t.m[id]
	if !ok {
		return false
	}
	cur.Shown = shown
	t.m[id] = cur
	return true
}

func (t *terms) all() map[string]ipc.Terminal {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.m) == 0 {
		return nil
	}
	out := make(map[string]ipc.Terminal, len(t.m))
	for k, v := range t.m {
		out[k] = v
	}
	return out
}

// rescan finds terminal servers that are still running (after a daemon
// restart); they come back hidden.
func (t *terms) rescan(p paths.Paths) {
	files, _ := filepath.Glob(filepath.Join(p.RuntimeDir, "t-*.id"))
	for _, f := range files {
		b, err := os.ReadFile(filepath.Clean(f))
		if err != nil {
			continue
		}
		id := strings.TrimSpace(string(b))
		if term.Running(p.TermSocket(id)) {
			t.set(id, true)
			t.show(id, false)
		} else {
			_ = os.Remove(f)
		}
	}
}
