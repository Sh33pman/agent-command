// Package header is the bar above a view pane (SPEC §8.4): one line with
// the shown session's status, name, description and folder. It spans only
// the view pane, not the sidebar.
package header

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/tui"
)

// ErrClosed means the header's view was closed.
var ErrClosed = errors.New("view closed")

var (
	dim  = lipgloss.NewStyle().Faint(true)
	bold = lipgloss.NewStyle().Bold(true)
)

// Actions is what the header needs from the daemon and the terminal.
type Actions interface {
	// Watch waits for a change after rev (passive: not a heartbeat).
	Watch(ctx context.Context, rev uint64, wait bool) (ipc.WatchResponse, error)
	Snapshot(ctx context.Context) (ipc.Snapshot, error)
	// FitHeight resizes the header's pane to lines.
	FitHeight(ctx context.Context, lines int) error
	FocusView(ctx context.Context) error
}

// Model is the header's Bubble Tea model.
type Model struct {
	ctx  context.Context
	act  Actions
	view string
	home string

	shown  *core.Session
	rev    uint64
	loaded bool
	w, h   int
	spin   int
	err    error
}

// New returns the header for view viewID.
func New(ctx context.Context, act Actions, viewID, home string) *Model {
	return &Model{ctx: ctx, act: act, view: viewID, home: home, w: 80, h: 1}
}

// Err is why the program ended (ErrClosed when the view closed).
func (m *Model) Err() error { return m.err }

type (
	stateMsg struct {
		resp ipc.WatchResponse
		snap ipc.Snapshot
	}
	goneMsg  struct{}
	fatalMsg struct{ err error }
	tickMsg  struct{}
)

func tick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

// Init starts watching the view.
func (m *Model) Init() tea.Cmd { return tea.Batch(m.watch(0, false), tick()) }

func (m *Model) watch(rev uint64, wait bool) tea.Cmd {
	return func() tea.Msg {
		for {
			w, err := m.act.Watch(m.ctx, rev, wait)
			if err != nil {
				return fatalMsg{err}
			}
			if w.Gone {
				return goneMsg{}
			}
			if w.Rev == rev && wait {
				continue // heartbeat timeout, nothing changed
			}
			snap, err := m.act.Snapshot(m.ctx)
			if err != nil {
				return fatalMsg{err}
			}
			return stateMsg{resp: w, snap: snap}
		}
	}
}

// Update handles messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case stateMsg:
		m.rev, m.loaded, m.shown = msg.resp.Rev, true, nil
		if v := msg.resp.View; v != nil {
			for _, s := range msg.snap.Sessions {
				if s.ID == v.Selected {
					m.shown = s
				}
			}
		}
		return m, m.watch(m.rev, true)
	case goneMsg:
		m.err = ErrClosed
		return m, tea.Quit
	case fatalMsg:
		m.err = msg.err
		return m, tea.Quit
	case tickMsg:
		m.spin++
		return m, tick()
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		if m.h != 1 {
			return m, func() tea.Msg { _ = m.act.FitHeight(m.ctx, 1); return nil }
		}
		return m, nil
	case tea.KeyPressMsg, tea.MouseClickMsg:
		return m, func() tea.Msg { _ = m.act.FocusView(m.ctx); return nil }
	}
	return m, nil
}

// View draws the header.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen, v.MouseMode = true, tea.MouseModeCellMotion
	return v
}

func (m *Model) render() string {
	out := []string{m.line()}
	for len(out) < max(m.h, 1) {
		out = append(out, strings.Repeat(" ", max(0, m.w)))
	}
	return strings.Join(out, "\n")
}

// line is "<glyph> <status> · <name> · <description>   <claude id>  <folder>",
// exactly m.w wide. When it doesn't fit, the description gives way
// first, then the Claude session id, then the folder.
func (m *Model) line() string {
	if m.w <= 0 {
		return ""
	}
	s := m.shown
	if s == nil {
		text := "No session shown"
		if !m.loaded {
			text = "acmd"
		}
		return pad(dim.Render(" "+text), m.w)
	}
	status := tui.StatusWord(s.Status)
	if s.StatusDetail != "" && s.Status != core.StatusDone {
		status += ": " + s.StatusDetail
	}
	left := " " + tui.Glyph(s.Status, m.spin) + " " + dim.Render(status) + dim.Render(" · ") + bold.Render(displayName(s))
	right := []string{}
	if s.ClaudeSessionID != "" {
		right = append(right, dim.Render(s.ClaudeSessionID))
	}
	right = append(right, dim.Render(tui.Tilde(s.Cwd, m.home)))
	desc := ""
	if s.Description != "" {
		desc = dim.Render(" · ") + s.Description
	}
	tail := func() string { return strings.Join(right, "  ") + " " }
	room := func() int { return m.w - lipgloss.Width(left) - lipgloss.Width(tail()) - 1 }
	for room() < 0 && len(right) > 0 {
		right = right[1:] // the id goes before the folder
		desc = ""
	}
	switch r := room(); {
	case r >= lipgloss.Width(desc):
	case r > 4:
		desc = ansi.Truncate(desc, r, "…")
	default:
		desc = ""
	}
	t := ""
	if len(right) > 0 {
		t = tail()
	}
	gap := m.w - lipgloss.Width(left) - lipgloss.Width(desc) - lipgloss.Width(t)
	return pad(left+desc+strings.Repeat(" ", max(1, gap))+t, m.w)
}

func displayName(s *core.Session) string {
	if s.Name != "" {
		return s.Name
	}
	if s.BgID != "" {
		return "session " + s.BgID
	}
	return "session"
}

func pad(s string, n int) string {
	s = ansi.Truncate(s, n, "")
	if w := lipgloss.Width(s); w < n {
		s += strings.Repeat(" ", n-w)
	}
	return s
}
