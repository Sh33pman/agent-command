package daemon

import (
	"context"
	"errors"
	"os"
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
	v := openView(t, c, "")

	s := newSess(t, c, ipc.NewRequest{Cwd: "/w/api", Name: "auth", Prompt: "go", Args: []string{"--model", "opus"}, ShowIn: v.ID})
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
	if snap := snapshot(t, c); len(snap.Views) != 1 || snap.Views[0].Selected != s.ID {
		t.Errorf("view didn't switch: %+v", snap.Views)
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
	hc := ipc.HookContext{ViewID: "v"}
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
