package daemon

import (
	"context"
	"regexp"
	"strings"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// A session's screen as text, for clients with no terminal emulator: a
// phone, a terminal UI, the CLI.

// escapes are terminal control sequences, which a host without the exact
// screen keeps in its raw output.
var escapes = regexp.MustCompile(`\x1b(\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)|[@-Z\\-_])`)

func (d *Daemon) screen(ctx context.Context, r ipc.ScreenRequest) (ipc.Screen, error) {
	if r.Lines < 0 {
		return ipc.Screen{}, invalid("lines is how many to send, not %d", r.Lines)
	}
	s, err := d.sessionByID(ctx, r.ID)
	if err != nil {
		return ipc.Screen{}, err
	}
	if s.OnHost() {
		return ipc.Screen{}, &codedError{code: ipc.CodeNotSupported,
			err: invalid("%s runs on %s, which can't send its screen yet", nameOf(s), s.Host)}
	}
	text, ok := d.cfg.HostScreen(s.ID)
	if !ok {
		return ipc.Screen{}, &codedError{code: ipc.CodeNotRunning, err: invalid("%s's terminal isn't running", nameOf(s))}
	}
	text = strings.ReplaceAll(escapes.ReplaceAllString(text, ""), "\r", "")
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	out := ipc.Screen{ID: s.ID, Total: len(lines)}
	if r.Lines > 0 && len(lines) > r.Lines {
		lines = lines[len(lines)-r.Lines:]
	}
	out.Lines, out.Text = len(lines), strings.Join(lines, "\n")
	return out, nil
}
