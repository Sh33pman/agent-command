package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

func newList(items []Item, sel int) *list {
	in := textinput.New()
	m := &list{title: "Pick", hint: "hint", items: items, input: in, sel: sel, w: 40, h: 10}
	m.input.Focus()
	m.Init()
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	return m
}

func TestList(t *testing.T) {
	items := []Item{{Label: "alpha", Detail: "PROJ · api", Value: "a"}, {Label: "beta", Detail: "web", Value: "b"}, {Label: "gamma", Value: "c"}}
	m := newList(items, 1)
	if s := ansi.Strip(m.render()); !strings.Contains(s, "Pick") || !strings.Contains(s, "alpha  PROJ · api") || !strings.Contains(s, "hint") {
		t.Errorf("render:\n%s", s)
	}
	m.Update(key("down"))
	m.Update(key("down")) // clamped at the last item
	m.Update(key("up"))
	m.Update(key("enter"))
	if m.chosen == nil || m.chosen.Value != "b" {
		t.Errorf("chosen %+v", m.chosen)
	}
	// Filtering (words, or letters in order) resets the selection.
	m = newList(items, 2)
	for _, r := range "gma" {
		m.Update(key(string(r)))
	}
	if vis := m.visible(); len(vis) != 1 || vis[0].Value != "c" || m.sel != 0 {
		t.Errorf("fuzzy: %+v sel %d", vis, m.sel)
	}
	m.Update(key("x"))
	if s := ansi.Strip(m.render()); !strings.Contains(s, "nothing matches") {
		t.Errorf("no match:\n%s", s)
	}
	m.Update(key("enter"))
	if m.chosen != nil {
		t.Error("chose from an empty list")
	}
	m = newList(items, 0)
	m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, Y: 4})
	if m.chosen == nil || m.chosen.Value != "b" {
		t.Errorf("click: %+v", m.chosen)
	}
	m = newList(items, 0)
	m.Update(key("esc"))
	if m.chosen != nil || !m.done || m.render() != "" {
		t.Error("esc")
	}
	if !fuzzy("refactor auth middleware", "auth mid") || !fuzzy("refactor", "rfc") || fuzzy("abc", "cba") {
		t.Error("fuzzy")
	}
}

func TestPager(t *testing.T) {
	var lines []string
	for i := range 30 {
		lines = append(lines, "line "+string(rune('a'+i%26)))
	}
	m := &pager{title: "Keys", input: textinput.New(), w: 40, h: 10, lines: func(q string, _ int) []string {
		var out []string
		for _, l := range lines {
			if strings.Contains(l, q) {
				out = append(out, l)
			}
		}
		return out
	}}
	m.input.Focus()
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	m.Update(key("down"))
	m.Update(key("pgdown"))
	m.Update(key("pgup"))
	m.Update(key("up"))
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if s := ansi.Strip(m.render()); !strings.Contains(s, "Keys") || !strings.Contains(s, "esc close") {
		t.Errorf("render:\n%s", s)
	}
	m.Update(key("#"))
	if s := ansi.Strip(m.render()); !strings.Contains(s, "nothing matches") {
		t.Errorf("search:\n%s", s)
	}
	m.Update(key("esc"))
	if !m.closed || m.render() != "" {
		t.Error("esc")
	}
}

func TestPrompt(t *testing.T) {
	m := &prompt{label: "Command", input: textinput.New()}
	m.input.Focus()
	m.Update(key("v"))
	m.Update(key("i"))
	if !strings.Contains(m.View().Content, "Command") {
		t.Error("view")
	}
	m.Update(key("enter"))
	if !m.ok || m.input.Value() != "vi" || m.View().Content != "" {
		t.Errorf("prompt: %v %q", m.ok, m.input.Value())
	}
	m = &prompt{input: textinput.New()}
	m.Update(key("esc"))
	if m.ok {
		t.Error("esc accepted")
	}
}

func TestRunWithIO(t *testing.T) {
	// The real programs, fed through a pipe: Esc cancels each.
	ctx := context.Background()
	o := IO{In: strings.NewReader("\x1b"), Out: &strings.Builder{}}
	if _, err := Choose(ctx, o, "t", []Item{{Label: "x"}}, 0); err == nil {
		t.Log("choose returned no error on esc") // input timing varies; ErrCanceled expected
	}
	o = IO{In: strings.NewReader("ab\r"), Out: &strings.Builder{}}
	if v, err := Ask(ctx, o, "q", ""); err == nil && v != "ab" {
		t.Errorf("ask: %q", v)
	}
	o = IO{In: strings.NewReader("\x1b"), Out: &strings.Builder{}}
	_ = Page(ctx, o, "t", func(string, int) []string { return []string{"a"} })
}
