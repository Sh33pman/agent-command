package daemon

import (
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/term"
)

// fakeFront is what's in front of every terminal, as its host would say.
type fakeFront struct {
	mu sync.Mutex
	f  term.Front
	ok bool
}

func (f *fakeFront) set(front term.Front) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.f, f.ok = front, true
}

func (f *fakeFront) get(string) (term.Front, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.f, f.ok
}

func TestTerminalFallsBackToWhatsInFront(t *testing.T) {
	old := unknownAfter
	unknownAfter = 150 * time.Millisecond
	t.Cleanup(func() { unknownAfter = old })
	p := testPaths(t)
	fh, ff := &fakeHosts{paths: p}, &fakeFront{}
	start(t, p, func(c *Config) { c.Hosts = fh; c.FrontEvery = 20 * time.Millisecond; c.HostFront = ff.get })
	c := dial(t, p)
	s := newSess(t, c, ipc.NewRequest{Cwd: "/w/api", Harness: core.HarnessTerminal})
	status := func() core.Status { return sessionByID(t, c, s.ID).Status }

	// An editor in front is still just the terminal.
	ff.set(term.Front{Name: "vim"})
	time.Sleep(200 * time.Millisecond)
	if got := status(); got != core.StatusRunning {
		t.Fatalf("vim: %s, want running", got)
	}

	// An agent with no hooks: running for a moment, in case its first hook is on the way, then unknown.
	ff.set(term.Front{Name: "opencode"})
	time.Sleep(60 * time.Millisecond)
	if got := status(); got != core.StatusRunning {
		t.Fatalf("straight after the agent started: %s, want running", got)
	}
	waitFor(t, "unknown", func() bool { return status() == core.StatusUnknown })
	if got := sessionByID(t, c, s.ID); got.StatusDetail != "opencode" {
		t.Errorf("detail %q, want the agent's name", got.StatusDetail)
	}

	// It exits: the terminal again.
	ff.set(term.Front{Shell: true})
	waitFor(t, "running after the agent exits", func() bool { return status() == core.StatusRunning })

	// An agent with hooks keeps its own status, and when it's killed, with no
	// SessionEnd, the shell back in front ends it and its Inbox item.
	ff.set(term.Front{Name: "claude"})
	in := ipc.HookContext{HostSession: s.ID, ClaudePid: 4242}
	t0 := time.Now()
	must(t, c.Call(ipc.OpEvent, hookEvent("SessionStart", `{"session_id":"a1","source":"startup"}`, t0, in), nil))
	must(t, c.Call(ipc.OpEvent, hookEvent("UserPromptSubmit", `{"session_id":"a1","prompt":"x"}`, t0.Add(time.Millisecond), in), nil))
	must(t, c.Call(ipc.OpEvent, hookEvent("PermissionRequest", `{"session_id":"a1","tool_name":"Bash"}`, t0.Add(2*time.Millisecond), in), nil))
	time.Sleep(250 * time.Millisecond)
	if got := status(); got != core.StatusWaiting {
		t.Fatalf("claude with hooks: %s, want waiting", got)
	}
	ff.set(term.Front{Shell: true})
	waitFor(t, "running after the agent is killed", func() bool { return status() == core.StatusRunning })
	if got := sessionByID(t, c, s.ID); got.ClaudeSessionID != "" {
		t.Errorf("the gone agent's id stayed: %q", got.ClaudeSessionID)
	}
	var items []core.InboxItem
	must(t, c.Call(ipc.OpInbox, ipc.InboxRequest{SessionID: s.ID}, &items))
	if len(items) != 1 || items[0].State != core.InboxStale {
		t.Errorf("inbox: %+v, want the permission no longer relevant", items)
	}
}
