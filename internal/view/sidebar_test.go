package view

import (
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

func TestNewFolder(t *testing.T) {
	t0 := time.Now()
	a := &core.Session{ID: "a", Cwd: "/a", LastEventAt: t0}
	b := &core.Session{ID: "b", Cwd: "/b", LastEventAt: t0.Add(time.Minute)}
	snap := ipc.Snapshot{Sessions: []*core.Session{a, b}}
	for _, c := range []struct {
		sel, want string
		snap      ipc.Snapshot
	}{{"a", "/a", snap}, {"", "/b", snap}, {"", "/home", ipc.Snapshot{}}} {
		if got := NewFolder(c.snap, &core.View{Selected: c.sel}, "/home"); got != c.want {
			t.Errorf("NewFolder(selected %q) = %s, want %s", c.sel, got, c.want)
		}
	}
}
