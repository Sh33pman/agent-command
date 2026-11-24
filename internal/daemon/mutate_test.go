package daemon

import (
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

func snapshot(t *testing.T, c *ipc.Conn) ipc.Snapshot {
	t.Helper()
	var s ipc.Snapshot
	must(t, c.Call(ipc.OpSnapshot, nil, &s))
	return s
}

func byClaude(snap ipc.Snapshot, cid string) *ipc.Session {
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
	start(t, p)
	c := dial(t, p)
	t0 := time.Now()
	seedHosted(t, p, "bg", "ext")
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"bg","source":"startup"}`, t0, ipc.HookContext{}), nil))
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"ext","source":"startup"}`, t0, ipc.HookContext{}), nil))
	snap := snapshot(t, c)
	bg, ex := byClaude(snap, "bg"), byClaude(snap, "ext")

	// Rename / describe.
	mutate(t, c, ipc.Mutation{Kind: ipc.MutRename, Targets: ipc.Here(bg.ID), Text: "  fix it  "})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutDescribe, Targets: ipc.Here(bg.ID), Text: "PROJ-9"})
	if s := byClaude(snapshot(t, c), "bg"); s.Name != "fix it" || s.NameSource != core.NameUser || s.Description != "PROJ-9" {
		t.Errorf("rename/describe: %+v", s)
	}
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRename, Targets: ipc.Here(bg.ID), Text: " "}, nil); err == nil {
		t.Error("empty name accepted")
	}
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRename, Targets: ipc.Here("missing"), Text: "x"}, nil); err == nil {
		t.Error("missing session accepted")
	}

	// Groups, move, collapse, view state, focus.
	var g ipc.Group
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "PROJ-1"}, &g))
	mutate(t, c, ipc.Mutation{Kind: ipc.MutMove, Targets: ipc.Here(bg.ID, ex.ID), GroupID: g.ID})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupRename, GroupID: g.ID, Text: "PROJ-1 auth"})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupColor, GroupID: g.ID, Color: "red"})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupCollapse, GroupID: g.ID, Flag: true})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewMode, Text: "list"})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewSort, Text: "status", Flag: true})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewFilter, Text: "g:proj"})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewArchived, Flag: true})
	snap = snapshot(t, c)
	var got *ipc.Group
	for _, x := range snap.Groups {
		if x.ID == g.ID {
			got = x
		}
	}
	if got.Name != "PROJ-1 auth" || got.Color != "red" || !snap.ViewState.IsCollapsed(got.ID) || byClaude(snap, "bg").GroupID != g.ID {
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
		if x.IsDefault && !snap.ViewState.IsCollapsed(x.ID) {
			t.Error("focus should collapse the other groups")
		}
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewFocus})
	if got := snapshot(t, c).ViewState.Collapsed; len(got) != 0 {
		t.Errorf("clear focus should restore every group, still collapsed: %v", got)
	}

	for _, bad := range []ipc.Mutation{
		{Kind: ipc.MutViewMode, Text: "tree"},
		{Kind: ipc.MutViewSort, Text: "size"},
		{Kind: ipc.MutViewFocus, GroupID: "nope"},
		{Kind: "nope"}} {
		if err := c.Call(ipc.OpMutate, bad, nil); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestArchiveRules(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	t0 := time.Now()
	seedHosted(t, p, "run", "grp")
	for _, cid := range []string{"run", "grp"} {
		must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"`+cid+`","source":"startup"}`, t0, ipc.HookContext{}), nil))
	}
	snap := snapshot(t, c)
	mutate(t, c, ipc.Mutation{Kind: ipc.MutArchive, Targets: ipc.Here(byClaude(snap, "run").ID)})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutArchive, Targets: ipc.Here(byClaude(snap, "run").ID)}) // idempotent
	if s := byClaude(snapshot(t, c), "run"); s.Status != core.StatusArchived || s.ArchivedAt.IsZero() {
		t.Errorf("run: %+v", s)
	}

	// Group archive / unarchive with sessions.
	var g ipc.Group
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "G"}, &g))
	mutate(t, c, ipc.Mutation{Kind: ipc.MutMove, Targets: ipc.Here(byClaude(snap, "grp").ID), GroupID: g.ID})
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
	mutate(t, c, ipc.Mutation{Kind: ipc.MutUnarchive, Targets: ipc.Here(byClaude(snap, "run").ID)})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutGroupDelete, GroupID: g.ID})
	if s := byClaude(snapshot(t, c), "grp"); s.GroupID != "default" {
		t.Errorf("group delete should move sessions to Default: %+v", s)
	}
}

// Finish stops a session's host and marks it finished; it stays finished
// when its Claude ends, and resuming it brings it back.
func TestFinish(t *testing.T) {
	sessionsHere(t)
	p := testPaths(t)
	start(t, p, func(c *Config) { c.Hosts = &fakeHosts{paths: p} })
	c := dial(t, p)
	seedHosted(t, p, "run00000-x")
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"run00000-x","source":"startup"}`, time.Now(), ipc.HookContext{}), nil))
	run := byClaude(snapshot(t, c), "run00000-x")

	mutate(t, c, ipc.Mutation{Kind: ipc.MutFinish, Targets: ipc.Here(run.ID)})
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionEnd", `{"session_id":"run00000-x","reason":"other"}`, time.Now(), ipc.HookContext{}), nil))
	if s := byClaude(snapshot(t, c), "run00000-x"); s.Status != core.StatusFinished {
		t.Errorf("after finish: %s", s.Status)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutFinish, Targets: ipc.Here(run.ID)}) // idempotent
	var r ipc.Session
	must(t, c.Call(ipc.OpResume, ipc.Mutation{Targets: ipc.Here(run.ID)}, &r))
	if r.Status != core.StatusStarting {
		t.Errorf("resume: %s", r.Status)
	}
}
