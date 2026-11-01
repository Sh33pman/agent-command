package view

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// Leaving a session from inside `claude attach` (← on an empty prompt,
// /exit) doesn't end attach: it switches to Claude's own agent view in the
// same process (SPEC §6.4). acmd runs attach through a relay that watches
// for that switch, which Claude marks by setting the terminal title to
// "… claude agents", and tells which of the two it was from what was typed.

// agentViewTitle is the end of the window title Claude sets in its agent
// view (OSC 0, checked against Claude Code 2.1).
var agentViewTitle = []byte("claude agents")

// relayed is how a relayed attach ended.
type relayed struct {
	agentView bool // Claude switched to its agent view
	exitTyped bool // ...after the user typed /exit (or /quit)
}

// relay runs cmd in a pseudo-terminal between the user's terminal (in,
// out) and cmd until cmd exits or it reaches Claude's agent view; stop
// ends it early. It returns how it ended and cmd's wait error (nil when
// relay killed it).
func relay(cmd *exec.Cmd, in *os.File, out io.Writer, stop <-chan struct{}) (relayed, error) {
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return relayed{}, err
	}
	defer ptmx.Close()
	_ = pty.InheritSize(in, ptmx)
	if state, err := term.MakeRaw(in.Fd()); err == nil {
		defer func() { _ = term.Restore(in.Fd(), state) }()
	}
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	var mu sync.Mutex
	typed := typing{}
	agentView := make(chan struct{})
	var once sync.Once
	outDone := make(chan struct{})
	go func() { // cmd's output: pass it on, watch the title
		defer close(outDone)
		buf := make([]byte, 32<<10)
		tail := make([]byte, 0, 512)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				_, _ = out.Write(buf[:n])
				tail = append(tail, buf[:n]...)
				if hasAgentTitle(tail) {
					once.Do(func() { close(agentView) })
				}
				if len(tail) > 256 {
					tail = append(tail[:0], tail[len(tail)-256:]...)
				}
			}
			if err != nil {
				return
			}
		}
	}()

	done := make(chan struct{})
	defer close(done)
	go func() { // the user's keys: to cmd, noting what's typed
		fds := []unix.PollFd{{Fd: int32(in.Fd()), Events: unix.POLLIN}} // #nosec G115 -- small fd
		buf := make([]byte, 4096)
		for {
			select {
			case <-done:
				return
			default:
			}
			// Poll, so nothing reads the terminal once the relay ends (the
			// placeholder reads keys next).
			n, err := unix.Poll(fds, 100)
			if errors.Is(err, unix.EINTR) || n == 0 {
				continue
			}
			if err != nil {
				return
			}
			m, err := in.Read(buf)
			if m > 0 {
				mu.Lock()
				typed.add(buf[:m])
				mu.Unlock()
				_, _ = ptmx.Write(buf[:m])
			}
			if err != nil {
				return
			}
		}
	}()

	end := func() error {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-exited:
			_ = err // relay ended it: not an attach failure
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			if err := <-exited; err != nil {
				return nil // killed: expected
			}
		}
		return nil
	}
	for {
		select {
		case <-winch:
			_ = pty.InheritSize(in, ptmx)
		case err := <-exited:
			<-outDone
			return relayed{}, err
		case <-agentView:
			mu.Lock()
			r := relayed{agentView: true, exitTyped: typed.exitRecently(time.Now())}
			mu.Unlock()
			return r, end()
		case <-stop:
			return relayed{}, end()
		}
	}
}

func hasAgentTitle(b []byte) bool {
	for {
		i := bytes.Index(b, []byte("\x1b]0;"))
		if i < 0 {
			return false
		}
		b = b[i+4:]
		j := bytes.IndexAny(b, "\x07\x1b")
		if j < 0 {
			return false // the title isn't complete yet
		}
		if bytes.HasSuffix(b[:j], agentViewTitle) {
			return true
		}
	}
}

// typing follows what the user types, to tell /exit from ←: the last
// line submitted with Enter that was a slash command, and when.
type typing struct {
	line     []byte
	esc      bool
	lastCmd  string
	lastSent time.Time
}

func (t *typing) add(b []byte) {
	for _, c := range b {
		switch {
		case t.esc:
			// Skip an escape sequence (arrows, …): it ends with a letter
			// or ~.
			if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '~' {
				t.esc = false
			}
		case c == 0x1b:
			t.esc = true
		case c == '\r' || c == '\n':
			if s := strings.TrimSpace(string(t.line)); strings.HasPrefix(s, "/") {
				t.lastCmd = s
			}
			t.lastSent = time.Now()
			t.line = t.line[:0]
		case c == 0x7f || c == 0x08:
			if len(t.line) > 0 {
				t.line = t.line[:len(t.line)-1]
			}
		case c == 0x15: // Ctrl+U
			t.line = t.line[:0]
		case c >= 0x20:
			t.line = append(t.line, c)
		}
	}
}

// exitRecently: the last slash command (completed by Claude from what was
// typed, e.g. /ex) is /exit or /quit, and was sent in the last 10 s.
func (t *typing) exitRecently(now time.Time) bool {
	f := strings.Fields(t.lastCmd)
	if len(f) != 1 || len(f[0]) < 3 || now.Sub(t.lastSent) > 10*time.Second { // /exit takes nothing after it
		return false
	}
	c := f[0]
	// c is a prefix of the command (Claude completes /ex to /exit).
	for _, full := range []string{"/exit", "/quit"} {
		if len(c) <= len(full) && full[:len(c)] == c {
			return true
		}
	}
	return false
}
