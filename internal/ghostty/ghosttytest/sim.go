// Package ghosttytest simulates enough of Ghostty's AppleScript for tests
// of acmd's Ghostty adapter and the commands built on it.
package ghosttytest

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/brettmostert/agent-command/internal/ghostty"
)

// Term is a simulated Ghostty terminal (pane).
type Term struct {
	ID, TTY, Command, Cwd string
	Env                   []string
	Tab                   int
}

// Sim is a Ghostty with terminals; it records operations.
type Sim struct {
	mu         sync.Mutex
	Terms      []*Term
	Focused    string
	Zoomed     map[string]bool
	Calls      []string
	Denied     bool // macOS Automation permission denied
	Background bool // Ghostty isn't the frontmost app
	next       int
}

// New returns a Ghostty with one terminal, "T1" on /dev/ttys001.
func New() *Sim {
	return &Sim{Terms: []*Term{{ID: "T1", TTY: "/dev/ttys001", Tab: 1}}, Focused: "T1", Zoomed: map[string]bool{}}
}

// App returns an adapter talking to the simulation.
func (s *Sim) App() *ghostty.App { return &ghostty.App{Run: s.Run} }

func (s *Sim) find(id string) *Term {
	for _, t := range s.Terms {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func (s *Sim) record(format string, a ...any) { s.Calls = append(s.Calls, fmt.Sprintf(format, a...)) }

// Run plays one osascript call, recognized by its script.
func (s *Sim) Run(_ context.Context, script string, args ...string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Denied {
		return "", ghostty.ErrNotPermitted
	}
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	switch {
	case strings.Contains(script, "(tty of term) is item 1"):
		for _, t := range s.Terms {
			if t.TTY == arg(0) {
				return t.ID, nil
			}
		}
		return "not found", nil
	case strings.Contains(script, "split view direction left"):
		v := s.find(arg(0))
		if v == nil {
			return "not found", nil
		}
		s.next++
		t := &Term{ID: fmt.Sprintf("S%d", s.next), TTY: fmt.Sprintf("/dev/ttys1%02d", s.next), Command: arg(1), Cwd: arg(2), Tab: v.Tab}
		if arg(3) != "" {
			t.Env = strings.Split(arg(3), "\n")
		}
		s.Terms = append(s.Terms, t)
		s.Focused = v.ID // the script refocuses the view
		s.record("split %s → %s", v.ID, t.ID)
		return t.ID, nil
	case strings.Contains(script, "perform action (item 2 of argv)"):
		if s.find(arg(0)) == nil {
			return "not found", nil
		}
		if arg(1) == "toggle_split_zoom" {
			s.Zoomed[arg(0)] = !s.Zoomed[arg(0)]
		}
		s.record("action %s %s", arg(0), arg(1))
		return "ok", nil
	case strings.Contains(script, "to focus term"):
		if s.find(arg(0)) == nil {
			return "not found", nil
		}
		s.Focused = arg(0)
		return "ok", nil
	case strings.Contains(script, "to close term"):
		for i, t := range s.Terms {
			if t.ID == arg(0) {
				s.Terms = append(s.Terms[:i], s.Terms[i+1:]...)
				s.record("close %s", arg(0))
				return "ok", nil
			}
		}
		return "not found", nil
	case strings.Contains(script, `return "no"`):
		if s.find(arg(0)) == nil {
			return "no", nil
		}
		return "yes", nil
	case strings.Contains(script, "new tab in"):
		near := s.find(arg(0))
		if near == nil {
			return "not found", nil
		}
		s.next++
		s.Terms = append(s.Terms, &Term{ID: fmt.Sprintf("N%d", s.next), Command: arg(1), Cwd: arg(2), Tab: 100 + s.next})
		s.record("tab %s %s", arg(2), arg(1))
		return "ok", nil
	case strings.Contains(script, "input text (item 2 of argv)"):
		if s.find(arg(0)) == nil {
			return "not found", nil
		}
		s.record("type %s %s", arg(0), arg(1))
		return "ok", nil
	case strings.Contains(script, "new window with configuration"):
		s.next++
		s.Terms = append(s.Terms, &Term{ID: fmt.Sprintf("W%d", s.next), Command: arg(0), Cwd: arg(1), Tab: 200 + s.next})
		s.record("window %s %s", arg(1), arg(0))
		return "ok", nil
	case strings.Contains(script, "reload_config"):
		s.record("reload")
		return "ok", nil
	case strings.Contains(script, "if frontmost and"):
		if s.Background {
			return "", nil
		}
		return s.Focused, nil
	case strings.Contains(script, "get version"):
		return "running", nil
	}
	return "", fmt.Errorf("ghosttytest: unknown script:\n%s", script)
}
