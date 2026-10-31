// Package tui holds small full-screen Bubble Tea pieces used by acmd's
// overlays: a filterable list to choose from (session picker, editor
// picker), a searchable pager (cheat sheet) and a one-line prompt.
package tui

import (
	"context"
	"errors"
	"io"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ErrCanceled means the user pressed Esc.
var ErrCanceled = errors.New("canceled")

var (
	dim     = lipgloss.NewStyle().Faint(true)
	bold    = lipgloss.NewStyle().Bold(true)
	reverse = lipgloss.NewStyle().Reverse(true)
)

// Item is one choice.
type Item struct {
	Label  string // shown; matched by the filter
	Detail string // dim, after the label; matched too
	Value  string
}

// IO is where a program reads keys and draws (nil = the terminal).
type IO struct {
	In  io.Reader
	Out io.Writer
}

func (o IO) opts(ctx context.Context) []tea.ProgramOption {
	opts := []tea.ProgramOption{tea.WithContext(ctx)}
	if o.In != nil {
		opts = append(opts, tea.WithInput(o.In))
	}
	if o.Out != nil {
		opts = append(opts, tea.WithOutput(o.Out))
	}
	return opts
}

// ---- list ----

type list struct {
	title  string
	hint   string
	items  []Item
	input  textinput.Model
	sel    int
	top    int
	w, h   int
	chosen *Item
	done   bool
}

// Choose shows items (with selected preselected) and returns the chosen
// one, or ErrCanceled.
func Choose(ctx context.Context, o IO, title string, items []Item, selected int) (Item, error) {
	in := textinput.New()
	in.Prompt = "> "
	in.Placeholder = "type to filter"
	m := &list{title: title, hint: "↑↓ move · ↵ choose · esc cancel", items: items, input: in, sel: max(0, selected), w: 60, h: 20}
	m.input.Focus()
	if _, err := tea.NewProgram(m, o.opts(ctx)...).Run(); err != nil {
		return Item{}, err
	}
	if m.chosen == nil {
		return Item{}, ErrCanceled
	}
	return *m.chosen, nil
}

func (m *list) visible() []Item {
	q := strings.ToLower(strings.TrimSpace(m.input.Value()))
	if q == "" {
		return m.items
	}
	var out []Item
	for _, it := range m.items {
		if fuzzy(strings.ToLower(it.Label+" "+it.Detail), q) {
			out = append(out, it)
		}
	}
	return out
}

// fuzzy reports whether every word of q occurs in s, or q's letters occur
// in order.
func fuzzy(s, q string) bool {
	all := true
	for _, w := range strings.Fields(q) {
		if !strings.Contains(s, w) {
			all = false
		}
	}
	if all {
		return true
	}
	i := 0
	for _, r := range s {
		if i < len(q) && rune(q[i]) == r {
			i++
		}
	}
	return i == len(q)
}

func (m *list) Init() tea.Cmd { return textinput.Blink }

func (m *list) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case tea.KeyPressMsg:
		vis := m.visible()
		switch msg.String() {
		case "esc", "ctrl+c":
			m.done = true
			return m, tea.Quit
		case "enter":
			if len(vis) > 0 {
				it := vis[min(m.sel, len(vis)-1)]
				m.chosen = &it
			}
			m.done = true
			return m, tea.Quit
		case "up", "ctrl+p":
			m.sel = max(0, m.sel-1)
			return m, nil
		case "down", "ctrl+n":
			m.sel = min(max(0, len(vis)-1), m.sel+1)
			return m, nil
		}
		before := m.input.Value()
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		if m.input.Value() != before {
			m.sel, m.top = 0, 0
		}
		return m, cmd
	case tea.MouseClickMsg:
		i := msg.Mouse().Y - 3 + m.top
		if vis := m.visible(); msg.Mouse().Button == tea.MouseLeft && i >= 0 && i < len(vis) {
			it := vis[i]
			m.chosen, m.done = &it, true
			return m, tea.Quit
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *list) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen, v.MouseMode = true, tea.MouseModeCellMotion
	return v
}

func (m *list) render() string {
	if m.done {
		return ""
	}
	out := []string{bold.Render(fit(" "+m.title, m.w)), " " + m.input.View(), ""}
	vis := m.visible()
	rows := max(1, m.h-len(out)-2)
	if m.sel < m.top {
		m.top = m.sel
	}
	if m.sel >= m.top+rows {
		m.top = m.sel - rows + 1
	}
	if len(vis) == 0 {
		out = append(out, dim.Render(" nothing matches"))
	}
	for i := m.top; i < len(vis) && i < m.top+rows; i++ {
		it := vis[i]
		l := " " + it.Label
		if it.Detail != "" {
			l += "  " + dim.Render(it.Detail)
		}
		l = fit(l, m.w)
		if i == m.sel {
			l = reverse.Render(pad(ansi.Strip(l), m.w))
		}
		out = append(out, l)
	}
	for len(out) < m.h-1 {
		out = append(out, "")
	}
	return strings.Join(append(out, dim.Render(fit(" "+m.hint, m.w))), "\n")
}

// ---- pager ----

type pager struct {
	title  string
	lines  func(query string, width int) []string
	input  textinput.Model
	top    int
	w, h   int
	closed bool
}

// Page shows lines (recomputed for the query and width) until Esc/q.
func Page(ctx context.Context, o IO, title string, lines func(query string, width int) []string) error {
	in := textinput.New()
	in.Prompt = "search: "
	m := &pager{title: title, lines: lines, input: in, w: 80, h: 24}
	m.input.Focus()
	_, err := tea.NewProgram(m, o.opts(ctx)...).Run()
	return err
}

func (m *pager) Init() tea.Cmd { return textinput.Blink }

func (m *pager) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "ctrl+c":
			m.closed = true
			return m, tea.Quit
		case "up":
			m.top = max(0, m.top-1)
			return m, nil
		case "down":
			m.top++
			return m, nil
		case "pgdown", "space":
			m.top += max(1, m.h-4)
			return m, nil
		case "pgup":
			m.top = max(0, m.top-max(1, m.h-4))
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.top = 0
		return m, cmd
	case tea.MouseWheelMsg:
		if msg.Mouse().Button == tea.MouseWheelUp {
			m.top = max(0, m.top-2)
		} else {
			m.top += 2
		}
		return m, nil
	}
	return m, nil
}

func (m *pager) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen, v.MouseMode = true, tea.MouseModeCellMotion
	return v
}

func (m *pager) render() string {
	if m.closed {
		return ""
	}
	body := m.lines(m.input.Value(), m.w-2)
	rows := max(1, m.h-4)
	m.top = max(0, min(m.top, len(body)-rows))
	out := []string{bold.Render(fit(" "+m.title, m.w)) + "", " " + m.input.View(), ""}
	if len(body) == 0 {
		out = append(out, dim.Render(" nothing matches"))
	}
	for i := m.top; i < len(body) && i < m.top+rows; i++ {
		out = append(out, fit(" "+body[i], m.w))
	}
	for len(out) < m.h-1 {
		out = append(out, "")
	}
	return strings.Join(append(out, dim.Render(fit(" esc close · ↑↓ scroll", m.w))), "\n")
}

// ---- prompt ----

type prompt struct {
	label string
	input textinput.Model
	ok    bool
	done  bool
}

// Ask reads one line (value preset), or ErrCanceled.
func Ask(ctx context.Context, o IO, label, value string) (string, error) {
	in := textinput.New()
	in.Prompt = "> "
	in.SetValue(value)
	in.CursorEnd()
	m := &prompt{label: label, input: in}
	m.input.Focus()
	if _, err := tea.NewProgram(m, o.opts(ctx)...).Run(); err != nil {
		return "", err
	}
	if !m.ok {
		return "", ErrCanceled
	}
	return m.input.Value(), nil
}

func (m *prompt) Init() tea.Cmd { return textinput.Blink }

func (m *prompt) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "enter":
			m.ok, m.done = true, true
			return m, tea.Quit
		case "esc", "ctrl+c":
			m.done = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *prompt) View() tea.View {
	if m.done {
		return tea.NewView("")
	}
	return tea.NewView(bold.Render(m.label) + "\n" + m.input.View() + "\n" + dim.Render("↵ ok · esc cancel"))
}

func fit(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return ansi.Truncate(s, n, "…")
}

func pad(s string, n int) string {
	s = ansi.Truncate(s, n, "")
	if w := lipgloss.Width(s); w < n {
		s += strings.Repeat(" ", n-w)
	}
	return s
}
