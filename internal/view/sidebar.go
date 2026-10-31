package view

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// Panes is what the sidebar asks of the terminal adapter.
type Panes interface {
	FocusView(ctx context.Context) error
	ToggleSidebar(ctx context.Context) error
	FitWidth(ctx context.Context) error // re-apply the sidebar width (not while hidden)
	FocusPane(ctx context.Context, loc core.PaneLoc) error
}

// Sidebar is a view's session list (SPEC §8.1). This is the plain M2b
// version: numbers, statuses, the view's selection, a few keys. M2c
// replaces it with the full Bubble Tea sidebar.
type Sidebar struct {
	ViewID string
	Client Client
	Panes  Panes
	Keys   Keys
	Out    io.Writer
	// Size returns the pane's columns and rows.
	Size func() (int, int)
	// New starts a session in dir and shows it in this view.
	New  func(ctx context.Context, dir string) error
	Home string
	Now  func() time.Time
}

// Run renders until the view is gone or ctx ends.
func (s *Sidebar) Run(ctx context.Context) error {
	if s.Now == nil {
		s.Now = time.Now
	}
	if err := s.Keys.Start(); err != nil {
		return err
	}
	defer s.Keys.Stop()
	fmt.Fprint(s.Out, "\x1b[?25l") // hide the cursor
	defer fmt.Fprint(s.Out, "\x1b[?25h")

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	type result struct {
		w   ipc.WatchResponse
		err error
	}
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	changed := make(chan result, 1)
	watch := func(rev uint64, wait bool) {
		go func() {
			req := ipc.WatchRequest{ViewID: s.ViewID, Rev: rev, Passive: true}
			if wait {
				req.WaitMS = 25000
			}
			w, err := s.Client.Watch(wctx, req)
			changed <- result{w, err}
		}()
	}
	watch(0, false)

	var (
		snap ipc.Snapshot
		v    *core.View
		note string
		rev  uint64
	)
	tick := time.NewTicker(30 * time.Second) // refresh ages
	defer tick.Stop()
	redraw := func() {
		w, h := s.Size()
		fmt.Fprint(s.Out, Render(snap, v, w, h, note, s.Now()))
	}
	for {
		select {
		case r := <-changed:
			if r.err != nil {
				return r.err
			}
			if r.w.Gone {
				return ErrClosed
			}
			if r.w.Rev != rev || v == nil {
				var err error
				if snap, err = s.Client.Snapshot(ctx); err != nil {
					return err
				}
				v, rev = r.w.View, r.w.Rev
				redraw()
			}
			watch(rev, true)
			continue
		case <-winch:
			_ = s.Panes.FitWidth(ctx) // proportional splits drift (M0 K3)
			redraw()
			continue
		case <-tick.C:
			redraw()
			continue
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		k, err := s.Keys.Poll(100 * time.Millisecond)
		if err != nil {
			return err
		}
		if k == 0 || v == nil {
			continue
		}
		note = ""
		if err := s.key(ctx, k, snap, v); err != nil {
			note = err.Error()
			redraw()
		}
	}
}

func (s *Sidebar) key(ctx context.Context, k byte, snap ipc.Snapshot, v *core.View) error {
	switch {
	case k >= '1' && k <= '9':
		for _, r := range core.Layout(snap.Sessions, snap.Groups, snap.ViewState) {
			if r.Session == nil || r.Number != int(k-'0') {
				continue
			}
			if r.Session.Kind == core.KindExternal {
				if r.Session.External == nil {
					return errors.New("don't know which pane that session is in")
				}
				return s.Panes.FocusPane(ctx, *r.Session.External)
			}
			if err := s.Client.Select(ctx, s.ViewID, r.Session.ID); err != nil {
				return err
			}
			return s.Panes.FocusView(ctx)
		}
	case k == 'n':
		if s.New == nil {
			return nil
		}
		return s.New(ctx, NewFolder(snap, v, s.Home))
	case k == 'h':
		return s.Panes.ToggleSidebar(ctx)
	case k == 0x1b: // Esc (arrow keys etc. send sequences: drain them)
		if more, _ := s.Keys.Poll(20 * time.Millisecond); more != 0 {
			for more != 0 {
				more, _ = s.Keys.Poll(5 * time.Millisecond)
			}
			return nil
		}
		return s.Panes.FocusView(ctx)
	}
	return nil
}

// NewFolder is where `n` and shortcuts start a session (SPEC §6.1 step
// 2): the shown session's folder, else the most recently active
// session's, else home.
func NewFolder(snap ipc.Snapshot, v *core.View, home string) string {
	var recent *core.Session
	for _, s := range snap.Sessions {
		if s.ID == v.Selected && s.Cwd != "" {
			return s.Cwd
		}
		if s.Cwd != "" && (recent == nil || s.LastEventAt.After(recent.LastEventAt)) {
			recent = s
		}
	}
	if recent != nil {
		return recent.Cwd
	}
	return home
}

const (
	dim   = "\x1b[2m"
	bold  = "\x1b[1m"
	reset = "\x1b[0m"
)

var glyphs = map[core.Status]string{
	core.StatusStarting: dim + "…" + reset,
	core.StatusBusy:     "\x1b[34m⠹" + reset,
	core.StatusWaiting:  "\x1b[1;33m!" + reset,
	core.StatusDone:     "\x1b[32m✓" + reset,
	core.StatusError:    "\x1b[31m✗" + reset,
	core.StatusPaused:   "\x1b[90m‖" + reset,
	core.StatusArchived: dim + "-" + reset,
}

// Render draws the sidebar for view v (SPEC §8.1) in a w×h pane.
func Render(snap ipc.Snapshot, v *core.View, w, h int, note string, now time.Time) string {
	if w < 12 {
		w = 12
	}
	var lines []string
	active, paused := 0, 0
	for _, s := range snap.Sessions {
		switch s.Status {
		case core.StatusPaused:
			paused++
		case core.StatusArchived:
		default:
			active++
		}
	}
	lines = append(lines, bold+fit(fmt.Sprintf(" AGENTS  %d active · %d paused", active, paused), w)+reset,
		dim+" "+strings.Repeat("─", w-2)+reset)
	selected := ""
	if v != nil {
		selected = v.Selected
	}
	for _, r := range core.Layout(snap.Sessions, snap.Groups, snap.ViewState) {
		if r.Session == nil {
			arrow := "▾"
			if r.Collapsed {
				arrow = "▸"
			}
			lines = append(lines, bold+fit(fmt.Sprintf(" %s %s  %d", arrow, r.Group.Name, r.Count), w)+reset)
			continue
		}
		s := r.Session
		bar, num := " ", " "
		if s.ID == selected {
			bar = "\x1b[36m▌" + reset
		}
		if r.Number > 0 && r.Number < 10 {
			num = fmt.Sprint(r.Number)
		}
		suffix := ""
		if s.Kind == core.KindExternal {
			suffix += " ↗"
		}
		if s.Unseen {
			suffix += " •"
		}
		name := s.Name
		if name == "" {
			name = "session " + s.BgID
		}
		room := w - 5 - utf8.RuneCountInString(suffix)
		lines = append(lines, fmt.Sprintf("%s%s %s %s%s", bar, num, glyphs[s.Status], fit(name, room), suffix))
		detail := []string{base(s.Cwd), age(now, s)}
		if s.StatusDetail != "" {
			detail = append(detail, s.StatusDetail)
		}
		lines = append(lines, dim+"    "+fit(strings.Join(detail, " · "), w-4)+reset)
	}
	footer := []string{dim + " " + strings.Repeat("─", w-2) + reset}
	if note != "" {
		footer = append(footer, "\x1b[31m "+fit(note, w-1)+reset)
	}
	footer = append(footer, dim+fit(" 1–9 show · n new · h hide · esc", w)+reset)
	// Keep the footer at the bottom; drop rows that don't fit.
	if h > 0 && len(lines)+len(footer) > h {
		keep := h - len(footer)
		if keep < 0 {
			keep = 0
		}
		lines = lines[:min(keep, len(lines))]
	}
	for h > 0 && len(lines)+len(footer) < h {
		lines = append(lines, "")
	}
	var b strings.Builder
	b.WriteString("\x1b[H")
	for i, l := range append(lines, footer...) {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(l + "\x1b[K") // clear the rest of the line
	}
	b.WriteString("\x1b[J")
	return b.String()
}

// fit truncates s to n columns with an ellipsis (one column per rune).
func fit(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func base(p string) string {
	if i := strings.LastIndexByte(strings.TrimRight(p, "/"), '/'); i >= 0 {
		return strings.TrimRight(p, "/")[i+1:]
	}
	return p
}

func age(now time.Time, s *core.Session) string {
	t := s.LastEventAt
	if t.IsZero() {
		t = s.CreatedAt
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(0, int(d.Seconds())))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
