package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// setsDaemon starts a daemon that can start terminals, and dials it.
func setsDaemon(t *testing.T) *ipc.Conn {
	t.Helper()
	p := testPaths(t)
	fh := &fakeHosts{paths: p}
	start(t, p, func(c *Config) { c.Hosts = fh })
	return dial(t, p)
}

func terminal(t *testing.T, c *ipc.Conn, name, with string) *ipc.Session {
	t.Helper()
	return newSess(t, c, ipc.NewRequest{Cwd: "/w", Harness: core.HarnessTerminal, Name: name, SetWith: with})
}

// setOf describes the snapshot's sets as "name,name|name,name", members in
// order, as a client would draw them from each session's parent and order.
func setOf(t *testing.T, c *ipc.Conn) string {
	t.Helper()
	snap := snapshot(t, c)
	var out []string
	for _, set := range snap.Sets {
		if set.Type != ipc.EntitySet {
			t.Errorf("set %s has type %q", set.ID, set.Type)
		}
		names := map[int]string{}
		for _, s := range snap.Sessions {
			if s.Parent == set.ID {
				if s.GroupID != set.Parent {
					t.Errorf("%s is in group %s, its set in %s", s.Name, s.GroupID, set.Parent)
				}
				names[s.Order] = s.Name
			}
		}
		var members []string
		for i := 1; i <= len(names); i++ {
			members = append(members, names[i])
		}
		out = append(out, strings.Join(members, ","))
	}
	return strings.Join(out, "|")
}

func TestSplittingMakesASetInTheDaemon(t *testing.T) {
	c := setsDaemon(t)
	a := terminal(t, c, "a", "")
	b := terminal(t, c, "b", a.ID)
	if b.Parent == "" || b.Parent == b.GroupID || b.Order != 2 {
		t.Errorf("b after splitting from a: parent %q, order %d", b.Parent, b.Order)
	}
	if got := setOf(t, c); got != "a,b" {
		t.Errorf("sets = %q, want a,b", got)
	}
	// A split from a goes right after a, as its pane does.
	terminal(t, c, "c", a.ID)
	if got := setOf(t, c); got != "a,c,b" {
		t.Errorf("sets = %q, want a,c,b", got)
	}
	// A split from a session that's gone still starts the session, on its own.
	alone := terminal(t, c, "alone", "nope")
	if alone.Parent != alone.GroupID {
		t.Errorf("a failed join left it in %q", alone.Parent)
	}
	if got := setOf(t, c); got != "a,c,b" {
		t.Errorf("sets = %q after a failed join", got)
	}
}

func TestSetOps(t *testing.T) {
	c := setsDaemon(t)
	a, b, d := terminal(t, c, "a", ""), terminal(t, c, "b", ""), terminal(t, c, "d", "")

	var set ipc.Set
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutSetCreate, Targets: ipc.Here(a.ID, b.ID)}, &set))
	if set.Type != ipc.EntitySet || set.ID == "" || set.Parent != a.GroupID {
		t.Errorf("set.create returned %+v", set)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutSetAdd, SetID: set.ID, Targets: ipc.Here(d.ID), After: a.ID})
	if got := setOf(t, c); got != "a,d,b" {
		t.Errorf("after set.add: %q, want a,d,b", got)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutSetRemove, Targets: ipc.Here(d.ID)})
	if got := setOf(t, c); got != "a,b" {
		t.Errorf("after set.remove: %q, want a,b", got)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutSetEnd, SetID: set.ID})
	if got := setOf(t, c); got != "" {
		t.Errorf("after set.end: %q, want none", got)
	}
	// The rules come back as errors.
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutSetCreate, Targets: ipc.Here(a.ID)}, nil); err == nil {
		t.Error("set.create of one session succeeded")
	}
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutSetAdd, SetID: "nope", Targets: ipc.Here(a.ID)}, nil); err == nil {
		t.Error("set.add to a missing set succeeded")
	}
}

func TestArchivingTheSecondToLastMemberEndsItsSet(t *testing.T) {
	c := setsDaemon(t)
	a := terminal(t, c, "a", "")
	b := terminal(t, c, "b", a.ID)
	mutate(t, c, ipc.Mutation{Kind: ipc.MutArchive, Targets: ipc.Here(b.ID)})
	if got := setOf(t, c); got != "" {
		t.Errorf("after archiving b: %q, want no set", got)
	}
	for _, s := range snapshot(t, c).Sessions {
		if s.Parent != s.GroupID {
			t.Errorf("%s still names %q as its parent", s.Name, s.Parent)
		}
	}
}

func TestMovingOneMemberMovesItsSet(t *testing.T) {
	c := setsDaemon(t)
	a := terminal(t, c, "a", "")
	b := terminal(t, c, "b", a.ID)
	var g ipc.Group
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "api"}, &g))
	mutate(t, c, ipc.Mutation{Kind: ipc.MutMove, Targets: ipc.Here(b.ID), GroupID: g.ID})
	snap := snapshot(t, c)
	for _, s := range snap.Sessions {
		if s.GroupID != g.ID {
			t.Errorf("%s is in %s, want %s", s.Name, s.GroupID, g.ID)
		}
	}
	if len(snap.Sets) != 1 || snap.Sets[0].Parent != g.ID {
		t.Errorf("sets = %+v, want one in %s", snap.Sets, g.ID)
	}
}

func TestEachClientKeepsItsOwnLayout(t *testing.T) {
	c := setsDaemon(t)
	for _, l := range []ipc.Layout{{JSON: "old"}, {ClientID: "mac-1", JSON: "one"}, {ClientID: "mac-2", JSON: "two"}} {
		must(t, c.Call(ipc.OpSetLayout, l, nil))
	}
	for id, want := range map[string]string{"": "old", "mac-1": "one", "mac-2": "two", "new": ""} {
		var got ipc.Layout
		must(t, c.Call(ipc.OpLayout, ipc.Layout{ClientID: id}, &got))
		if got.JSON != want || got.ClientID != id {
			t.Errorf("layout of %q = %+v, want %q", id, got, want)
		}
	}
	// Asking with no data at all is the one layout from before.
	var got ipc.Layout
	must(t, c.Call(ipc.OpLayout, nil, &got))
	if got.JSON != "old" {
		t.Errorf("layout with no request = %q, want old", got.JSON)
	}
}

// A session that asks you something says so in the snapshot, with when it
// started asking: what "next that needs you" goes by.
func TestTheSnapshotSaysWhatNeedsYou(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	t0 := time.Now().UTC().Truncate(time.Millisecond)
	for _, e := range []ipc.HookEvent{
		hookEvent("SessionStart", `{"session_id":"c1","source":"startup","cwd":"/w"}`, t0, ipc.HookContext{}),
		hookEvent("PermissionRequest", `{"session_id":"c1","tool_name":"Bash"}`, t0.Add(time.Second), ipc.HookContext{}),
	} {
		must(t, c.Call(ipc.OpEvent, e, nil))
	}
	s := byClaude(snapshot(t, c), "c1")
	if s == nil || s.Attention == nil || s.Attention.Rank != 1 || s.Attention.Reason != core.NeedsWaiting || s.Attention.Since.IsZero() {
		t.Fatalf("waiting session: %+v", s)
	}
}
