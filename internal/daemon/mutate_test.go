package daemon

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/claudecli"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

type stops struct {
	mu  sync.Mutex
	ids []string
	err error
}

func (s *stops) Stop(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids = append(s.ids, id)
	return s.err
}

func snapshot(t *testing.T, c *ipc.Conn) ipc.Snapshot {
	t.Helper()
	var s ipc.Snapshot
	must(t, c.Call(ipc.OpSnapshot, nil, &s))
	return s
}

func byClaude(snap ipc.Snapshot, cid string) *core.Session {
	for _, s := range snap.Sessions {
		if s.ClaudeSessionID == cid {
			return s
		}
	}
	return nil
}

func mutate(t *testing.T, c *ipc.Conn, m ipc.Mutation) {
	t.Helper()
	if err := c.Call(ipc.OpMutate, m, nil); err != nil {
		t.Fatalf("%s: %v", m.Kind, err)
	}
}

func TestMutations(t *testing.T) {
	p := testPaths(t)
	st := &stops{}
	start(t, p, func(c *Config) { c.Stopper = st })
	c := dial(t, p)
	t0 := time.Now()
	view := ipc.HookContext{ViewID: "v"}
	ext := ipc.HookContext{KittyWindowID: "2", TermProgram: "kitty"}
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"bg","source":"startup"}`, t0, view), nil))
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"ext","source":"startup"}`, t0, ext), nil))
	snap := snapshot(t, c)
	bg, ex := byClaude(snap, "bg"), byClaude(snap, "ext")

	// Rename / describe.
	mutate(t, c, ipc.Mutation{Kind: ipc.MutRename, ID: bg.ID, Text: "  fix it  "})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutDescribe, ID: bg.ID, Text: "PROJ-9"})
	if s := byClaude(snapshot(t, c), "bg"); s.Name != "fix it" || s.NameSource != core.NameUser || s.Description != "PROJ-9" {
		t.Errorf("rename/describe: %+v", s)
	}
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRename, ID: bg.ID, Text: " "}, nil); err == nil {
		t.Error("empty name accepted")
	}
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRename, ID: "missing", Text: "x"}, nil); err == nil {
		t.Error("missing session accepted")
	}

	// Groups, move, collapse, view state, focus.
	var g core.Group
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "PROJ-1"}, &g))
	mutate(t, c, ipc.Mutation{Kind: ipc.MutMove, IDs: []string{bg.ID, ex.ID}, GroupID: g.ID})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupRename, GroupID: g.ID, Text: "PROJ-1 auth"})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupColor, GroupID: g.ID, Color: "red"})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupCollapse, GroupID: g.ID, Flag: true})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewMode, Text: "list"})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewSort, Text: "status", Flag: true})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewFilter, Text: "g:proj"})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewArchived, Flag: true})
	snap = snapshot(t, c)
	var got *core.Group
	for _, x := range snap.Groups {
		if x.ID == g.ID {
			got = x
		}
	}
	if got.Name != "PROJ-1 auth" || got.Color != "red" || !got.Collapsed || byClaude(snap, "bg").GroupID != g.ID {
		t.Errorf("group state: %+v", got)
	}
	vs := snap.ViewState
	if vs.Mode != "list" || vs.Sort != "status" || !vs.Desc || vs.Filter != "g:proj" || !vs.ShowArchived {
		t.Errorf("view state: %+v", vs)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupCollapse, Flag: false}) // all
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewFocus, GroupID: g.ID})
	snap = snapshot(t, c)
	if snap.ViewState.FocusedGroup != g.ID || snap.ViewState.Mode != "groups" {
		t.Errorf("focus: %+v", snap.ViewState)
	}
	for _, x := range snap.Groups {
		if x.IsDefault && !x.Collapsed {
			t.Error("focus should collapse the other groups")
		}
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewFocus})
	for _, x := range snapshot(t, c).Groups {
		if x.Collapsed {
			t.Errorf("clear focus should restore %s", x.Name)
		}
	}

	for _, bad := range []ipc.Mutation{
		{Kind: ipc.MutViewMode, Text: "tree"},
		{Kind: ipc.MutViewSort, Text: "size"},
		{Kind: ipc.MutViewFocus, GroupID: "nope"},
		{Kind: "nope"},
	} {
		if err := c.Call(ipc.OpMutate, bad, nil); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestArchiveRules(t *testing.T) {
	p := testPaths(t)
	st := &stops{}
	pid := 9
	agents := &fakeAgents{}
	start(t, p, func(c *Config) {
		c.Stopper = st
		c.Agents = agents
		c.ReconcileEvery = 30 * time.Millisecond
	})
	c := dial(t, p)
	t0 := time.Now()
	view := ipc.HookContext{ViewID: "v"}
	for _, cid := range []string{"run", "grp"} {
		must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"`+cid+`","source":"startup"}`, t0, view), nil))
	}
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"ext","source":"startup"}`, t0, ipc.HookContext{TermProgram: "ghostty"}), nil))
	idle := "idle"
	agents.add(bgAgent("run", "rrrrrrrr", &idle, &pid))
	agents.add(bgAgent("grp", "gggggggg", &idle, &pid))
	deadline := time.Now().Add(5 * time.Second)
	for byClaude(snapshot(t, c), "grp").BgID == "" {
		if time.Now().After(deadline) {
			t.Fatal("reconciler didn't set short ids")
		}
		time.Sleep(20 * time.Millisecond)
	}
	snap := snapshot(t, c)

	// External: archived without any stop call (never killed).
	mutate(t, c, ipc.Mutation{Kind: ipc.MutArchive, ID: byClaude(snap, "ext").ID})
	// Running background: claude stop, then archived.
	mutate(t, c, ipc.Mutation{Kind: ipc.MutArchive, ID: byClaude(snap, "run").ID})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutArchive, ID: byClaude(snap, "run").ID}) // idempotent
	if strings.Join(st.ids, ",") != "rrrrrrrr" {
		t.Errorf("stop calls = %v", st.ids)
	}
	snap = snapshot(t, c)
	for _, cid := range []string{"ext", "run"} {
		if s := byClaude(snap, cid); s.Status != core.StatusArchived || s.ArchivedAt.IsZero() {
			t.Errorf("%s: %+v", cid, s)
		}
	}

	// A failing stop leaves the session unarchived.
	st.err = context.DeadlineExceeded
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutArchive, ID: byClaude(snap, "grp").ID}, nil); err == nil {
		t.Error("archive should fail when claude stop fails")
	}
	if s := byClaude(snapshot(t, c), "grp"); s.Status == core.StatusArchived {
		t.Error("session archived although stop failed")
	}
	st.err = nil

	// Group archive / unarchive with sessions.
	var g core.Group
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "G"}, &g))
	mutate(t, c, ipc.Mutation{Kind: ipc.MutMove, IDs: []string{byClaude(snap, "grp").ID}, GroupID: g.ID})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupArchive, GroupID: g.ID})
	if s := byClaude(snapshot(t, c), "grp"); s.Status != core.StatusArchived {
		t.Errorf("group archive: %+v", s)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupUnarchive, GroupID: g.ID}) // group only
	if s := byClaude(snapshot(t, c), "grp"); s.Status != core.StatusArchived {
		t.Error("unarchive without --sessions must leave sessions archived")
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupArchive, GroupID: g.ID})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupUnarchive, GroupID: g.ID, Flag: true})
	if s := byClaude(snapshot(t, c), "grp"); s.Status != core.StatusPaused {
		t.Errorf("unarchive --sessions: %+v", s)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutUnarchive, ID: byClaude(snap, "ext").ID})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupDelete, GroupID: g.ID})
	if s := byClaude(snapshot(t, c), "grp"); s.GroupID != "default" {
		t.Errorf("group delete should move sessions to Default: %+v", s)
	}
}

func TestArchiveWithoutClaude(t *testing.T) {
	p := testPaths(t)
	start(t, p) // no Stopper
	c := dial(t, p)
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"bg","source":"startup"}`, time.Now(), ipc.HookContext{ViewID: "v"}), nil))
	// Without a short id there is nothing to stop: archiving works.
	mutate(t, c, ipc.Mutation{Kind: ipc.MutArchive, ID: byClaude(snapshot(t, c), "bg").ID})
}

func bgAgent(sid, id string, status *string, pid *int) claudecli.Agent {
	return claudecli.Agent{SessionID: sid, ID: id, Kind: "background", Status: status, Pid: pid}
}

// Finish stops a running background session and marks it finished; it
// stays finished when Claude reports it stopped, a view showing it goes
// back to its list, and resuming it brings it back.
func TestFinish(t *testing.T) {
	p := testPaths(t)
	st := &stops{}
	pid := 9
	agents := &fakeAgents{}
	start(t, p, func(c *Config) {
		c.Stopper, c.Agents, c.Claude = st, agents, &fakeClaude{}
		c.ReconcileEvery = 30 * time.Millisecond
	})
	c := dial(t, p)
	t0 := time.Now()
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"run00000-x","source":"startup"}`, t0, ipc.HookContext{ViewID: "v"}), nil))
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"ext","source":"startup"}`, t0, ipc.HookContext{TermProgram: "ghostty"}), nil))
	idle := "idle"
	agents.add(bgAgent("run00000-x", "rrrrrrrr", &idle, &pid))
	deadline := time.Now().Add(5 * time.Second)
	for byClaude(snapshot(t, c), "run00000-x").BgID == "" {
		if time.Now().After(deadline) {
			t.Fatal("reconciler didn't set the short id")
		}
		time.Sleep(20 * time.Millisecond)
	}
	run := byClaude(snapshot(t, c), "run00000-x")
	v := openView(t, c, run.ID)

	mutate(t, c, ipc.Mutation{Kind: ipc.MutFinish, ID: run.ID})
	if strings.Join(st.ids, ",") != "rrrrrrrr" {
		t.Errorf("stop calls = %v", st.ids)
	}
	// Claude now lists it as stopped: it stays finished.
	agents.mu.Lock()
	agents.agents = []claudecli.Agent{bgAgent("run00000-x", "rrrrrrrr", nil, nil)}
	agents.mu.Unlock()
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionEnd", `{"session_id":"run00000-x","reason":"other"}`, time.Now(), ipc.HookContext{ViewID: "v"}), nil))
	time.Sleep(150 * time.Millisecond)
	snap := snapshot(t, c)
	if s := byClaude(snap, "run00000-x"); s.Status != core.StatusFinished {
		t.Errorf("after finish: %s", s.Status)
	}
	for _, x := range snap.Views {
		if x.ID == v.ID && x.Selected != "" {
			t.Errorf("view still shows it: %q", x.Selected)
		}
	}
	// External sessions can't be finished; resuming brings it back.
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutFinish, ID: byClaude(snap, "ext").ID}, nil); err == nil {
		t.Error("finished an external session")
	}
	agents.add(bgAgent("run00000-x", "run00000", &idle, &pid)) // Claude runs it again
	var r core.Session
	must(t, c.Call(ipc.OpResume, ipc.Mutation{ID: run.ID}, &r))
	if r.Status != core.StatusStarting {
		t.Errorf("resume: %s", r.Status)
	}
}
