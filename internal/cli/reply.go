package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/procenv"
)

// Answering from the reply popup (SPEC §8.8): Claude has no command that
// sends a message to a running background session, so acmd opens it in a
// hidden terminal (claude attach in a pseudo-terminal), types the answer
// and leaves with Ctrl+Z. Checked 2026-11-01 against Claude Code 2.1.316:
// a question's option is picked by its number key; text is pasted
// (bracketed paste keeps its lines) and sent with Enter.

// replyInput is what acmd types for an answer: text (pasted, then Enter)
// or an option (its number key, 0-based).
type replyInput struct {
	Text   string
	Option int // -1: none
}

func (r replyInput) keys() []byte {
	if r.Option >= 0 {
		return []byte(fmt.Sprint(r.Option + 1))
	}
	return []byte("\x1b[200~" + r.Text + "\x1b[201~")
}

// attachTimings: how long to wait for attach to draw, and for Claude to
// take the answer (tests shorten them).
var (
	replySettle = 600 * time.Millisecond // output quiet this long: drawn
	replyDraw   = 8 * time.Second        // at most, before typing anyway
	replyTake   = 10 * time.Second       // for the session to move on
)

// deliverReply answers session s with in.
func deliverReply(ctx context.Context, call caller, s *core.Session, in replyInput) error {
	if s.Kind == core.KindExternal {
		if s.External == nil {
			return errors.New("don't know which pane that session is in: answer it there")
		}
		if in.Option >= 0 {
			if s.External.Terminal != "kitty" {
				return errors.New("answer it in its own pane")
			}
			return kittyAt(s.External.KittySocket).SendText(ctx, s.External.KittyWindow, string(in.keys()))
		}
		return typeInto(ctx, *s.External, in.Text)
	}
	if s.Stopped() {
		var r core.Session
		if err := call(ipc.OpResume, ipc.Mutation{ID: s.ID}, &r); err != nil {
			return err
		}
		s = &r
	}
	if s.BgID == "" {
		return errors.New("that session has no Claude background id")
	}
	p, err := paths.Default()
	if err != nil {
		return err
	}
	claude, env, err := claudeCommand(p)
	if err != nil {
		return err
	}
	from := s.Status
	return typeViaAttach(ctx, claude, env, s, in.keys(), in.Option < 0, func() bool {
		var snap ipc.Snapshot
		if call(ipc.OpSnapshot, nil, &snap) != nil {
			return false
		}
		for _, x := range snap.Sessions {
			if x.ID == s.ID {
				return x.Status != from || x.LastEventAt.After(s.LastEventAt)
			}
		}
		return false
	})
}

// typeViaAttach opens s with claude attach in a pseudo-terminal, waits
// until it has drawn, types keys (then Enter when enter), waits until
// moved reports the session reacted, and leaves with Ctrl+Z.
func typeViaAttach(ctx context.Context, claude string, env []string, s *core.Session, keys []byte, enter bool, moved func() bool) error {
	cmd := exec.CommandContext(ctx, claude, "attach", s.BgID) // #nosec G204 -- resolved claude binary, short id from Claude
	cmd.Env = append(procenv.Scrub(env), "TERM=xterm-256color")
	if st, err := os.Stat(s.Cwd); err == nil && st.IsDir() {
		cmd.Dir = s.Cwd
	}
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		return err
	}
	defer ptmx.Close()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer func() {
		select {
		case err := <-exited:
			_ = err // it ended (Ctrl+Z): nothing to stop
		default:
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case err := <-exited:
				_ = err // stopped
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
			}
		}
	}()

	// Read (and drop) its output, noting when it last drew.
	var mu sync.Mutex
	last := time.Now()
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				mu.Lock()
				last = time.Now()
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	quiet := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return time.Since(last) >= replySettle
	}
	for deadline := time.Now().Add(replyDraw); !quiet() && time.Now().Before(deadline); {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-exited:
			return fmt.Errorf("claude attach %s ended: %w", s.BgID, err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	if _, err := ptmx.Write(keys); err != nil {
		return err
	}
	if enter {
		time.Sleep(300 * time.Millisecond) // let it take the paste
		if _, err := ptmx.Write([]byte("\r")); err != nil {
			return err
		}
	}
	reacted := false
	for deadline := time.Now().Add(replyTake); time.Now().Before(deadline); {
		if moved() {
			reacted = true
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	_, _ = ptmx.Write([]byte{0x1a}) // Ctrl+Z: leave attach; the session keeps running
	select {
	case err := <-exited:
		_ = err       // it left
		exited <- nil // for the deferred check
	case <-time.After(3 * time.Second):
	}
	if !reacted {
		return errors.New("Claude didn't take the answer yet (it may be busy: it queues it); check the session") //nolint:staticcheck // ST1005: product name
	}
	return nil
}

// optionIndex is the 0-based option of an item's answer label.
func optionIndex(it *core.InboxItem, n int) (replyInput, error) {
	if n < 0 || n >= len(it.Options) {
		return replyInput{}, fmt.Errorf("no option %d", n+1)
	}
	return replyInput{Option: n}, nil
}

// replyText is a free-text answer.
func replyText(text string) replyInput {
	return replyInput{Text: strings.TrimSpace(text), Option: -1}
}
