// Package view is the view pane loop (SPEC §4.1, §6.4): it keeps one pane
// attached to the session its view selects, switching by ending the
// `claude attach` client (SIGTERM detaches cleanly, M0 A8) and starting the
// next one. With nothing selected, or after the user detached, it shows a
// placeholder that lists sessions. Sessions are never affected by what the
// loop does.
package view

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// Client is the loop's connection to the daemon. Watch must be safe to
// call from another goroutine than the other methods, and must return
// when ctx is canceled.
type Client interface {
	Watch(ctx context.Context, req ipc.WatchRequest) (ipc.WatchResponse, error)
	Snapshot(ctx context.Context) (ipc.Snapshot, error)
	Select(ctx context.Context, viewID, sessionID string) error
	Close(ctx context.Context, viewID string) error
	Resume(ctx context.Context, sessionID string) (*core.Session, error)
}

// Loop runs one view.
type Loop struct {
	ViewID string
	Client Client
	// Attach returns the command that shows a session (claude attach <bgID>),
	// wired to the terminal.
	Attach func(bgID string) *exec.Cmd
	// Keys reads single keys from the terminal in the placeholder.
	Keys Keys
	Out  io.Writer
	// New starts a session from the placeholder ('n'); nil disables it.
	New func(ctx context.Context) error
	// HeartbeatWait is the longest watch (default 25 s, under the daemon's
	// 90 s view timeout).
	HeartbeatWait time.Duration
	// QuickExit: an attach that ends on its own with an error sooner than
	// this failed (default 1 s); otherwise the user detached.
	QuickExit time.Duration
	// Now is the clock (tests).
	Now func() time.Time
}

// Keys delivers keypresses. Poll waits up to timeout and returns 0 when
// no key was pressed; nothing reads the terminal between calls, so a
// program started from the placeholder gets all input.
type Keys interface {
	Start() error // e.g. terminal to non-canonical, no echo
	Stop()        // restore
	Poll(timeout time.Duration) (byte, error)
}

// ErrClosed means the view was closed (q, or by the daemon).
var ErrClosed = errors.New("view closed")

// Run runs until the view is closed or ctx ends.
func (l *Loop) Run(ctx context.Context) error {
	l.defaults()
	// The pane's foreground process group gets Ctrl+C/Ctrl+Z/Ctrl+\; they
	// are for the attach client, never for the loop.
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTSTP)
	defer signal.Stop(sigs)
	go func() {
		for range sigs { // drained: caught signals revert to default in the attach child
		}
	}()

	var lastErr string
	for {
		w, err := l.Client.Watch(ctx, ipc.WatchRequest{ViewID: l.ViewID})
		if err != nil {
			return err
		}
		if w.Gone {
			return ErrClosed
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		sel := w.View.Selected
		if sel == "" {
			if err := l.placeholder(ctx, w.Rev, lastErr); err != nil {
				return err
			}
			lastErr = ""
			continue
		}
		msg, err := l.show(ctx, sel)
		if err != nil {
			return err
		}
		if msg != "" {
			lastErr = msg
			if err := l.Client.Select(ctx, l.ViewID, ""); err != nil {
				return err
			}
		}
	}
}

func (l *Loop) defaults() {
	if l.HeartbeatWait == 0 {
		l.HeartbeatWait = 25 * time.Second
	}
	if l.QuickExit == 0 {
		l.QuickExit = time.Second
	}
	if l.Now == nil {
		l.Now = time.Now
	}
}

// show attaches session id until the view selects something else (nil,
// ""), the user detaches (Select "" and nil), or attaching fails (a message
// for the placeholder).
func (l *Loop) show(ctx context.Context, id string) (string, error) {
	resumed := false
	for {
		s, err := l.session(ctx, id)
		if err != nil {
			return err.Error(), nil
		}
		started := l.Now()
		switched, exitErr, err := l.attachUntil(ctx, s)
		if err != nil {
			return "", err
		}
		if switched {
			return "", nil
		}
		// The attach client ended by itself.
		if exitErr == nil || l.Now().Sub(started) >= l.QuickExit {
			return "", l.Client.Select(ctx, l.ViewID, "") // the user detached: placeholder
		}
		// It failed at once: a stopped session Claude couldn't restart on
		// attach is resumed explicitly, once (SPEC §6.2).
		if resumed || s.Status != core.StatusPaused {
			return fmt.Sprintf("Couldn't show %q (claude attach %s ended at once).", displayName(s), s.BgID), nil
		}
		resumed = true
		if _, err := l.Client.Resume(ctx, id); err != nil {
			return fmt.Sprintf("Couldn't resume %q: %v", displayName(s), err), nil
		}
	}
}

func (l *Loop) session(ctx context.Context, id string) (*core.Session, error) {
	snap, err := l.Client.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range snap.Sessions {
		if s.ID == id {
			if s.BgID == "" {
				return nil, fmt.Errorf("%q has no Claude background id yet", displayName(s))
			}
			return s, nil
		}
	}
	return nil, errors.New("that session no longer exists")
}

// attachUntil runs the attach client for s; switched is true when the
// loop ended it because the view now selects something else (or closed).
// exitErr is how the client ended when it ended by itself.
func (l *Loop) attachUntil(ctx context.Context, s *core.Session) (switched bool, exitErr, err error) {
	cmd := l.Attach(s.BgID)
	if err := cmd.Start(); err != nil {
		return false, nil, fmt.Errorf("start %s: %w", strings.Join(cmd.Args, " "), err)
	}
	exited := make(chan struct{})
	go func() {
		exitErr = cmd.Wait()
		close(exited)
	}()

	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	changed := make(chan error, 1)
	go func() { changed <- l.waitForChange(wctx, s.ID) }()

	select {
	case <-exited:
		return false, exitErr, nil
	case werr := <-changed:
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
		if werr != nil && !errors.Is(werr, ErrClosed) {
			return true, nil, werr
		}
		return true, nil, nil
	case <-ctx.Done():
		_ = cmd.Process.Signal(syscall.SIGTERM)
		<-exited
		return true, nil, ctx.Err()
	}
}

// waitForChange watches (as the view's heartbeat) until the view selects
// something other than id or is closed.
func (l *Loop) waitForChange(ctx context.Context, id string) error {
	var rev uint64
	first := true
	for {
		req := ipc.WatchRequest{ViewID: l.ViewID, Rev: rev}
		if !first {
			req.WaitMS = int(l.HeartbeatWait / time.Millisecond)
		}
		w, err := l.Client.Watch(ctx, req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if w.Gone {
			return ErrClosed
		}
		if w.View.Selected != id {
			return nil
		}
		rev, first = w.Rev, false
	}
}

// placeholder shows the session list until the view selects a session,
// or q closes the view.
func (l *Loop) placeholder(ctx context.Context, rev uint64, note string) error {
	if err := l.Keys.Start(); err != nil {
		return err
	}
	defer l.Keys.Stop()

	snap, err := l.Client.Snapshot(ctx)
	if err != nil {
		return err
	}
	rows := l.draw(snap, note)

	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		rev uint64
		err error
	}
	changed := make(chan result, 1)
	watchFrom := func(r uint64) {
		go func() {
			nr, err := l.waitForSelection(wctx, r)
			changed <- result{nr, err}
		}()
	}
	watchFrom(rev)
	for {
		select {
		case res := <-changed:
			if !errors.Is(res.err, errRedraw) {
				return res.err
			}
			// The list changed but nothing is selected: redraw.
			if snap, err = l.Client.Snapshot(ctx); err != nil {
				return err
			}
			rows = l.draw(snap, note)
			watchFrom(res.rev)
			continue
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		k, err := l.Keys.Poll(100 * time.Millisecond)
		if err != nil {
			return err
		}
		switch {
		case k == 'q':
			_ = l.Client.Close(ctx, l.ViewID)
			fmt.Fprint(l.Out, "\r\n")
			return ErrClosed
		case k == 'n' && l.New != nil:
			l.Keys.Stop()
			err := l.New(ctx)
			if startErr := l.Keys.Start(); startErr != nil {
				return startErr
			}
			if err != nil {
				note = err.Error()
				rows = l.draw(snap, note)
			}
		case k >= '1' && k <= '9':
			n := int(k - '0')
			if n <= len(rows) {
				if err := l.Client.Select(ctx, l.ViewID, rows[n-1].ID); err != nil {
					note = err.Error()
					rows = l.draw(snap, note)
				}
			}
		}
	}
}

var errRedraw = errors.New("redraw")

// waitForSelection returns once the view selects a session (nil), is gone
// (ErrClosed) or anything else changed (errRedraw with the new revision).
// Watches without a change are just heartbeats.
func (l *Loop) waitForSelection(ctx context.Context, rev uint64) (uint64, error) {
	for {
		w, err := l.Client.Watch(ctx, ipc.WatchRequest{ViewID: l.ViewID, Rev: rev, WaitMS: int(l.HeartbeatWait / time.Millisecond)})
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return rev, ctx.Err()
			}
			return rev, err
		case w.Gone:
			return rev, ErrClosed
		case w.View.Selected != "":
			return w.Rev, nil
		case w.Rev != rev:
			return w.Rev, errRedraw
		}
	}
}

// draw renders the placeholder and returns the numbered sessions.
func (l *Loop) draw(snap ipc.Snapshot, note string) []*core.Session {
	var b strings.Builder
	b.WriteString("\x1b[H\x1b[2J") // home, clear
	b.WriteString("No session shown in this pane.\r\n\r\n")
	var rows []*core.Session
	for _, r := range core.Layout(snap.Sessions, snap.Groups, snap.ViewState) {
		if r.Session == nil || r.Session.Kind != core.KindBackground || r.Session.BgID == "" {
			continue
		}
		rows = append(rows, r.Session)
	}
	if len(rows) == 0 {
		b.WriteString("  No background sessions yet.\r\n")
	}
	for i, s := range rows {
		label := " "
		if i < 9 {
			label = strconv.Itoa(i + 1)
		}
		unseen := ""
		if s.Unseen {
			unseen = " •"
		}
		fmt.Fprintf(&b, "  %s  %-8s %s%s\r\n", label, s.Status, displayName(s), unseen)
	}
	b.WriteString("\r\n")
	if note != "" {
		b.WriteString("  " + note + "\r\n\r\n")
	}
	keys := "1–9 show"
	if l.New != nil {
		keys += " · n new session here"
	}
	b.WriteString("  " + keys + " · q close this view\r\n")
	fmt.Fprint(l.Out, b.String())
	return rows
}

func displayName(s *core.Session) string {
	if s.Name != "" {
		return s.Name
	}
	return "session " + s.BgID
}
