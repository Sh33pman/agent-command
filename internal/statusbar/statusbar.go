// Package statusbar is a view's full-width status bar (SPEC §8.4): a thin
// Kitty pane along the bottom of the tab, under the sidebar and the view
// pane. It shows the view's latest message (sent by the sidebar through
// the daemon) or, when there is none, a summary of the sessions.
package statusbar

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/tui"
)

// MaxLines is the most lines the bar grows to for a long error.
const MaxLines = 4

// ErrClosed means the bar's view was closed.
var ErrClosed = errors.New("view closed")

var (
	dim    = lipgloss.NewStyle().Faint(true)
	errBar = lipgloss.NewStyle().Background(lipgloss.Color("1")).Foreground(lipgloss.Color("15"))
)

// Actions is what the bar needs from the daemon and the terminal.
type Actions interface {
	// Watch waits for a change after rev (passive: not a heartbeat).
	Watch(ctx context.Context, rev uint64, wait bool) (ipc.WatchResponse, error)
	Snapshot(ctx context.Context) (ipc.Snapshot, error)
	Mutate(ctx context.Context, m ipc.Mutation, out any) error
	// FitHeight resizes the bar's pane to lines.
	FitHeight(ctx context.Context, lines int) error
	FocusView(ctx context.Context) error
}

// Model is the bar's Bubble Tea model.
type Model struct {
	ctx    context.Context
	act    Actions
	viewID string

	note   *ipc.ViewNote
	snap   ipc.Snapshot
	rev    uint64
	loaded bool
	w, h   int
	want   int // the height last asked for
	err    error
}

// New returns the bar for view viewID.
func New(ctx context.Context, act Actions, viewID string) *Model {
	return &Model{ctx: ctx, act: act, viewID: viewID, w: 80, h: 1, want: 1}
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
)

// Init starts watching the view.
func (m *Model) Init() tea.Cmd { return m.watch(0, false) }

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
		m.note, m.snap, m.rev, m.loaded = msg.resp.Note, msg.snap, msg.resp.Rev, true
		return m, tea.Batch(m.watch(m.rev, true), m.fit(false))
	case goneMsg:
		m.err = ErrClosed
		return m, tea.Quit
	case fatalMsg:
		m.err = msg.err
		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, m.fit(true)
	case tea.KeyPressMsg, tea.MouseClickMsg:
		return m, m.dismiss()
	}
	return m, nil
}

// fit asks for the height the content needs: when it changed, or (after
// a resize) when the pane isn't that height.
func (m *Model) fit(resized bool) tea.Cmd {
	want := len(m.lines())
	if want == m.want && (!resized || m.h == want) {
		return nil
	}
	m.want = want
	return func() tea.Msg {
		_ = m.act.FitHeight(m.ctx, want) // best effort: the bar still draws
		return nil
	}
}

// dismiss clears the message and gives focus back to the view pane.
func (m *Model) dismiss() tea.Cmd {
	return func() tea.Msg {
		if m.note != nil {
			_ = m.act.Mutate(m.ctx, ipc.Mutation{Kind: ipc.MutViewNote, ID: m.viewID}, nil)
		}
		_ = m.act.FocusView(m.ctx)
		return nil
	}
}

// View draws the bar.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen, v.MouseMode = true, tea.MouseModeCellMotion
	return v
}

func (m *Model) render() string {
	lines := m.lines()
	out := make([]string, 0, max(m.h, 1))
	for i := 0; i < max(m.h, 1); i++ {
		if i < len(lines) {
			out = append(out, lines[i])
		} else {
			out = append(out, strings.Repeat(" ", max(0, m.w)))
		}
	}
	return strings.Join(out, "\n")
}

// lines is the bar's content, each line exactly m.w wide.
func (m *Model) lines() []string {
	if m.w <= 2 {
		return []string{""}
	}
	if n := m.note; n != nil && n.Text != "" {
		text, st := n.Text, dim
		if n.Err {
			text, st = "✗ "+text, errBar
		}
		lines := tui.BarLines(strings.Join(strings.Fields(text), " "), m.w-2, MaxLines)
		for i, l := range lines {
			lines[i] = st.Render(pad(" "+l+" ", m.w))
		}
		return lines
	}
	return []string{dim.Render(pad(" "+m.summary(), m.w))}
}

// summary counts the sessions by status, e.g. "acmd · 2 busy · 1 needs you".
func (m *Model) summary() string {
	if !m.loaded {
		return "acmd"
	}
	count := map[core.Status]int{}
	for _, s := range m.snap.Sessions {
		count[s.Status]++
	}
	parts := []string{"acmd"}
	for _, c := range []struct {
		st    core.Status
		label string
	}{
		{core.StatusWaiting, "needs you"},
		{core.StatusError, "failed"},
		{core.StatusBusy, "busy"},
		{core.StatusDone, "done"},
		{core.StatusPaused, "paused"},
	} {
		if n := count[c.st]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, c.label))
		}
	}
	if len(parts) == 1 {
		parts = append(parts, "no sessions")
	}
	return strings.Join(parts, " · ")
}

func pad(s string, n int) string {
	s = ansi.Truncate(s, n, "")
	if w := lipgloss.Width(s); w < n {
		s += strings.Repeat(" ", n-w)
	}
	return s
}
