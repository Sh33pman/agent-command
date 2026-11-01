package header

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

type fake struct {
	heights []int
	focused int
}

func (f *fake) Watch(ctx context.Context, _ uint64, _ bool) (ipc.WatchResponse, error) {
	<-ctx.Done()
	return ipc.WatchResponse{}, ctx.Err()
}
func (f *fake) Snapshot(context.Context) (ipc.Snapshot, error) { return ipc.Snapshot{}, nil }
func (f *fake) FitHeight(_ context.Context, n int) error {
	f.heights = append(f.heights, n)
	return nil
}
func (f *fake) FocusView(context.Context) error { f.focused++; return nil }

// run runs a non-blocking command (not the watch).
func run(cmd tea.Cmd) {
	if cmd != nil {
		cmd()
	}
}

func line(m *Model) string { return ansi.Strip(strings.Split(m.render(), "\n")[0]) }

func TestHeader(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fake{}
	m := New(ctx, f, "v1", "/Users/me")
	m.Update(tea.WindowSizeMsg{Width: 70, Height: 1})
	if got := strings.TrimSpace(line(m)); got != "acmd" {
		t.Errorf("before loading: %q", got)
	}

	s := &core.Session{ID: "s1", Name: "auth work", Description: "refactor the middleware", Status: core.StatusBusy,
		StatusDetail: "Bash", Cwd: "/Users/me/w/api"}
	snap := ipc.Snapshot{Sessions: []*core.Session{s}}
	m.Update(stateMsg{resp: ipc.WatchResponse{Rev: 1, View: &core.View{ID: "v1", Selected: "s1"}}, snap: snap})
	got := line(m)
	for _, want := range []string{"⠋ working: Bash · auth work · refactor the middleware", "~/w/api "} {
		if !strings.Contains(got, want) {
			t.Errorf("header %q lacks %q", got, want)
		}
	}
	if w := ansi.StringWidth(got); w != 70 {
		t.Errorf("width %d", w)
	}

	// Narrow: the description gives way before the folder and the name.
	m.Update(tea.WindowSizeMsg{Width: 44, Height: 1})
	got = line(m)
	if !strings.Contains(got, "auth work") || !strings.Contains(got, "~/w/api") || ansi.StringWidth(got) != 44 {
		t.Errorf("narrow header %q", got)
	}

	// Taller than one line: ask for one.
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 44, Height: 18})
	run(cmd)
	if len(f.heights) != 1 || f.heights[0] != 1 {
		t.Errorf("heights %v", f.heights)
	}

	// The placeholder; a click focuses the view; the view closing ends it.
	m.Update(stateMsg{resp: ipc.WatchResponse{Rev: 2, View: &core.View{ID: "v1"}}, snap: snap})
	if got := strings.TrimSpace(line(m)); got != "No session shown" {
		t.Errorf("placeholder: %q", got)
	}
	_, cmd = m.Update(tea.MouseClickMsg{Button: tea.MouseLeft})
	run(cmd)
	if f.focused != 1 {
		t.Error("click didn't focus the view")
	}
	m.Update(goneMsg{})
	if !errors.Is(m.Err(), ErrClosed) {
		t.Errorf("err %v", m.Err())
	}
}
