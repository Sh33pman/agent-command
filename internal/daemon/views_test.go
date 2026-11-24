package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

func newSess(t *testing.T, c *ipc.Conn, req ipc.NewRequest) *ipc.Session {
	t.Helper()
	var s ipc.Session
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
		func() error { return c.Call(ipc.OpResume, ipc.Mutation{Targets: ipc.Here("x")}, nil) },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "run `acmd doctor`") {
			t.Errorf("without claude: %v", err)
		}
	}
	if err := c.Call(ipc.OpWatch, "not an object", nil); err == nil {
		t.Error("bad watch request accepted")
	}
}

func groupNamed(t *testing.T, c *ipc.Conn, name string) string {
	t.Helper()
	var g ipc.Group
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
		for _, r := range core.Layout(nil, snap.CoreGroups(), core.ViewState{Mode: "groups"}) {
			names = append(names, r.Group.Name)
		}
		return strings.Join(names, ",")
	}
	// Dragging C above A, then B after C.
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupMove, GroupID: cc, Before: a})
	if got := order(); !strings.HasPrefix(got, "C,A,B") {
		t.Errorf("C before A: order %s", got)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupMove, GroupID: b, After: cc})
	if got := order(); !strings.HasPrefix(got, "C,B,A") {
		t.Errorf("B after C: order %s", got)
	}
	// The snapshot says the order you arranged, for clients to sort by.
	orders := map[string]int{}
	for _, g := range snapshot(t, c).Groups {
		orders[g.Name] = g.Order
	}
	if orders["C"] >= orders["B"] || orders["B"] >= orders["A"] {
		t.Errorf("sort keys %v", orders)
	}
	for name, m := range map[string]ipc.Mutation{
		"Default":               {Kind: ipc.MutGroupMove, GroupID: "default", After: a},
		"no neighbor":           {Kind: ipc.MutGroupMove, GroupID: a},
		"both neighbors":        {Kind: ipc.MutGroupMove, GroupID: a, After: b, Before: cc},
		"next to Default":       {Kind: ipc.MutGroupMove, GroupID: a, Before: "default"},
		"next to a missing one": {Kind: ipc.MutGroupMove, GroupID: a, After: "nope"}} {
		if err := c.Call(ipc.OpMutate, m, nil); err == nil {
			t.Errorf("moved a group with %s", name)
		}
	}
}

// Collapse is the CLI's view: the snapshot says which groups it has
// collapsed in the view, not on each group.
func TestCollapseIsInTheView(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	g := groupNamed(t, c, "A")
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupCollapse, GroupID: g, Flag: true})
	snap := snapshot(t, c)
	if !snap.ViewState.IsCollapsed(g) || len(snap.ViewState.Collapsed) != 1 {
		t.Errorf("view collapsed = %v, want [%s]", snap.ViewState.Collapsed, g)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupCollapse, GroupID: g, Flag: false})
	if got := snapshot(t, c).ViewState.Collapsed; len(got) != 0 {
		t.Errorf("after expanding: %v", got)
	}
}

func TestPurge(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	id := seedSession(t, p, &core.Session{ClaudeSessionID: "beef0001-x", Cwd: "/w"})
	// One session that can't be purged is an error, with its code.
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutPurge, Targets: ipc.Here(id)}, nil); codeOf(err) != ipc.CodeInvalid {
		t.Errorf("purged a live session: %v", err)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutArchive, Targets: ipc.Here(id)})
	var res ipc.Results
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutPurge, Targets: ipc.Here(id, "nope")}, &res))
	if len(res.Results) != 2 || !res.Results[0].OK || res.Results[1].Code != ipc.CodeNotFound || len(snapshot(t, c).Sessions) != 0 {
		t.Errorf("purge: %+v", res)
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
	seedHosted(t, p, "s1")
	c := dial(t, p)
	t0 := time.Now()
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"s1","source":"startup"}`, t0, ipc.HookContext{}), nil))
	must(t, c.Call(ipc.OpEvent, hookEvent("PermissionRequest", `{"session_id":"s1","tool_name":"Bash"}`, t0.Add(time.Second), ipc.HookContext{}), nil))
	s := snapshot(t, c).Sessions[0]
	if !s.Unseen {
		t.Fatalf("not unseen: %+v", s)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutSeen, Targets: ipc.Here(s.ID)})
	if s := snapshot(t, c).Sessions[0]; s.Unseen {
		t.Errorf("still unseen: %+v", s)
	}
}
