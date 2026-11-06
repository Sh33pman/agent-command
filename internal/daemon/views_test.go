package daemon

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

func newSess(t *testing.T, c *ipc.Conn, req ipc.NewRequest) *core.Session {
	t.Helper()
	var s core.Session
	if err := c.Call(ipc.OpNew, req, &s); err != nil {
		t.Fatal(err)
	}
	return &s
}

func TestWithoutClaude(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	for _, call := range []func() error{
		func() error { return c.Call(ipc.OpNew, ipc.NewRequest{Cwd: "/w"}, nil) },
		func() error { return c.Call(ipc.OpResume, ipc.Mutation{ID: "x"}, nil) },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "claude not available") {
			t.Errorf("without claude: %v", err)
		}
	}
	if err := c.Call(ipc.OpWatch, "not an object", nil); err == nil {
		t.Error("bad watch request accepted")
	}
}

func groupNamed(t *testing.T, c *ipc.Conn, name string) string {
	t.Helper()
	var g core.Group
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: name}, &g); err != nil {
		t.Fatal(err)
	}
	return g.ID
}

func TestMoveGroup(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	a, b, cc := groupNamed(t, c, "A"), groupNamed(t, c, "B"), groupNamed(t, c, "C")
	order := func() string {
		snap := snapshot(t, c)
		var names []string
		for _, r := range core.Layout(nil, snap.Groups, core.ViewState{Mode: "groups"}) {
			names = append(names, r.Group.Name)
		}
		return strings.Join(names, ",")
	}
	_ = a
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupMove, GroupID: cc})            // up
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupMove, GroupID: b, Flag: true}) // down
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupMove, GroupID: b, Flag: true}) // edge: no-op
	if got := order(); !strings.HasPrefix(got, "A,C,B") {
		t.Errorf("order %s", got)
	}
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupMove, GroupID: "default"}, nil); err == nil {
		t.Error("moved Default")
	}
}

type removes struct{ ids []string }

func (r *removes) Remove(_ context.Context, id string) error {
	r.ids = append(r.ids, id)
	return fmt.Errorf("already gone") // logged, not fatal
}

func TestPurge(t *testing.T) {
	p := testPaths(t)
	rm := &removes{}
	start(t, p, func(c *Config) { c.Remover, c.Stopper = rm, &stops{} })
	c := dial(t, p)
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"beef0001-x","source":"startup","cwd":"/w"}`, time.Now(), ipc.HookContext{}), nil))
	s := byClaude(snapshot(t, c), "beef0001-x")
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutPurge, IDs: []string{s.ID}}, nil); err == nil {
		t.Error("purged a live session")
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutArchive, ID: s.ID})
	var n int
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutPurge, IDs: []string{s.ID}}, &n))
	if n != 1 || len(snapshot(t, c).Sessions) != 0 || len(rm.ids) != 0 {
		t.Errorf("purge: n=%d removes=%v", n, rm.ids) // no BgID known (hook-only session): no claude rm
	}
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutPurge, IDs: []string{"nope"}}, nil); err == nil {
		t.Error("purged an unknown session")
	}
}

// A watch returns when something changes, or when its wait is up.
func TestWatch(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	var w ipc.WatchResponse
	must(t, c.Call(ipc.OpWatch, ipc.WatchRequest{}, &w))
	rev := w.Rev
	must(t, c.Call(ipc.OpWatch, ipc.WatchRequest{Rev: rev, WaitMS: 10}, &w))
	if w.Rev != rev {
		t.Errorf("nothing changed: %d, %d", rev, w.Rev)
	}
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"w1","source":"startup"}`, time.Now(), ipc.HookContext{}), nil))
	must(t, c.Call(ipc.OpWatch, ipc.WatchRequest{Rev: rev, WaitMS: 1000}, &w))
	if w.Rev == rev {
		t.Error("a change should move the revision")
	}
}

// Looking at a session clears its unseen mark.
func TestMarkSeen(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	t0 := time.Now()
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"s1","source":"startup"}`, t0, ipc.HookContext{}), nil))
	must(t, c.Call(ipc.OpEvent, hookEvent("PermissionRequest", `{"session_id":"s1","tool_name":"Bash"}`, t0.Add(time.Second), ipc.HookContext{}), nil))
	s := snapshot(t, c).Sessions[0]
	if !s.Unseen {
		t.Fatalf("not unseen: %+v", s)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutSeen, ID: s.ID})
	if s := snapshot(t, c).Sessions[0]; s.Unseen {
		t.Errorf("still unseen: %+v", s)
	}
}
