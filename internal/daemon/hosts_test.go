package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/claudecli"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/term"
)

// fakeHosts runs each host as an in-process term.Server around `sleep`.
type fakeHosts struct {
	paths paths.Paths
	fail  error
	mu    sync.Mutex
	reqs  []HostRequest
}

func (f *fakeHosts) Start(ctx context.Context, r HostRequest) error {
	f.mu.Lock()
	f.reqs = append(f.reqs, r)
	f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	srv := &term.Server{Socket: f.paths.HostSocket(r.ID), Shell: []string{"/bin/sleep", "30"}, Dir: "/", Env: []string{"PATH=/usr/bin:/bin"}}
	go func() { _ = srv.Serve(context.WithoutCancel(ctx)) }()
	return nil
}

func (f *fakeHosts) requests() []HostRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]HostRequest(nil), f.reqs...)
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func waitHost(t *testing.T, p paths.Paths, id string, running bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if term.Running(p.HostSocket(id)) == running {
			return
		}
	}
	t.Fatalf("host %s running=%v never became %v", id, !running, running)
}

func TestHostedSessionLifecycle(t *testing.T) {
	p := testPaths(t)
	fh := &fakeHosts{paths: p}
	start(t, p, func(c *Config) { c.Hosts = fh })
	c := dial(t, p)
	s := newSess(t, c, ipc.NewRequest{Cwd: "/w/api", Name: "auth", Prompt: "go", Args: []string{"--model", "opus"}})
	if s.Kind != core.KindHosted || s.Status != core.StatusStarting || s.Name != "auth" || !uuidRe.MatchString(s.ClaudeSessionID) || s.BgID != "" {
		t.Fatalf("new session: %+v", s)
	}
	r := fh.requests()
	if len(r) != 1 || r[0].ID != s.ID || r[0].Dir != "/w/api" || r[0].Prompt != "go" || r[0].ClaudeSession != s.ClaudeSessionID || r[0].Resume != "" {
		t.Errorf("host request: %+v", r)
	}
	if !term.Running(p.HostSocket(s.ID)) {
		t.Error("host not running")
	}

	// Its first hook finds the row by the id acmd chose.
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"`+s.ClaudeSessionID+`","source":"startup","cwd":"/w/api","transcript_path":"/t"}`, time.Now(), ipc.HookContext{}), nil))
	snap := snapshot(t, c)
	if len(snap.Sessions) != 1 || snap.Sessions[0].Status != core.StatusDone || snap.Sessions[0].Kind != core.KindHosted {
		t.Fatalf("hook not linked: %+v", snap.Sessions)
	}

	// Finishing hangs up the host and keeps the row.
	mutate(t, c, ipc.Mutation{Kind: ipc.MutFinish, ID: s.ID})
	waitHost(t, p, s.ID, false)
	if got := snapshot(t, c).Sessions[0]; got.Status != core.StatusFinished {
		t.Errorf("after finish: %v", got.Status)
	}

	// Showing a stopped hosted session resumes it in a new host.
	var rs core.Session
	must(t, c.Call(ipc.OpResume, ipc.Mutation{ID: s.ID}, &rs))
	waitHost(t, p, s.ID, true)
	r = fh.requests()
	if len(r) != 2 || r[1].Resume != s.ClaudeSessionID || r[1].Dir != "/w/api" || r[1].ClaudeSession != "" {
		t.Errorf("resume request: %+v", r)
	}
	if rs.Status != core.StatusStarting {
		t.Errorf("resumed status %v", rs.Status)
	}

	// Archiving hangs the host up too.
	mutate(t, c, ipc.Mutation{Kind: ipc.MutArchive, ID: s.ID})
	waitHost(t, p, s.ID, false)
}

func TestHostedStartFailureLeavesNoRow(t *testing.T) {
	p := testPaths(t)
	start(t, p, func(c *Config) { c.Hosts = &fakeHosts{paths: p, fail: errors.New("boom")} })
	c := dial(t, p)
	if err := c.Call(ipc.OpNew, ipc.NewRequest{Cwd: "/w"}, nil); err == nil {
		t.Fatal("expected an error")
	}
	if n := len(snapshot(t, c).Sessions); n != 0 {
		t.Errorf("%d sessions left behind", n)
	}
	if err := c.Call(ipc.OpNew, ipc.NewRequest{}, nil); err == nil {
		t.Error("new without a folder accepted")
	}
}

func TestNewUUID(t *testing.T) {
	a, b := newUUID(), newUUID()
	if !uuidRe.MatchString(a) || a == b {
		t.Errorf("uuids %q %q", a, b)
	}
}

// A daemon that restarts keeps hosted sessions whose host still runs, and
// pauses the ones whose host is gone; stale host files are swept.
func TestHostedSurvivesDaemonRestart(t *testing.T) {
	p := testPaths(t)
	fh := &fakeHosts{paths: p}
	r := start(t, p, func(c *Config) { c.Hosts = fh })
	c := dial(t, p)
	live := newSess(t, c, ipc.NewRequest{Cwd: "/w/a", Name: "live"})
	dead := newSess(t, c, ipc.NewRequest{Cwd: "/w/b", Name: "dead"})
	for _, s := range []*core.Session{live, dead} {
		must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"`+s.ClaudeSessionID+`","source":"startup","cwd":"`+s.Cwd+`"}`, time.Now(), ipc.HookContext{}), nil))
	}
	// A leftover from a host that is long gone.
	stale := p.HostIDFile("01STALESTALESTALESTALESTAL")
	must(t, os.WriteFile(stale, []byte("01STALESTALESTALESTALESTAL\n"), 0o600))

	r.cancel() // the daemon stops; the in-process "hosts" keep running
	<-r.Done
	term.Kill(p.HostSocket(dead.ID)) //nolint:errcheck // the host goes away while the daemon is down
	waitHost(t, p, dead.ID, false)

	start(t, p, func(c *Config) {
		c.Hosts, c.Agents, c.ReconcileEvery = fh, emptyAgents{}, 50*time.Millisecond
		c.Now = func() time.Time { return time.Now().Add(time.Hour) } // past the startup grace
	})
	c = dial(t, p)
	waitFor(t, "dead session paused", func() bool { return statusOf(snapshot(t, c), dead.ID) == core.StatusPaused })
	if got := statusOf(snapshot(t, c), live.ID); got != core.StatusDone {
		t.Errorf("live session status %v", got)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale host file not swept: %v", err)
	}
}

type emptyAgents struct{}

func (emptyAgents) Agents(context.Context) ([]claudecli.Agent, error) { return nil, nil }

func statusOf(snap ipc.Snapshot, id string) core.Status {
	for _, s := range snap.Sessions {
		if s.ID == id {
			return s.Status
		}
	}
	return ""
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A session from before hosting (`claude --bg`) becomes a hosted one when
// it is resumed: a running one is stopped first, a paused one just resumes.
func TestResumeLegacyBackground(t *testing.T) {
	p := testPaths(t)
	fh := &fakeHosts{paths: p}
	st := &stops{}
	start(t, p, func(c *Config) { c.Hosts, c.Stopper = fh, st })
	c := dial(t, p)
	hc := ipc.HookContext{}
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"cafe0001-x","source":"startup","cwd":"/w"}`, time.Now(), hc), nil))
	s := byClaude(snapshot(t, c), "cafe0001-x")
	if s.Kind != core.KindBackground {
		t.Fatalf("kind %v", s.Kind)
	}
	var r core.Session
	must(t, c.Call(ipc.OpResume, ipc.Mutation{ID: s.ID}, &r))
	reqs := fh.requests()
	if len(reqs) != 1 || reqs[0].Resume != "cafe0001-x" || reqs[0].Dir != "/w" {
		t.Errorf("host request: %+v", reqs)
	}
	if r.Kind != core.KindHosted || r.Status != core.StatusStarting || r.BgID != "" {
		t.Errorf("resumed: %+v", r)
	}
	// Running hosted sessions are left alone; archived and unknown ones are refused.
	must(t, c.Call(ipc.OpResume, ipc.Mutation{ID: s.ID}, &r))
	if len(fh.requests()) != 1 {
		t.Error("resumed a running session")
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutArchive, ID: s.ID})
	for _, id := range []string{s.ID, "nope"} {
		if err := c.Call(ipc.OpResume, ipc.Mutation{ID: id}, nil); err == nil {
			t.Errorf("resumed %s", id)
		}
	}
}

func TestTerminalSessionLifecycle(t *testing.T) {
	p := testPaths(t)
	fh := &fakeHosts{paths: p}
	start(t, p, func(c *Config) { c.Hosts = fh })
	c := dial(t, p)

	s := newSess(t, c, ipc.NewRequest{Cwd: "/w/api", Harness: core.HarnessTerminal})
	if !s.IsTerminal() || s.Status != core.StatusRunning || s.ClaudeSessionID != "" {
		t.Fatalf("new terminal: %+v", s)
	}
	r := fh.requests()
	if len(r) != 1 || r[0].Harness != core.HarnessTerminal || r[0].ClaudeSession != "" {
		t.Fatalf("host request: %+v", r)
	}
	if got := sessionByID(t, c, s.ID); got.Harness != core.HarnessTerminal || got.Status != core.StatusRunning {
		t.Fatalf("stored terminal: %+v", got)
	}

	// Archiving ends it for good.
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutArchive, ID: s.ID}, nil))
	if got := sessionByID(t, c, s.ID); got.Status != core.StatusArchived {
		t.Fatalf("archived: %+v", got)
	}
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutUnarchive, ID: s.ID}, nil); err == nil {
		t.Error("an archived terminal was unarchived")
	}
	if got := sessionByID(t, c, s.ID); got.Status != core.StatusArchived {
		t.Errorf("after refused unarchive: %+v", got)
	}
}

// Unarchiving an agent only lists it again: nothing starts until it's activated.
func TestUnarchiveDoesNotStartAnAgent(t *testing.T) {
	p := testPaths(t)
	fh := &fakeHosts{paths: p}
	start(t, p, func(c *Config) { c.Hosts = fh })
	c := dial(t, p)
	s := newSess(t, c, ipc.NewRequest{Cwd: "/w/api"})
	// Hosts start in the background: wait for the new session's, so it isn't
	// counted as one the unarchive started.
	waitFor(t, "the new session's host", func() bool { return len(fh.requests()) == 1 })
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutArchive, ID: s.ID}, nil))
	before := len(fh.requests())
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutUnarchive, ID: s.ID}, nil))
	if got := sessionByID(t, c, s.ID); got.Status != core.StatusPaused {
		t.Errorf("after unarchive: %+v", got)
	}
	if n := len(fh.requests()); n != before {
		t.Errorf("unarchive started %d hosts", n-before)
	}
	must(t, c.Call(ipc.OpResume, ipc.Mutation{ID: s.ID}, nil))
	waitFor(t, "the resumed session's host", func() bool { return len(fh.requests()) > before })
	r := fh.requests()
	if len(r) != before+1 || r[before].Resume != s.ClaudeSessionID {
		t.Errorf("resume after unarchive: %+v", r)
	}
}

func sessionByID(t *testing.T, c *ipc.Conn, id string) *core.Session {
	t.Helper()
	for _, s := range snapshot(t, c).Sessions {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("session %s not listed", id)
	return nil
}

type runningAgent struct{ id string }

func (f runningAgent) Agents(context.Context) ([]claudecli.Agent, error) {
	pid := 1
	status := "idle"
	return []claudecli.Agent{{SessionID: f.id, Kind: "interactive", Pid: &pid, Status: &status}}, nil
}

func writeConversation(t *testing.T, dir, id, cwd, title string) {
	t.Helper()
	folder := filepath.Join(dir, "projects", "p")
	must(t, os.MkdirAll(folder, 0o700))
	must(t, os.WriteFile(filepath.Join(folder, id+".jsonl"), []byte(
		`{"cwd":"`+cwd+`"}`+"\n"+`{"aiTitle":"`+title+`"}`+"\n"), 0o600))
}

func TestResumeConversation(t *testing.T) {
	const (
		idle = "11111111-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		live = "22222222-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	)
	p := testPaths(t)
	claude := t.TempDir()
	writeConversation(t, claude, idle, "/w/idle", "idle work")
	writeConversation(t, claude, live, "/w/live", "live work")
	fh := &fakeHosts{paths: p}
	start(t, p, func(c *Config) {
		c.Hosts, c.ClaudeDir, c.Agents = fh, claude, runningAgent{id: live}
	})
	c := dial(t, p)

	var list []ipc.Conversation
	must(t, c.Call(ipc.OpConversations, nil, &list))
	if len(list) != 2 {
		t.Fatalf("conversations: %+v", list)
	}
	byID := map[string]ipc.Conversation{}
	for _, item := range list {
		byID[item.ID] = item
	}
	if !byID[live].Running || byID[idle].Running || byID[idle].Title != "idle work" {
		t.Errorf("conversations: %+v", list)
	}

	// By id prefix: its folder and title come from the conversation.
	s := newSess(t, c, ipc.NewRequest{Resume: "11111111"})
	if s.ClaudeSessionID != idle || s.Cwd != "/w/idle" || s.Name != "idle work" || s.Kind != core.KindHosted || s.IsTerminal() {
		t.Fatalf("resumed: %+v", s)
	}
	r := fh.requests()
	if len(r) != 1 || r[0].Resume != idle || r[0].ClaudeSession != "" || r[0].Dir != "/w/idle" {
		t.Fatalf("host request: %+v", r)
	}

	// It's listed as already tracked, and resuming it again isn't a second session.
	must(t, c.Call(ipc.OpConversations, nil, &list))
	for _, item := range list {
		if item.ID == idle && item.SessionID != s.ID {
			t.Errorf("tracked: %+v", item)
		}
	}
	again := newSess(t, c, ipc.NewRequest{Resume: idle})
	if again.ID != s.ID || len(fh.requests()) != 1 {
		t.Errorf("resumed twice: %+v, %d hosts", again, len(fh.requests()))
	}

	// An archived one comes back rather than being duplicated.
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutArchive, ID: s.ID}, nil))
	back := newSess(t, c, ipc.NewRequest{Resume: idle})
	if back.ID != s.ID || back.Status == core.StatusArchived {
		t.Errorf("after archive: %+v", back)
	}

	if err := c.Call(ipc.OpNew, ipc.NewRequest{Resume: "99999999"}, nil); err == nil {
		t.Error("an unknown conversation was resumed")
	}
}

func TestLayoutIsKept(t *testing.T) {
	p := testPaths(t)
	r := start(t, p)
	c := dial(t, p)
	var got ipc.Layout
	must(t, c.Call(ipc.OpLayout, nil, &got))
	if got.JSON != "" {
		t.Errorf("initial layout = %q", got.JSON)
	}
	must(t, c.Call(ipc.OpSetLayout, ipc.Layout{JSON: `{"root":1}`}, nil))
	r.cancel()
	<-r.Done
	start(t, p)
	c = dial(t, p)
	must(t, c.Call(ipc.OpLayout, nil, &got))
	if got.JSON != `{"root":1}` {
		t.Errorf("layout after a restart = %q", got.JSON)
	}
}

// An agent typed into a terminal shows on that terminal's row, and leaves
// nothing behind when it exits.
func TestAgentInTerminalShowsOnItsRow(t *testing.T) {
	p := testPaths(t)
	fh := &fakeHosts{paths: p}
	start(t, p, func(c *Config) { c.Hosts = fh })
	c := dial(t, p)
	s := newSess(t, c, ipc.NewRequest{Cwd: "/w/api", Harness: core.HarnessTerminal})

	t0 := time.Now().Add(time.Hour)
	in := ipc.HookContext{HostSession: s.ID, ClaudePid: 4242}
	send := func(name, payload string, offset int) {
		t.Helper()
		must(t, c.Call(ipc.OpEvent, hookEvent(name, payload, t0.Add(time.Duration(offset)*time.Second), in), nil))
	}
	expect := func(when string, status core.Status, claudeID string) {
		t.Helper()
		got := sessionByID(t, c, s.ID)
		if got.Status != status || got.ClaudeSessionID != claudeID {
			t.Fatalf("%s: status %s, claude id %q", when, got.Status, got.ClaudeSessionID)
		}
	}

	send("SessionStart", `{"session_id":"a1","source":"startup","cwd":"/w/api"}`, 0)
	expect("started", core.StatusDone, "a1")
	send("UserPromptSubmit", `{"session_id":"a1","prompt":"fix the test"}`, 1)
	expect("prompt", core.StatusBusy, "a1")
	send("PermissionRequest", `{"session_id":"a1","tool_name":"Bash"}`, 2)
	expect("permission", core.StatusWaiting, "a1")
	send("Stop", `{"session_id":"a1"}`, 3)
	expect("stopped", core.StatusDone, "a1")

	// A nested agent's events don't touch the row, unless it starts over it.
	send("UserPromptSubmit", `{"session_id":"other","prompt":"x"}`, 4)
	expect("other agent", core.StatusDone, "a1")
	send("SessionEnd", `{"session_id":"other","reason":"other"}`, 5)
	expect("other agent ended", core.StatusDone, "a1")

	send("SessionEnd", `{"session_id":"a1","reason":"prompt_input_exit"}`, 6)
	expect("exited", core.StatusRunning, "")
	got := sessionByID(t, c, s.ID)
	if got.Name != "" || got.Pid != 0 || got.TranscriptPath != "" || len(got.ClaudeHistory) != 0 {
		t.Errorf("left behind: %+v", got)
	}
	// A late event from the agent that's gone doesn't bring it back.
	send("SessionEnd", `{"session_id":"a1","reason":"other"}`, 7)
	expect("late end", core.StatusRunning, "")

	// No separate session was created, and the conversation isn't the terminal's.
	var snap ipc.Snapshot
	must(t, c.Call(ipc.OpSnapshot, nil, &snap))
	if len(snap.Sessions) != 1 {
		t.Errorf("sessions = %d, want just the terminal", len(snap.Sessions))
	}

	// A later agent in the same terminal shows too.
	send("SessionStart", `{"session_id":"a2","source":"startup"}`, 8)
	expect("second agent", core.StatusDone, "a2")

	// An archived terminal stays archived.
	must(t, c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutArchive, ID: s.ID}, nil))
	send("SessionStart", `{"session_id":"a3","source":"startup"}`, 60)
	if got := sessionByID(t, c, s.ID); got.Status != core.StatusArchived {
		t.Errorf("archived terminal changed: %+v", got)
	}
}

// A hosted agent also has ACMD_SESSION_ID: it still goes by its Claude id.
func TestHostedAgentIsNotTreatedAsTerminal(t *testing.T) {
	p := testPaths(t)
	fh := &fakeHosts{paths: p}
	start(t, p, func(c *Config) { c.Hosts = fh })
	c := dial(t, p)
	s := newSess(t, c, ipc.NewRequest{Cwd: "/w/api"})
	in := ipc.HookContext{HostSession: s.ID}
	must(t, c.Call(ipc.OpEvent, hookEvent("UserPromptSubmit", `{"session_id":"`+s.ClaudeSessionID+`","prompt":"go"}`, time.Now().Add(time.Hour), in), nil))
	if got := sessionByID(t, c, s.ID); got.Status != core.StatusBusy {
		t.Errorf("hosted agent: %+v", got)
	}
}

// /clear changes Claude's session id, but not the session it runs in.
func TestStatusFollowsAHostedSessionThroughClear(t *testing.T) {
	p := testPaths(t)
	fh := &fakeHosts{paths: p}
	start(t, p, func(c *Config) { c.Hosts = fh })
	c := dial(t, p)
	s := newSess(t, c, ipc.NewRequest{Cwd: "/w/api"})
	first := s.ClaudeSessionID
	in := ipc.HookContext{HostSession: s.ID}
	t0 := time.Now().Add(time.Hour)
	send := func(name, payload string, offset int) {
		t.Helper()
		must(t, c.Call(ipc.OpEvent, hookEvent(name, payload, t0.Add(time.Duration(offset)*time.Second), in), nil))
	}
	send("UserPromptSubmit", `{"session_id":"`+first+`","prompt":"go"}`, 0)
	send("Stop", `{"session_id":"`+first+`"}`, 1)
	send("SessionEnd", `{"session_id":"`+first+`","reason":"clear"}`, 2)
	send("SessionStart", `{"session_id":"after-clear","source":"clear"}`, 3)
	send("UserPromptSubmit", `{"session_id":"after-clear","prompt":"more"}`, 4)
	got := sessionByID(t, c, s.ID)
	if got.Status != core.StatusBusy || got.ClaudeSessionID != "after-clear" {
		t.Fatalf("after /clear: status %s, id %q", got.Status, got.ClaudeSessionID)
	}
	if len(got.ClaudeHistory) != 1 || got.ClaudeHistory[0] != first {
		t.Errorf("history: %v", got.ClaudeHistory)
	}
	var snap ipc.Snapshot
	must(t, c.Call(ipc.OpSnapshot, nil, &snap))
	if len(snap.Sessions) != 1 {
		t.Errorf("sessions = %d, want one", len(snap.Sessions))
	}
	// Without a SessionStart, the next event still lands here and moves the id on.
	send("Stop", `{"session_id":"missed-start"}`, 5)
	if got := sessionByID(t, c, s.ID); got.Status != core.StatusDone || got.ClaudeSessionID != "missed-start" {
		t.Errorf("missed start: status %s, id %q", got.Status, got.ClaudeSessionID)
	}
	// A late event from an earlier conversation doesn't move it back.
	send("Stop", `{"session_id":"`+first+`"}`, 6)
	send("UserPromptSubmit", `{"session_id":"missed-start","prompt":"again"}`, 6)
	send("Stop", `{"session_id":"`+first+`"}`, 7)
	if got := sessionByID(t, c, s.ID); got.Status != core.StatusBusy || got.ClaudeSessionID != "missed-start" {
		t.Errorf("late event: status %s, id %q", got.Status, got.ClaudeSessionID)
	}
}

// A session started with ⌘N shows waiting while Claude asks for permission or
// asks a question, and error when a turn fails, and leaves each once Claude moves
// on (PAN-29). The payloads are the shapes Claude Code sends for each hook.
func TestHostedSessionShowsWaitingAndError(t *testing.T) {
	p := testPaths(t)
	fh := &fakeHosts{paths: p}
	start(t, p, func(c *Config) { c.Hosts = fh })
	c := dial(t, p)
	s := newSess(t, c, ipc.NewRequest{Cwd: "/w/api"})
	id := s.ClaudeSessionID
	in := ipc.HookContext{HostSession: s.ID}
	t0 := time.Now().Add(time.Hour)
	step := 0
	send := func(name, fields string) {
		t.Helper()
		step++
		payload := `{"session_id":"` + id + `"` + fields + `}`
		must(t, c.Call(ipc.OpEvent, hookEvent(name, payload, t0.Add(time.Duration(step)*time.Second), in), nil))
	}
	expect := func(when string, status core.Status, detail string) {
		t.Helper()
		got := sessionByID(t, c, s.ID)
		if got.Status != status || got.StatusDetail != detail {
			t.Errorf("%s: %s %q, want %s %q", when, got.Status, got.StatusDetail, status, detail)
		}
	}

	send("SessionStart", `,"source":"startup","cwd":"/w/api"`)
	send("UserPromptSubmit", `,"prompt":"run the tests"`)
	expect("prompt", core.StatusBusy, "")

	// Permission: the dialog fires PermissionRequest, then a Notification about it.
	send("PreToolUse", `,"tool_name":"Bash","tool_input":{"command":"go test ./..."}`)
	send("PermissionRequest", `,"tool_name":"Bash","tool_input":{"command":"go test ./..."}`)
	expect("permission asked", core.StatusWaiting, "permission: Bash")
	send("Notification", `,"notification_type":"permission_prompt","message":"Claude needs your permission to use Bash"`)
	send("Notification", `,"notification_type":"idle_prompt","message":"Claude is waiting for your input"`)
	// Claude's own words replace ours, and its idle reminder doesn't end waiting.
	expect("still waiting after the idle reminder", core.StatusWaiting, "Claude needs your permission to use Bash")
	send("PostToolUse", `,"tool_name":"Bash"`)
	expect("permission granted", core.StatusBusy, "")

	// A question.
	send("PreToolUse", `,"tool_name":"AskUserQuestion"`)
	send("PermissionRequest", `,"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which?","options":[{"label":"A"},{"label":"B"}]}]}`)
	expect("question", core.StatusWaiting, "question")
	send("PostToolUse", `,"tool_name":"AskUserQuestion"`)
	expect("question answered", core.StatusBusy, "")

	// An MCP server asking for input.
	send("Notification", `,"notification_type":"elicitation_dialog","message":"GitHub wants a repository name"`)
	expect("elicitation", core.StatusWaiting, "GitHub wants a repository name")
	send("PostToolUse", `,"tool_name":"mcp__github__create_issue"`)
	send("Stop", ``)
	expect("turn done", core.StatusDone, "")

	// A failed turn.
	send("UserPromptSubmit", `,"prompt":"and again"`)
	send("StopFailure", `,"error":"rate_limit","error_details":"429 Too Many Requests"`)
	expect("failed turn", core.StatusError, "rate_limit")
	send("Notification", `,"notification_type":"idle_prompt","message":"Claude is waiting for your input"`)
	expect("still an error after the idle reminder", core.StatusError, "rate_limit")
	send("UserPromptSubmit", `,"prompt":"try again"`)
	expect("trying again", core.StatusBusy, "")
	send("Stop", ``)
	expect("done after the retry", core.StatusDone, "")
}

// Denying a permission interrupts the turn, and no hook says so. The transcript
// does, so the session stops showing waiting, and its Inbox item closes.
func TestDenyingAPermissionEndsWaiting(t *testing.T) {
	p := testPaths(t)
	fh := &fakeHosts{paths: p}
	start(t, p, func(c *Config) { c.Hosts = fh; c.TailEvery = 20 * time.Millisecond })
	c := dial(t, p)
	s := newSess(t, c, ipc.NewRequest{Cwd: "/w/api"})
	transcript := filepath.Join(t.TempDir(), "t.jsonl")
	must(t, os.WriteFile(transcript, []byte(`{"type":"user","message":{"content":"run the tests"}}`+"\n"), 0o600))
	in := ipc.HookContext{HostSession: s.ID}
	t0 := time.Now()
	send := func(name, fields string, offset int) {
		t.Helper()
		payload := `{"session_id":"` + s.ClaudeSessionID + `","transcript_path":"` + transcript + `"` + fields + `}`
		must(t, c.Call(ipc.OpEvent, hookEvent(name, payload, t0.Add(time.Duration(offset)*time.Millisecond), in), nil))
	}
	send("UserPromptSubmit", `,"prompt":"run the tests"`, 0)
	send("PreToolUse", `,"tool_name":"Bash"`, 1)
	send("PermissionRequest", `,"tool_name":"Bash","tool_input":{"command":"rm -rf build"}`, 2)
	if got := sessionByID(t, c, s.ID); got.Status != core.StatusWaiting {
		t.Fatalf("status %s, want waiting", got.Status)
	}
	time.Sleep(100 * time.Millisecond) // the transcript check notes where the file ends

	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o600)
	must(t, err)
	_, err = f.WriteString(`{"type":"user","message":{"content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}` + "\n")
	must(t, err)
	must(t, f.Close())

	waitFor(t, "done after the denial", func() bool { return sessionByID(t, c, s.ID).Status == core.StatusDone })
	var items []core.InboxItem
	must(t, c.Call(ipc.OpInbox, ipc.InboxRequest{SessionID: s.ID}, &items))
	if len(items) != 1 || items[0].Kind != core.InboxPermission || items[0].State != core.InboxStale {
		t.Errorf("inbox: %+v, want the permission no longer relevant", items)
	}
}
