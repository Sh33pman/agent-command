package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/claudecli"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/store"
)

// fakeClaude plays `claude --bg` and `claude --bg --resume`.
type fakeClaude struct {
	mu        sync.Mutex
	n         int
	untrusted map[string]bool
	starts    []claudecli.StartOptions
	resumes   []string
	delay     time.Duration
}

func (f *fakeClaude) StartBackground(_ context.Context, o claudecli.StartOptions) (string, error) {
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.untrusted[o.Dir] {
		return "", claudecli.ErrNotTrusted
	}
	f.n++
	f.starts = append(f.starts, o)
	return fmt.Sprintf("bg%06d", f.n), nil
}

func (f *fakeClaude) Resume(_ context.Context, _, sessionID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumes = append(f.resumes, sessionID)
	return sessionID[:8], nil
}

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

func TestNewSessionAndViews(t *testing.T) {
	p := testPaths(t)
	fc := &fakeClaude{untrusted: map[string]bool{"/untrusted": true}}
	start(t, p, func(c *Config) { c.Claude = fc })
	c := dial(t, p)

	// A view before any session shows the placeholder.
	empty := openView(t, c, "")
	if empty.ID == "" || empty.Selected != "" {
		t.Fatalf("empty view: %+v", empty)
	}

	g := groupNamed(t, c, "PROJ-1")
	s := newSess(t, c, ipc.NewRequest{Cwd: "/w/api", Name: "auth", Description: "d", GroupID: g, Args: []string{"--model", "opus"},
		Prompt: "go", ShowIn: empty.ID})
	if s.BgID != "bg000001" || s.Status != core.StatusStarting || s.Name != "auth" || s.NameSource != core.NameUser ||
		s.GroupID != g || s.Description != "d" || s.Cwd != "/w/api" {
		t.Errorf("new session: %+v", s)
	}
	if o := fc.starts[0]; o.Dir != "/w/api" || o.Name != "auth" || o.Prompt != "go" || strings.Join(o.Args, " ") != "--model opus" {
		t.Errorf("claude --bg options: %+v", o)
	}
	snap := snapshot(t, c)
	if len(snap.Views) != 1 || snap.Views[0].Selected != s.ID {
		t.Errorf("view didn't switch to the new session: %+v", snap.Views)
	}

	// Untrusted folder: a recognizable error, no row.
	err := c.Call(ipc.OpNew, ipc.NewRequest{Cwd: "/untrusted"}, nil)
	if err == nil || !strings.Contains(err.Error(), ipc.ErrNotTrustedText) {
		t.Errorf("untrusted: %v", err)
	}
	if err := c.Call(ipc.OpNew, ipc.NewRequest{}, nil); err == nil {
		t.Error("new without a folder accepted")
	}
	if n := len(snapshot(t, c).Sessions); n != 1 {
		t.Errorf("%d sessions after refused starts", n)
	}

	// The first hook links to the row by short id; the user's name stays.
	t0 := time.Now()
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"bg000001-aaaa","source":"startup","cwd":"/w/api","transcript_path":"/t"}`, t0, ipc.HookContext{}), nil))
	snap = snapshot(t, c)
	if len(snap.Sessions) != 1 || snap.Sessions[0].ClaudeSessionID != "bg000001-aaaa" || snap.Sessions[0].Name != "auth" ||
		snap.Sessions[0].Status != core.StatusDone {
		t.Errorf("hook not linked: %+v", snap.Sessions[0])
	}

	// A status change in the shown session isn't unseen; in another one it is.
	other := newSess(t, c, ipc.NewRequest{Cwd: "/w/web"})
	for _, e := range []ipc.HookEvent{
		hookEvent("UserPromptSubmit", `{"session_id":"bg000001-aaaa","prompt":"x"}`, t0.Add(time.Second), ipc.HookContext{}),
		hookEvent("Stop", `{"session_id":"bg000001-aaaa"}`, t0.Add(2*time.Second), ipc.HookContext{}),
		hookEvent("UserPromptSubmit", `{"session_id":"bg000002-bbbb","prompt":"y"}`, t0.Add(time.Second), ipc.HookContext{}),
		hookEvent("Stop", `{"session_id":"bg000002-bbbb"}`, t0.Add(2*time.Second), ipc.HookContext{}),
	} {
		must(t, c.Call(ipc.OpEvent, e, nil))
	}
	snap = snapshot(t, c)
	if byClaude(snap, "bg000001-aaaa").Unseen || !byClaude(snap, "bg000002-bbbb").Unseen {
		t.Errorf("unseen: shown=%v other=%v", byClaude(snap, "bg000001-aaaa").Unseen, byClaude(snap, "bg000002-bbbb").Unseen)
	}

	// Selecting it in a view marks it seen; a second view starts on the most recently active.
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewSelect, ID: empty.ID, Text: other.ID})
	if byClaude(snapshot(t, c), "bg000002-bbbb").Unseen {
		t.Error("selected session still unseen")
	}
	second := openView(t, c, "")
	if second.Selected == "" {
		t.Error("second view started on the placeholder")
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewSelect, ID: second.ID, Text: ""})

	// Views can't show archived or external sessions, or unknown ids.
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"ext-1","source":"startup","cwd":"/w"}`, t0,
		ipc.HookContext{KittyWindowID: "9", KittyListenOn: "unix:/k"}), nil))
	ext := byClaude(snapshot(t, c), "ext-1")
	for _, sel := range []string{ext.ID, "nope"} {
		if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutViewSelect, ID: second.ID, Text: sel}, nil); err == nil {
			t.Errorf("view showed %s", sel)
		}
	}
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutViewOpen}, nil); err == nil {
		t.Error("view without a pane accepted")
	}

	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewTouch, ID: second.ID})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewClose, ID: second.ID})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutViewClose, ID: second.ID}) // idempotent
	if n := len(snapshot(t, c).Views); n != 1 {
		t.Errorf("%d views after close", n)
	}
}

func TestNewSessionRacesItsFirstHook(t *testing.T) {
	p := testPaths(t)
	fc := &fakeClaude{delay: 100 * time.Millisecond}
	start(t, p, func(c *Config) { c.Claude = fc })
	c, hookConn := dial(t, p), dial(t, p)
	done := make(chan *core.Session)
	go func() {
		var s core.Session
		_ = c.Call(ipc.OpNew, ipc.NewRequest{Cwd: "/w", Name: "named"}, &s)
		done <- &s
	}()
	// The hook arrives while `claude --bg` is still "running".
	must(t, hookConn.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"bg000001-zzzz","source":"startup","cwd":"/w"}`, time.Now(), ipc.HookContext{}), nil))
	s := <-done
	snap := snapshot(t, c)
	if len(snap.Sessions) != 1 || s.ID != snap.Sessions[0].ID || snap.Sessions[0].Name != "named" || snap.Sessions[0].BgID != "bg000001" {
		t.Errorf("race made %d sessions: %+v", len(snap.Sessions), snap.Sessions)
	}
}

func TestWatchAndPrune(t *testing.T) {
	p := testPaths(t)
	start(t, p, func(c *Config) { c.Claude = &fakeClaude{} })
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

func TestResume(t *testing.T) {
	p := testPaths(t)
	fc := &fakeClaude{}
	start(t, p, func(c *Config) { c.Claude, c.Stopper = fc, &stops{} })
	c := dial(t, p)
	t0 := time.Now()
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"cafe0001-x","source":"startup","cwd":"/w"}`, t0, ipc.HookContext{}), nil))
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionEnd", `{"session_id":"cafe0001-x","reason":"other"}`, t0.Add(time.Second), ipc.HookContext{}), nil))
	s := byClaude(snapshot(t, c), "cafe0001-x")
	if s.Status != core.StatusPaused {
		t.Fatalf("not paused: %s", s.Status)
	}
	var r core.Session
	must(t, c.Call(ipc.OpResume, ipc.Mutation{ID: s.ID}, &r))
	if r.Status != core.StatusStarting || r.BgID != "cafe0001" || len(fc.resumes) != 1 || fc.resumes[0] != "cafe0001-x" {
		t.Errorf("resume: %+v %v", r, fc.resumes)
	}
	// Running sessions are left alone; archived and unknown ones are refused.
	must(t, c.Call(ipc.OpResume, ipc.Mutation{ID: s.ID}, &r))
	if len(fc.resumes) != 1 {
		t.Error("resumed a running session")
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutArchive, ID: s.ID})
	for _, id := range []string{s.ID, "nope"} {
		if err := c.Call(ipc.OpResume, ipc.Mutation{ID: id}, nil); err == nil {
			t.Errorf("resumed %s", id)
		}
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

func TestAdoptLinksTheFork(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	t0 := time.Now()
	ext := ipc.HookContext{KittyWindowID: "4", KittyListenOn: "unix:/k", TTY: "/dev/ttys004"}
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"old-1","source":"startup","cwd":"/w/api"}`, t0, ext), nil))
	s := byClaude(snapshot(t, c), "old-1")
	if s.Kind != core.KindExternal || s.External.TTY != "/dev/ttys004" {
		t.Fatalf("external: %+v %+v", s, s.External)
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutRename, ID: s.ID, Text: "mine"})
	mutate(t, c, ipc.Mutation{Kind: ipc.MutAdopt, ID: s.ID})
	// /bg: the old process ends, a forked background session starts.
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionEnd", `{"session_id":"old-1","reason":"prompt_input_exit"}`, t0.Add(time.Second), ext), nil))
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"abcd1234-new","source":"startup","cwd":"/w/api"}`, t0.Add(2*time.Second), ipc.HookContext{ClaudePid: 77}), nil))
	snap := snapshot(t, c)
	if len(snap.Sessions) != 1 {
		t.Fatalf("%d sessions", len(snap.Sessions))
	}
	got := snap.Sessions[0]
	if got.ID != s.ID || got.Kind != core.KindBackground || got.BgID != "abcd1234" || got.Name != "mine" || got.External != nil ||
		len(got.ClaudeHistory) != 1 || got.ClaudeHistory[0] != "old-1" || got.Pid != 77 {
		t.Errorf("not linked: %+v", got)
	}
	// Background sessions can't be adopted.
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutAdopt, ID: s.ID}, nil); err == nil {
		t.Error("adopted a background session")
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
	start(t, p, func(c *Config) { c.Claude = &fakeClaude{} })
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

// Resume returns only once Claude lists the session as running, so a view
// doesn't attach to a session that is still starting.
func TestResumeWaitsUntilRunning(t *testing.T) {
	oldReady, oldPoll := resumeReady, resumePoll
	resumeReady, resumePoll = 2*time.Second, 20*time.Millisecond
	t.Cleanup(func() { resumeReady, resumePoll = oldReady, oldPoll })

	p := testPaths(t)
	agents := &fakeAgents{}
	start(t, p, func(c *Config) { c.Claude, c.Agents, c.ReconcileEvery = &fakeClaude{}, agents, time.Hour })
	c := dial(t, p)
	t0 := time.Now()
	for _, id := range []string{"cafe0001-x", "dead0001-x"} {
		must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"`+id+`","source":"startup","cwd":"/w"}`, t0, ipc.HookContext{}), nil))
		must(t, c.Call(ipc.OpEvent, hookEvent("SessionEnd", `{"session_id":"`+id+`","reason":"other"}`, t0.Add(time.Second), ipc.HookContext{}), nil))
	}
	s := byClaude(snapshot(t, c), "cafe0001-x")

	pid := 42
	go func() {
		time.Sleep(300 * time.Millisecond)
		agents.add(claudecli.Agent{ID: "cafe0001", SessionID: "cafe0001-x", Kind: "background", Pid: &pid, Cwd: "/w"})
	}()
	began := time.Now()
	var r core.Session
	must(t, c.Call(ipc.OpResume, ipc.Mutation{ID: s.ID}, &r))
	if took := time.Since(began); took < 250*time.Millisecond || r.BgID != "cafe0001" {
		t.Errorf("returned after %v (before Claude ran it?): %+v", took, r)
	}

	// Never starts: a clear error, the bg id still recorded.
	d := byClaude(snapshot(t, c), "dead0001-x")
	err := c.Call(ipc.OpResume, ipc.Mutation{ID: d.ID}, nil)
	if err == nil || !strings.Contains(err.Error(), "hasn't started it") {
		t.Errorf("never running: %v", err)
	}
	if got := byClaude(snapshot(t, c), "dead0001-x"); got.BgID != "dead0001" {
		t.Errorf("bg id not recorded: %+v", got)
	}
}

// The reply popup's inbox: a finished turn, a permission prompt that
// replaces it, a prompt that drops it, skip, and order.
func TestInbox(t *testing.T) {
	p := testPaths(t)
	start(t, p, func(c *Config) { c.Claude = &fakeClaude{} })
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
