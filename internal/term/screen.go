package term

import "github.com/brettmostert/agent-command/internal/vt"

// scrollback limits the memory a host's screen uses for history. It counts
// terminal cells rather than text: about 700 bytes per row at 80 columns, so
// this is around 95,000 rows.
const scrollback = 64 << 20

// screen is what a server remembers of the terminal, for clients that attach
// later. Calls are serialized by the server.
type screen interface {
	// Write feeds the program's output.
	Write(p []byte)
	// Replay is what a client gets first, to show the current state.
	Replay() []byte
	// Resize follows the PTY's size.
	Resize(rows, cols int)
	// Text is the current screen and history as plain text.
	Text() string
	// Exact reports whether Replay restores the full state. If not, a
	// program has to redraw for a client to see it correctly.
	Exact() bool
	Close()
}

// newScreen uses libghostty-vt when this binary has it, and otherwise replays
// recent raw output. reply gets the answers to the program's terminal queries.
func newScreen(rows, cols, keep int, reply func([]byte)) screen {
	if vt.Available {
		if t, err := vt.New(cols, rows, scrollback, reply); err == nil {
			return &vtScreen{t: t}
		}
	}
	return &ringScreen{kept: ring{n: keep}}
}

// vtScreen restores the terminal's exact state.
type vtScreen struct {
	t *vt.Terminal
}

func (s *vtScreen) Write(p []byte) { s.t.Write(p) }

func (s *vtScreen) Replay() []byte {
	reset := []byte("\x1bc")
	snap, err := s.t.Snapshot()
	if err != nil {
		return reset
	}
	return append(reset, snap...)
}

func (s *vtScreen) Resize(rows, cols int) { _ = s.t.Resize(cols, rows) }

func (s *vtScreen) Text() string {
	text, err := s.t.Text()
	if err != nil {
		return ""
	}
	return text
}

func (s *vtScreen) Exact() bool { return true }

func (s *vtScreen) Close() { s.t.Close() }

// ringScreen keeps recent raw output.
type ringScreen struct {
	kept ring
}

func (s *ringScreen) Write(p []byte) { s.kept.Write(p) }

func (s *ringScreen) Replay() []byte {
	return append([]byte("\x1b[H\x1b[2J"), s.kept.Bytes()...)
}

func (s *ringScreen) Resize(_, _ int) {}

func (s *ringScreen) Text() string { return string(s.kept.Bytes()) }

func (s *ringScreen) Exact() bool { return false }

func (s *ringScreen) Close() {}
