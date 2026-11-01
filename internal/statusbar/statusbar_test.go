package statusbar

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

type fake struct {
	mu        sync.Mutex
	heights   []int
	mutations []ipc.Mutation
	focused   int
}

func (f *fake) Watch(ctx context.Context, _ uint64, _ bool) (ipc.WatchResponse, error) {
	<-ctx.Done()
	return ipc.WatchResponse{}, ctx.Err()
}
func (f *fake) Snapshot(context.Context) (ipc.Snapshot, error) { return ipc.Snapshot{}, nil }
func (f *fake) Mutate(_ context.Context, m ipc.Mutation, _ any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mutations = append(f.mutations, m)
	return nil
}
func (f *fake) FitHeight(_ context.Context, n int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heights = append(f.heights, n)
	return nil
}
func (f *fake) FocusView(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.focused++
	return nil
}

// send delivers msg and runs the commands it returns (not the watch).
func send(m *Model, msg tea.Msg) {
	_, cmd := m.Update(msg)
	run(cmd)
}

// run runs cmd and the commands it batches; one that blocks (the watch)
// is left after 100ms.
func run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if b, ok := msg.(tea.BatchMsg); ok {
			for _, c := range b {
				run(c)
			}
		}
	case <-time.After(100 * time.Millisecond):
	}
}

func screen(m *Model) []string { return strings.Split(ansi.Strip(m.render()), "\n") }

func TestBar(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fake{}
	m := New(ctx, f, "v1")
	send(m, tea.WindowSizeMsg{Width: 40, Height: 1})

	// Idle: a summary of the sessions.
	snap := ipc.Snapshot{Sessions: []*core.Session{
		{ID: "a", Status: core.StatusBusy}, {ID: "b", Status: core.StatusBusy}, {ID: "c", Status: core.StatusWaiting},
	}}
	send(m, stateMsg{resp: ipc.WatchResponse{Rev: 1}, snap: snap})
	if got := strings.TrimSpace(screen(m)[0]); got != "acmd · 1 needs you · 2 busy" {
		t.Errorf("summary %q", got)
	}

	// A long error wraps across the full width and grows the pane.
	long := "kitten @ resize-window: exit status 1: Error: Could not resize window, the split is at its limit"
	send(m, stateMsg{resp: ipc.WatchResponse{Rev: 2, Note: &ipc.ViewNote{Text: long, Err: true}}, snap: snap})
	f.mu.Lock()
	heights := append([]int(nil), f.heights...)
	f.mu.Unlock()
	if len(heights) == 0 || heights[len(heights)-1] != 3 {
		t.Fatalf("asked for heights %v, want ...3", heights)
	}
	send(m, tea.WindowSizeMsg{Width: 40, Height: 3})
	lines := screen(m)
	if len(lines) != 3 || !strings.HasPrefix(lines[0], " ✗ kitten @ resize-window") {
		t.Errorf("error bar:\n%s", strings.Join(lines, "\n"))
	}
	for _, l := range lines {
		if w := ansi.StringWidth(l); w != 40 {
			t.Errorf("line %q is %d wide", l, w)
		}
	}

	// A click dismisses it and focuses the view; the pane shrinks back
	// once the daemon reports the note gone.
	send(m, tea.MouseClickMsg{Button: tea.MouseLeft})
	f.mu.Lock()
	if len(f.mutations) != 1 || f.mutations[0].Kind != ipc.MutViewNote || f.mutations[0].Text != "" || f.focused != 1 {
		t.Errorf("dismiss: %+v focused %d", f.mutations, f.focused)
	}
	f.mu.Unlock()
	send(m, stateMsg{resp: ipc.WatchResponse{Rev: 3}, snap: snap})
	f.mu.Lock()
	if h := f.heights[len(f.heights)-1]; h != 1 {
		t.Errorf("didn't shrink back: %v", f.heights)
	}
	f.mu.Unlock()

	// The view closing ends the bar.
	send(m, goneMsg{})
	if !errors.Is(m.Err(), ErrClosed) {
		t.Errorf("err %v", m.Err())
	}
}
