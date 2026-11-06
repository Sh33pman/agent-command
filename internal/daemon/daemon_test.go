package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/claudecli"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/store"
)

const testVersion = "9.9.9-test"

// testPaths uses a short temp dir: macOS limits socket paths to 104 bytes
// and t.TempDir() paths can exceed that.
func testPaths(t *testing.T) paths.Paths {
	t.Helper()
	dir, err := os.MkdirTemp("", "acmd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return paths.Paths{StateDir: filepath.Join(dir, "s"), RuntimeDir: filepath.Join(dir, "r")}
}

// running is a daemon started by a test. Done is closed when Run returns
// (so it can be waited on any number of times); Err is then set.
type running struct {
	cancel context.CancelFunc
	Done   chan struct{}
	Err    error
}

func start(t *testing.T, p paths.Paths, opts ...func(*Config)) *running {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{cancel: cancel, Done: make(chan struct{})}
	cfg := Config{Paths: p, Version: testVersion, Log: log.New(io.Discard, "", 0)}
	for _, o := range opts {
		o(&cfg)
	}
	go func() {
		r.Err = Run(ctx, cfg)
		close(r.Done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if c, err := ipc.Dial(context.Background(), p.Socket(), ipc.Hello{Proto: ipc.Proto, Version: testVersion}); err == nil {
			c.Close()
			break
		}
		select {
		case <-r.Done:
			t.Fatalf("daemon exited early: %v", r.Err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon didn't start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.Done:
		case <-time.After(5 * time.Second):
			t.Error("daemon didn't stop")
		}
	})
	return r
}

func dial(t *testing.T, p paths.Paths) *ipc.Conn {
	t.Helper()
	c, err := ipc.Dial(context.Background(), p.Socket(), ipc.Hello{Proto: ipc.Proto, Version: testVersion})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func hookEvent(name, payload string, at time.Time, hc ipc.HookContext) ipc.HookEvent {
	return ipc.HookEvent{Name: name, At: at, Payload: json.RawMessage(payload), Context: hc}
}

func TestDaemonAppliesHookEvents(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c := dial(t, p)
	if c.Daemon.Version != testVersion || c.Daemon.Pid == 0 {
		t.Errorf("hello: %+v", c.Daemon)
	}

	t0 := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	view := ipc.HookContext{ClaudePid: 4242}
	for i, e := range []ipc.HookEvent{
		hookEvent("SessionStart", `{"session_id":"c1","source":"startup","cwd":"/w"}`, t0, view),
		hookEvent("UserPromptSubmit", `{"session_id":"c1","prompt":"fix the flaky test"}`, t0.Add(time.Second), view),
		hookEvent("PermissionRequest", `{"session_id":"c1","tool_name":"AskUserQuestion"}`, t0.Add(2*time.Second), view),
	} {
		if err := c.Call(ipc.OpEvent, e, nil); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
	}
	var st ipc.Status
	if err := c.Call(ipc.OpStatus, nil, &st); err != nil {
		t.Fatal(err)
	}
	if st.Sessions != 1 || st.Events != 3 || st.Version != testVersion {
		t.Errorf("status = %+v", st)
	}

	// A bad payload is reported, not fatal.
	if err := c.Call(ipc.OpEvent, hookEvent("Stop", `{"no_session":1}`, t0, view), nil); err == nil {
		t.Error("expected error for payload without session_id")
	}
	if err := c.Call("bogus", nil, nil); err == nil {
		t.Error("expected error for unknown op")
	}
	if err := c.Call(ipc.OpPing, nil, nil); err != nil {
		t.Errorf("ping: %v", err)
	}
}

func TestDaemonPersistsStateAcrossRestarts(t *testing.T) {
	p := testPaths(t)
	d := start(t, p)
	c := dial(t, p)
	t0 := time.Now()
	ext := ipc.HookContext{ClaudePid: 99}
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"c1","source":"startup","cwd":"/w"}`, t0, ext), nil))
	must(t, c.Call(ipc.OpEvent, hookEvent("Stop", `{"session_id":"c1"}`, t0.Add(time.Second), ext), nil))
	c.Close()
	d.cancel()
	<-d.Done

	st, err := store.Open(context.Background(), p.DB())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s, err := st.SessionByClaudeID(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != core.StatusDone || !s.Unseen || s.Kind != core.KindBackground || s.Pid != 99 {
		t.Errorf("session = %+v", s)
	}
}

func TestDaemonDrainsSpoolOnStart(t *testing.T) {
	p := testPaths(t)
	must(t, p.Ensure())
	t0 := time.Now()
	lines := []any{
		hookEvent("SessionStart", `{"session_id":"s1","source":"startup"}`, t0, ipc.HookContext{}),
		"not an event",
		hookEvent("UserPromptSubmit", `{"session_id":"s1","prompt":"hi"}`, t0.Add(time.Second), ipc.HookContext{}),
	}
	f, err := os.Create(p.Spool())
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		b, _ := json.Marshal(l)
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	must(t, f.Close())

	start(t, p)
	var st ipc.Status
	must(t, dial(t, p).Call(ipc.OpStatus, nil, &st))
	if st.Sessions != 1 || st.Events != 2 {
		t.Errorf("after drain: %+v", st)
	}
	for _, f := range []string{p.Spool(), p.Spool() + ".draining"} {
		if _, err := os.Stat(f); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s should be gone: %v", filepath.Base(f), err)
		}
	}
}

func TestDaemonSingleInstanceAndShutdown(t *testing.T) {
	p := testPaths(t)
	d := start(t, p)

	if err := Run(context.Background(), Config{Paths: p, Version: testVersion, Log: log.New(io.Discard, "", 0)}); !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("second daemon: %v, want ErrAlreadyRunning", err)
	}

	st, err := os.Stat(p.Socket())
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("socket permissions %o, want 600", perm)
	}

	// A client of another version gets a mismatch and can shut the daemon down.
	c, err := ipc.Dial(context.Background(), p.Socket(), ipc.Hello{Proto: ipc.Proto, Version: "0.0.1"})
	var mm *ipc.ErrVersionMismatch
	if !errors.As(err, &mm) || mm.Daemon.Version != testVersion || c == nil {
		t.Fatalf("dial old version: %v", err)
	}
	must(t, c.Call(ipc.OpShutdown, nil, nil))
	c.Close()
	select {
	case <-d.Done:
		if d.Err != nil {
			t.Errorf("Run returned %v", d.Err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon didn't shut down")
	}
	if _, err := os.Stat(p.Socket()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket not removed: %v", err)
	}
}

func TestNewSessionClassification(t *testing.T) {
	s := sessionFromHook(core.Event{SessionID: "x", Cwd: "/w"}, ipc.HookContext{ClaudePid: 7}, time.Now())
	if s.Kind != core.KindBackground || s.External != nil || s.Pid != 7 {
		t.Errorf("session: %+v", s)
	}
}

// makeExternal turns a session into one of v1's external rows, which a database
// can still hold, though nothing creates them now.
func makeExternal(t *testing.T, p paths.Paths, claudeID string) {
	t.Helper()
	st, err := store.Open(context.Background(), p.DB())
	must(t, err)
	defer st.Close()
	s, err := st.SessionByClaudeID(context.Background(), claudeID)
	must(t, err)
	s.Kind = core.KindExternal
	s.External = &core.PaneLoc{Terminal: "kitty", KittyWindow: 2}
	must(t, st.UpdateSession(context.Background(), s))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// fakeAgents is read by the daemon's background loop while the test
// changes it, so access is locked and readers get a copy.
type fakeAgents struct {
	mu     sync.Mutex
	agents []claudecli.Agent
}

func (f *fakeAgents) Agents(context.Context) ([]claudecli.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]claudecli.Agent(nil), f.agents...), nil
}

func (f *fakeAgents) add(a claudecli.Agent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agents = append(f.agents, a)
}

func TestDaemonBackgroundLoops(t *testing.T) {
	p := testPaths(t)
	transcript := filepath.Join(p.StateDir, "t.jsonl")
	must(t, p.Ensure())
	must(t, os.WriteFile(transcript, []byte("{\"type\":\"user\"}\n"), 0o600))
	pid := 777
	agents := &fakeAgents{}
	start(t, p, func(c *Config) {
		c.Agents = agents
		c.ReconcileEvery = 50 * time.Millisecond
		c.TailEvery = 50 * time.Millisecond
	})
	c := dial(t, p)
	now := time.Now()
	view := ipc.HookContext{}
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"esc-1","source":"startup","transcript_path":"`+transcript+`"}`, now, view), nil))
	must(t, c.Call(ipc.OpEvent, hookEvent("PreToolUse", `{"session_id":"esc-1","tool_name":"Bash"}`, now.Add(time.Millisecond), view), nil))
	agents.add(claudecli.Agent{SessionID: "esc-1", ID: "eeeeeeee", Kind: "background", Status: ptr("busy"), Pid: &pid})

	// Let the tail record the transcript's size, then append the Esc marker.
	time.Sleep(200 * time.Millisecond)
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o600)
	must(t, err)
	_, err = f.WriteString(`{"type":"user","message":"[Request interrupted by user for tool use]"}` + "\n")
	must(t, err)
	must(t, f.Close())

	deadline := time.Now().Add(5 * time.Second)
	for {
		st, err := store.Open(context.Background(), p.DB())
		must(t, err)
		esc, err := st.SessionByClaudeID(context.Background(), "esc-1")
		st.Close()
		if err == nil && esc.Status == core.StatusDone {
			if esc.BgID != "eeeeeeee" || !esc.Unseen {
				t.Errorf("esc=%+v", esc)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("loops didn't converge: esc=%+v (%v)", esc, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func ptr[T any](v T) *T { return &v }

// A client that keeps its connection open without sending anything (a
// sidebar or the popup between requests) doesn't keep the daemon from
// stopping (it hung before: `acmd daemon restart` then failed).
func TestShutdownWithIdleClient(t *testing.T) {
	old := shutdownGrace
	shutdownGrace = 200 * time.Millisecond
	t.Cleanup(func() { shutdownGrace = old })
	p := testPaths(t)
	r := start(t, p)
	idle := dial(t, p) // connected, then silent
	defer idle.Close()
	c := dial(t, p)
	_ = c.Call(ipc.OpShutdown, nil, nil)
	select {
	case <-r.Done:
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon didn't stop while a client was connected")
	}
}

// A new daemon waits for one that's on its way out, but not for one that stays.
func TestLockWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock")
	unlock, err := lockWaiting(path, 0)
	must(t, err)
	if _, err := lockWaiting(path, 100*time.Millisecond); !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("held lock: %v", err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		unlock()
	}()
	next, err := lockWaiting(path, 5*time.Second)
	must(t, err)
	next()
}
