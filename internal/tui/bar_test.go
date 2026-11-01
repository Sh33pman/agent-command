package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestBarLines(t *testing.T) {
	got := BarLines("aaaa bbbb cccc", 9, 4)
	if strings.Join(got, "|") != "aaaa bbbb|cccc" {
		t.Errorf("wrap: %q", got)
	}
	got = BarLines("/a/very/long/path/without/spaces", 10, 2)
	if len(got) != 2 || !strings.HasSuffix(got[1], "…") || ansi.StringWidth(got[1]) > 10 {
		t.Errorf("hard wrap and cut: %q", got)
	}
}
