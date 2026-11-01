package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// BarLines word-wraps s to width w (breaking words longer than w) and
// keeps at most limit lines, marking a cut with "…". Used by the status
// bars (sidebar and full-width).
func BarLines(s string, w, limit int) []string {
	lines := strings.Split(ansi.Wrap(s, w, ""), "\n")
	if len(lines) > limit {
		lines = lines[:limit]
		last := strings.TrimRight(lines[limit-1], " ")
		if lipgloss.Width(last) >= w {
			last = ansi.Truncate(last, w-1, "")
		}
		lines[limit-1] = last + "…"
	}
	return lines
}
