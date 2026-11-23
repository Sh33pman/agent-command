package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/brettmostert/agent-command/internal/core"
)

// Styles: plain ANSI, which every terminal draws, Windows' included.
const (
	bold    = "\x1b[1m"
	dim     = "\x1b[2m"
	reverse = "\x1b[7m"
	reset   = "\x1b[0m"
)

// keysHelp is the bottom line in the list.
const keysHelp = "↑↓/jk move · enter attach (ctrl+] comes back) · a answer · n new · / filter · q quit"

// View draws the list in the whole terminal.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

// render is the screen as text, ANSI styles included.
func (m Model) render() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	var b strings.Builder
	b.WriteString(bold + m.title() + reset + "\n")
	lines := m.lines(width)
	// Room for the title, the status line and the keys.
	room := len(lines)
	if m.height > 0 {
		room = max(m.height-3, 1)
	}
	first := 0
	if m.sel >= room {
		first = m.sel - room + 1
	}
	for i := first; i < len(lines) && i < first+room; i++ {
		b.WriteString(lines[i] + "\n")
	}
	b.WriteString(ansi.Truncate(m.status, width, "…") + "\n")
	b.WriteString(dim + ansi.Truncate(m.prompt(), width, "…") + reset)
	return b.String()
}

func (m Model) title() string {
	if !m.loaded {
		return "acmd · loading…"
	}
	n, waiting := 0, 0
	for _, r := range m.rows {
		if r.isSession() {
			n++
			if r.waiting != nil || r.s.Status.NeedsAttention() {
				waiting++
			}
		}
	}
	t := fmt.Sprintf("acmd · %d sessions", n)
	if waiting > 0 {
		t += fmt.Sprintf(" · %d need you", waiting)
	}
	if m.filter != "" {
		t += " · filter: " + m.filter
	}
	return t
}

// lines are the rows, one line each, the selected one reversed.
func (m Model) lines(width int) []string {
	out := make([]string, len(m.rows))
	for i, r := range m.rows {
		var line string
		switch {
		case r.section != "":
			line = bold + r.section + reset
		case r.group != "":
			line = "  " + r.group
		default:
			line = "    " + sessionLine(r)
		}
		line = ansi.Truncate(line, width, "…")
		if i == m.sel && r.isSession() {
			line = reverse + ansi.Strip(line) + reset
		}
		out[i] = line
	}
	if len(m.rows) == 1 && m.loaded {
		out = append(out, "    no sessions yet: n starts one")
	}
	return out
}

// sessionLine is a session's name, status and what's waiting.
func sessionLine(r row) string {
	s := r.s
	line := fmt.Sprintf("%-24s %-8s", ansi.Truncate(sessionName(s), 24, "…"), s.Status)
	if r.waiting != nil {
		line += "  " + waitingText(r.waiting)
	} else if s.Status.NeedsAttention() {
		line += "  needs you"
	}
	return line
}

// waitingText is an Inbox item on one line, with its options.
func waitingText(it *core.InboxItem) string {
	text := strings.Join(strings.Fields(it.Text), " ")
	if r := []rune(text); len(r) > 50 {
		text = string(r[:49]) + "…"
	}
	text = it.Kind + ": " + text
	if len(it.Options) > 0 {
		opts := make([]string, len(it.Options))
		for i, o := range it.Options {
			opts[i] = fmt.Sprintf("%d %s", i+1, o)
		}
		text += " (" + strings.Join(opts, " · ") + ")"
	}
	return text
}

// prompt is the bottom line: what to type now, or the keys.
func (m Model) prompt() string {
	switch m.mode {
	case modeFilter:
		return "filter: " + m.input + "▏ (enter keeps it, esc clears it)"
	case modeAnswer:
		return fmt.Sprintf("answer %s: press 1-%d (esc cancels)", m.name(m.item), len(m.item.Options))
	case modeReply:
		return "reply to " + m.name(m.item) + ": " + m.input + "▏ (enter sends, esc cancels)"
	case modeNew:
		where := "here"
		if m.newOn != "" {
			where = "on " + m.newOn
		}
		return "new session " + where + ", in folder: " + m.input + "▏ (enter starts, esc cancels)"
	}
	return keysHelp
}
