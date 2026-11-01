package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/store"
)

func openView(t *testing.T, c *ipc.Conn, sessionID string) *core.View {
	t.Helper()
	var v core.View
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutViewOpen, ID: sessionID, Pane: &core.PaneLoc{Terminal: "kitty", KittyWindow: 3}}, &v); err != nil {
		t.Fatal(err)
	}
	return &v
}

func newSess(t *testing.T, c *ipc.Conn, req ipc.NewRequest) *core.Session {
	t.Helper()
	var s core.Session
	if err := c.Call(ipc.OpNew, req, &s); err != nil {
		t.Fatal(err)
	}
	return &s
}

func TestWatchAndPrune(t *testing.T) {
	p := testPaths(t)
	start(t, p, func(c *Config) { c.Hosts = &fakeHosts{paths: p} })
	c := dial(t, p)
	v := openView(t, c, "")

	var w ipc.WatchResponse
	must(t, c.Call(ipc.OpWatch, ipc.WatchRequest{ViewID: v.ID}, &w))
	if w.View == nil || w.View.ID != v.ID || w.Gone {
		t.Fatalf("watch: %+v", w)
	}
	// Waiting returns on a change.
	got := make(chan ipc.WatchResponse)
	go func() {
		var r ipc.WatchResponse
		_ = dial(t, p).Call(ipc.OpWatch, ipc.WatchRequest{Rev: w.Rev, ViewID: v.ID, WaitMS: 5000}, &r)
		got <- r
	}()
	time.Sleep(50 * time.Millisecond)
	s := newSess(t, c, ipc.NewRequest{Cwd: "/w", ShowIn: v.ID})
	select {
	case r := <-got:
		if r.Rev == w.Rev || r.View.Selected != s.ID {
			t.Errorf("watch after change: %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watch didn't return on a change")
	}
	// Without a change it times out with the same revision.
	start := time.Now()
	var same ipc.WatchResponse
	must(t, c.Call(ipc.OpWatch, ipc.WatchRequest{Rev: w.Rev + 1, WaitMS: 100}, &same))
	if same.Rev != w.Rev+1 || time.Since(start) < 90*time.Millisecond {
		t.Errorf("idle watch: %+v after %v", same, time.Since(start))
	}

	// A closed view is gone for its loop.
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewClose, ID: v.ID})
	must(t, c.Call(ipc.OpWatch, ipc.WatchRequest{ViewID: v.ID}, &w))
	if !w.Gone {
		t.Errorf("closed view not gone: %+v", w)
	}
}

func TestPruneViews(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	must(t, err)
	t.Cleanup(func() { st.Close() })
	clock := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	d := &Daemon{cfg: Config{Now: func() time.Time { return clock }}, store: st, seen: seen{at: map[string]time.Time{}, start: clock}}
	ctx := context.Background()
	old, watched, fresh := &core.View{}, &core.View{}, &core.View{}
	for _, v := range []*core.View{old, watched, fresh} {
		must(t, st.PutView(ctx, v))
	}
	clock = clock.Add(time.Minute)
	d.seen.mark(watched.ID, clock)
	clock = clock.Add(time.Minute)
	d.seen.mark(fresh.ID, clock)
	n, err := d.pruneViews(ctx)
	must(t, err)
	views, _ := st.Views(ctx)
	if n != 1 || len(views) != 2 {
		t.Fatalf("pruned %d, %d left", n, len(views))
	}
	for _, v := range views {
		if v.ID == old.ID {
			t.Error("the view nobody watched for 2 minutes survived")
		}
	}
	// shown counts only live views.
	watched.Selected = "s1"
	must(t, st.PutView(ctx, watched))
	if !d.shown(ctx, "s1") {
		t.Error("watched view's session not shown")
	}
	clock = clock.Add(2 * time.Minute)
	if d.shown(ctx, "s1") {
		t.Error("a view that stopped watching still shows its session")
	}
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

func TestViewNotes(t *testing.T) {
	p := testPaths(t)
	start(t, p, func(c *Config) { c.Hosts = &fakeHosts{paths: p} })
	c := dial(t, p)
	v := openView(t, c, "")

	w := watchView(t, c, v.ID)
	if w.Note != nil {
		t.Fatalf("note before any: %+v", w.Note)
	}
	rev := w.Rev
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewNote, ID: v.ID, Text: "Could not resize", Flag: true})
	w = watchView(t, c, v.ID)
	if w.Rev == rev || w.Note == nil || w.Note.Text != "Could not resize" || !w.Note.Err {
		t.Errorf("note not delivered: rev %d→%d %+v", rev, w.Rev, w.Note)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewNote, ID: v.ID})
	w = watchView(t, c, v.ID)
	if w.Note != nil {
		t.Errorf("note not cleared: %+v", w.Note)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewNote, ID: v.ID, Text: "x"})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewClose, ID: v.ID})
	w = watchView(t, c, v.ID)
	if !w.Gone || w.Note != nil {
		t.Errorf("closed view: %+v", w)
	}
}

// watchView returns a fresh (non-waiting) watch response for view id.
func watchView(t *testing.T, c *ipc.Conn, id string) ipc.WatchResponse {
	t.Helper()
	var w ipc.WatchResponse
	must(t, c.Call(ipc.OpWatch, ipc.WatchRequest{ViewID: id}, &w))
	return w
}

// The reply popup's inbox: a finished turn, a permission prompt that
// replaces it, a prompt that drops it, skip, and order.
func TestInbox(t *testing.T) {
	p := testPaths(t)
	start(t, p, func(c *Config) { c.Hosts = &fakeHosts{paths: p} })
	c := dial(t, p)
	t0 := time.Now()
	ev := func(name, payload string, at time.Time) {
		t.Helper()
		must(t, c.Call(ipc.OpEvent, hookEvent(name, payload, at, ipc.HookContext{ViewID: "v"}), nil))
	}
	ev("SessionStart", `{"session_id":"aaaa0000-1","source":"startup","cwd":"/w"}`, t0)
	ev("SessionStart", `{"session_id":"bbbb0000-1","source":"startup","cwd":"/w"}`, t0)
	ev("UserPromptSubmit", `{"session_id":"aaaa0000-1","prompt":"do it"}`, t0.Add(time.Second))
	ev("Stop", `{"session_id":"aaaa0000-1","last_assistant_message":"All done.\n\nTests pass."}`, t0.Add(2*time.Second))
	ev("PermissionRequest", `{"session_id":"bbbb0000-1","tool_name":"Bash","tool_input":{"command":"make"}}`, t0.Add(3*time.Second))

	snap := snapshot(t, c)
	a, b := byClaude(snap, "aaaa0000-1"), byClaude(snap, "bbbb0000-1")
	if len(snap.Inbox) != 2 || snap.Inbox[0].SessionID != a.ID || snap.Inbox[0].Text != "All done.\n\nTests pass." ||
		snap.Inbox[1].SessionID != b.ID || snap.Inbox[1].Kind != core.InboxPermission {
		t.Fatalf("inbox: %+v", snap.Inbox)
	}
	// A newer item replaces the session's old one.
	ev("UserPromptSubmit", `{"session_id":"aaaa0000-1","prompt":"more"}`, t0.Add(4*time.Second))
	if in := snapshot(t, c).Inbox; len(in) != 1 || in[0].SessionID != b.ID {
		t.Errorf("after working in a: %+v", in)
	}
	ev("Stop", `{"session_id":"aaaa0000-1","last_assistant_message":"Second answer."}`, t0.Add(5*time.Second))
	// Answered elsewhere (the tool runs): b's prompt goes.
	ev("PreToolUse", `{"session_id":"bbbb0000-1","tool_name":"Bash"}`, t0.Add(6*time.Second))
	in := snapshot(t, c).Inbox
	if len(in) != 1 || in[0].SessionID != a.ID || in[0].Text != "Second answer." {
		t.Errorf("fresh: %+v", in)
	}
	// Stale events don't add anything.
	ev("Stop", `{"session_id":"bbbb0000-1","last_assistant_message":"old"}`, t0)
	mutate(t, c, ipc.Mutation{Kind: ipc.MutInboxSkip, ID: a.ID})
	if in := snapshot(t, c).Inbox; len(in) != 0 {
		t.Errorf("after skip: %+v", in)
	}
}

// A session in the user's own pane that continues in another window (or
// after the terminal restarted) gets its new pane from the next hook, so
// focusing it and moving it into acmd reach the right window.
func TestExternalPaneFollowsHooks(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	t0 := time.Now()
	old := ipc.HookContext{KittyWindowID: "5", ClaudePid: 100}
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"ext0000-1","source":"startup","cwd":"/w"}`, t0, old), nil))
	s := byClaude(snapshot(t, c), "ext0000-1")
	if s.Kind != core.KindExternal || s.External.KittyWindow != 5 || s.External.KittySocket != "" {
		t.Fatalf("start: %+v %+v", s, s.External)
	}
	// Resumed in window 4 of a Kitty with remote control.
	now := ipc.HookContext{KittyWindowID: "4", KittyListenOn: "unix:/tmp/kitty-1", ClaudePid: 200}
	must(t, c.Call(ipc.OpEvent, hookEvent("UserPromptSubmit", `{"session_id":"ext0000-1","prompt":"hi"}`, t0.Add(time.Second), now), nil))
	s = byClaude(snapshot(t, c), "ext0000-1")
	if s.External.KittyWindow != 4 || s.External.KittySocket != "unix:/tmp/kitty-1" || s.Pid != 200 {
		t.Errorf("after moving: %+v pid %d", s.External, s.Pid)
	}
	// A hook without the socket (same window) keeps the one known; one
	// from a view (ACMD_VIEW_ID) changes nothing.
	must(t, c.Call(ipc.OpEvent, hookEvent("Stop", `{"session_id":"ext0000-1"}`, t0.Add(2*time.Second), ipc.HookContext{KittyWindowID: "4"}), nil))
	must(t, c.Call(ipc.OpEvent, hookEvent("Notification", `{"session_id":"ext0000-1","notification_type":"idle_prompt"}`, t0.Add(3*time.Second), ipc.HookContext{ViewID: "v"}), nil))
	if s = byClaude(snapshot(t, c), "ext0000-1"); s.External.KittyWindow != 4 || s.External.KittySocket != "unix:/tmp/kitty-1" {
		t.Errorf("kept: %+v", s.External)
	}
}
