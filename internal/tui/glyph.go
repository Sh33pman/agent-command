package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/brettmostert/agent-command/internal/core"
)

// Spinner frames for busy sessions (advance once per tick).
var Spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

var dimGlyph = lipgloss.NewStyle().Faint(true)

// Glyph is a session status indicator (SPEC §5.2; plain Unicode, ANSI
// colors so it follows the terminal's palette). spin picks the spinner
// frame for busy sessions.
func Glyph(s core.Status, spin int) string {
	c := func(color, g string) string { return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(g) }
	switch s {
	case core.StatusStarting:
		return dimGlyph.Render("…")
	case core.StatusBusy:
		return c("4", Spinner[spin%len(Spinner)])
	case core.StatusWaiting:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true).Render("!")
	case core.StatusDone:
		return c("2", "✓")
	case core.StatusError:
		return c("1", "✗")
	case core.StatusPaused:
		return c("8", "‖")
	default:
		return dimGlyph.Render("-")
	}
}

// StatusWord names a status for people ("working", "needs you", …).
func StatusWord(s core.Status) string {
	switch s {
	case core.StatusStarting:
		return "starting"
	case core.StatusBusy:
		return "working"
	case core.StatusWaiting:
		return "needs you"
	case core.StatusDone:
		return "done"
	case core.StatusError:
		return "failed"
	case core.StatusPaused:
		return "paused"
	case core.StatusArchived:
		return "archived"
	}
	return string(s)
}

// Tilde shortens p under home to ~/….
func Tilde(p, home string) string {
	if home != "" && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + p[len(home):]
	}
	return p
}
