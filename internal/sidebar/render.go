package sidebar

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/brettmostert/agent-command/internal/core"
)

const (
	bodyTop   = 2 // header and rule
	footerMin = 3 // rule, tooltip, status line
)

var (
	dimStyle    = lipgloss.NewStyle().Faint(true)
	boldStyle   = lipgloss.NewStyle().Bold(true)
	cursorStyle = lipgloss.NewStyle().Reverse(true)
	noteStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	barStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	spinner     = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	groupColors = map[string]string{"red": "1", "green": "2", "yellow": "3", "blue": "4", "magenta": "5", "cyan": "6"}
)

// glyph is the status indicator (SPEC §5.2; plain Unicode, ANSI colors so
// it follows the terminal's palette).
func (m *Model) glyph(s core.Status) string {
	c := func(color, g string) string { return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(g) }
	switch s {
	case core.StatusStarting:
		return dimStyle.Render("…")
	case core.StatusBusy:
		return c("4", spinner[m.spin%len(spinner)])
	case core.StatusWaiting:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true).Render("!")
	case core.StatusDone:
		return c("2", "✓")
	case core.StatusError:
		return c("1", "✗")
	case core.StatusPaused:
		return c("8", "‖")
	default:
		return dimStyle.Render("-")
	}
}

func colorDot(g *core.Group) string {
	if g == nil || g.IsDefault {
		return " "
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(groupColors[g.Color])).Render("●")
}

// line is one body line and the row it belongs to (for mouse hits).
type line struct {
	text string
	key  string
}

func (m *Model) bodyLines() []line {
	var out []line
	selected := ""
	if m.view != nil {
		selected = m.view.Selected
	}
	listDots := m.snap.ViewState.Mode == "list" && m.groupsExist()
	for _, r := range m.rows() {
		k := key(r)
		var text []string
		if r.Session == nil {
			g := r.Group
			arrow := "▾"
			if r.Collapsed {
				arrow = "▸"
			}
			head := fmt.Sprintf("%s %s %s", arrow, colorDot(g), g.Name)
			if g.IsDefault {
				head = arrow + " " + g.Name
			}
			if r.Focused {
				head = "◉" + head[len("▾"):]
			}
			tail := fmt.Sprintf(" %d", r.Count)
			if r.Collapsed && r.Urgent != "" {
				tail += " " + m.glyph(r.Urgent)
			}
			text = append(text, fit(" "+boldStyle.Render(head), m.w-lipgloss.Width(tail))+tail)
		} else {
			s := r.Session
			bar := " "
			if s.ID == selected {
				bar = barStyle.Render("▌")
			}
			num := " "
			if r.Number > 0 && r.Number < 10 {
				num = fmt.Sprint(r.Number)
			}
			mark := " "
			if m.marked[s.ID] {
				mark = "+"
			}
			badges := ""
			if s.Kind == core.KindExternal {
				badges += " ↗"
			}
			if s.Unseen {
				badges += " •"
			}
			dot := ""
			if listDots {
				dot = colorDot(r.Group) + " "
			}
			nm := name(s)
			if s.Unseen && s.Status == core.StatusDone {
				nm = boldStyle.Render(nm)
			}
			head := fmt.Sprintf("%s%s%s%s %s", bar, num, mark, m.glyph(s.Status), dot)
			room := m.w - lipgloss.Width(head) - lipgloss.Width(badges)
			text = append(text, head+fit(nm, room)+badges)
			if !m.compact {
				detail := []string{filepath.Base(s.Cwd), age(m.now(), s)}
				if s.StatusDetail != "" {
					detail = append(detail, s.StatusDetail)
				}
				text = append(text, dimStyle.Render("     "+fit(strings.Join(detail, " · "), m.w-5)))
			}
		}
		for i, t := range text {
			if k == m.cursor && i == 0 && m.mode == modeNormal {
				t = cursorStyle.Render(padTo(ansi.Strip(t), m.w))
			}
			out = append(out, line{text: t, key: k})
		}
	}
	return out
}

func (m *Model) footer() []string {
	rule := dimStyle.Render(" " + strings.Repeat("─", max(0, m.w-2)))
	out := []string{rule}
	if r, ok := m.current(); ok && m.mode == modeNormal {
		if s := r.Session; s != nil {
			out = append(out, fit(" "+name(s), m.w))
			info := tilde(s.Cwd, m.home)
			if r.Group != nil && m.groupsExist() {
				info = r.Group.Name + " · " + info
			}
			if s.Description != "" {
				out = append(out, dimStyle.Render(fit(" "+s.Description, m.w)))
			}
			out = append(out, dimStyle.Render(fit(" "+info, m.w)))
		}
	}
	if m.note != "" {
		st := dimStyle
		if m.noteErr {
			st = noteStyle
		}
		out = append(out, st.Render(fit(" "+m.note, m.w)))
	}
	switch m.mode {
	case modeInput:
		out = append(out, fit(" "+m.label+": ", m.w), " "+m.input.View())
	case modeConfirm:
		out = append(out, wrap(" "+m.label, m.w)...)
		out = append(out, boldStyle.Render(" y")+" yes · any other key: no")
	}
	vs := m.snap.ViewState
	arrow := "↑"
	if vs.Desc {
		arrow = "↓"
	}
	mode := vs.Mode
	if mode == "" {
		mode = "groups"
	}
	status := fmt.Sprintf(" %s · %s %s", mode, sortOrDefault(vs.Sort), arrow)
	if vs.Filter != "" {
		status += " · /" + vs.Filter
	}
	if vs.ShowArchived {
		status += " · +archived"
	}
	keys := "n new ? keys"
	if n := len(m.marked); n > 0 {
		keys = fmt.Sprintf("%d marked", n)
	}
	if pad := m.w - lipgloss.Width(status) - len(keys) - 1; pad > 0 {
		status += strings.Repeat(" ", pad) + keys
	}
	return append(out, dimStyle.Render(fit(status, m.w)))
}

func (m *Model) bodyHeight() int {
	return max(1, m.h-bodyTop-len(m.footer()))
}

// keyAt maps a screen line to the row shown there.
func (m *Model) keyAt(y int) (string, bool) {
	i := m.top + y - bodyTop
	lines := m.bodyLines()
	if y < bodyTop || y-bodyTop >= m.bodyHeight() || i < 0 || i >= len(lines) {
		return "", false
	}
	return lines[i].key, true
}

// scrollToCursor keeps the cursor's lines visible.
func (m *Model) scrollToCursor() {
	lines := m.bodyLines()
	first, last := -1, -1
	for i, l := range lines {
		if l.key == m.cursor {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return
	}
	h := m.bodyHeight()
	if first < m.top {
		m.top = first
	}
	if last >= m.top+h {
		m.top = last - h + 1
	}
}

// View renders the sidebar.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m *Model) render() string {
	active, paused := 0, 0
	for _, s := range m.snap.Sessions {
		switch s.Status {
		case core.StatusPaused:
			paused++
		case core.StatusArchived:
		default:
			active++
		}
	}
	out := []string{
		boldStyle.Render(fit(fmt.Sprintf(" AGENTS  ▸ %d active · %d paused", active, paused), m.w)),
		dimStyle.Render(" " + strings.Repeat("─", max(0, m.w-2))),
	}
	h := m.bodyHeight()
	var body []string
	switch {
	case !m.loaded:
		body = []string{dimStyle.Render(" loading…")}
	case m.mode == modePicker:
		body = m.pickerLines()
	case m.mode == modeHelp:
		body = helpLines(m.w)
	default:
		lines := m.bodyLines()
		if len(lines) == 0 {
			body = []string{dimStyle.Render(" No sessions."), dimStyle.Render(" n starts one.")}
			if m.snap.ViewState.Filter != "" {
				body = []string{dimStyle.Render(" Nothing matches the filter."), dimStyle.Render(" Esc clears it.")}
			}
		}
		m.top = max(0, min(m.top, len(lines)-h))
		for i := m.top; i < len(lines) && i < m.top+h; i++ {
			body = append(body, lines[i].text)
		}
		if m.mode == modeMenu {
			body = m.overlayMenu(body)
		}
	}
	for len(body) < h {
		body = append(body, "")
	}
	out = append(out, body[:h]...)
	out = append(out, m.footer()...)
	for i, l := range out {
		out[i] = padTo(l, m.w)
	}
	return strings.Join(out, "\n")
}

func (m *Model) pickerLines() []string {
	p := m.picker
	out := []string{boldStyle.Render(fit(" "+p.label, m.w)), " > " + m.input.View()}
	for i, it := range m.pickItems() {
		label := ""
		if it.group != nil {
			label = colorDot(it.group) + " " + it.group.Name
			if it.group.IsDefault {
				label = "○ " + it.group.Name
			}
		} else {
			label = "＋ Create group " + quote(it.create)
		}
		label = fit("   "+label, m.w)
		if i == p.sel {
			label = cursorStyle.Render(padTo(ansi.Strip(label), m.w))
		}
		out = append(out, label)
	}
	return append(out, dimStyle.Render(fit(" ↵ choose · esc cancel", m.w)))
}

// overlayMenu draws the context menu over the body lines.
func (m *Model) overlayMenu(body []string) []string {
	mn := m.menu
	width := lipgloss.Width(mn.title) + 4
	for _, it := range mn.items {
		width = max(width, lipgloss.Width(it.label)+4)
	}
	width = min(width, m.w)
	box := []string{boldStyle.Render(fit(" "+mn.title, width))}
	for i, it := range mn.items {
		l := padTo(fit("  "+it.label, width), width)
		if i == mn.sel {
			l = cursorStyle.Render(l)
		}
		box = append(box, l)
	}
	start := mn.y - 1 - bodyTop // title line
	for i, l := range box {
		at := start + i
		for at >= len(body) {
			body = append(body, "")
		}
		if at >= 0 {
			body[at] = padTo(l, m.w)
		}
	}
	return body
}

func helpLines(w int) []string {
	keys := [][2]string{
		{"↑↓ j k", "move"}, {"↵ 1–9", "show"}, {"n / N", "new (/ in folder)"}, {"r e", "rename · describe"},
		{"m", "move to group"}, {"space", "mark / collapse"}, {"← →", "collapse · expand"}, {"f z", "focus group · all"},
		{"v", "groups ↔ list"}, {"o O", "sort · direction"}, {"K J D", "group up · down · delete"}, {"a A", "archive · show archived"},
		{"/", "filter (g:name)"}, {"tab", "next needing you"}, {"h c", "hide · compact"}, {"esc", "back to the session"},
		{"click", "show · collapse"}, {"2×click", "rename"}, {"r-click", "menu"},
	}
	out := []string{boldStyle.Render(" Keys")}
	for _, k := range keys {
		out = append(out, fit(fmt.Sprintf(" %-7s %s", k[0], k[1]), w))
	}
	return append(out, dimStyle.Render(" any key to close"))
}

// fit truncates to n cells with an ellipsis.
func fit(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return ansi.Truncate(s, n, "…")
}

func padTo(s string, n int) string {
	s = ansi.Truncate(s, n, "")
	if w := lipgloss.Width(s); w < n {
		s += strings.Repeat(" ", n-w)
	}
	return s
}

func wrap(s string, w int) []string {
	var out []string
	words := strings.Fields(s)
	cur := ""
	for _, word := range words {
		if cur != "" && lipgloss.Width(cur)+1+lipgloss.Width(word) > w-1 {
			out = append(out, " "+cur)
			cur = word
			continue
		}
		if cur == "" {
			cur = word
		} else {
			cur += " " + word
		}
	}
	if cur != "" {
		out = append(out, " "+cur)
	}
	return out
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
