package daemon

import (
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/term"
)

// fakeAgent is the OSC 7501 report every host holds.
type fakeAgent struct {
	mu sync.Mutex
	st term.AgentState
}

func (f *fakeAgent) report(state, kind, msg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.st = term.AgentState{State: state, Kind: kind, Msg: msg, Seq: f.st.Seq + 1, At: time.Now()}
}

func (f *fakeAgent) get(string) (term.AgentState, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st, true
}

// agy has no hook for waiting on you; what it reports on the terminal fills
// that in: blocked is waiting with its message, working is busy, failed an
// error, done the end of the turn.
func TestAntigravityStatusFromItsReports(t *testing.T) {
	p := testPaths(t)
	fh, fa := &fakeHosts{paths: p}, &fakeAgent{}
	start(t, p, func(c *Config) { c.Hosts = fh; c.FrontEvery = 20 * time.Millisecond; c.HostAgent = fa.get })
	c := dial(t, p)
	s := newSess(t, c, ipc.NewRequest{Cwd: "/w/api", Harness: core.HarnessAntigravity})
	get := func() *core.Session { return sessionByID(t, c, s.ID) }

	fa.report("working", "", "")
	waitFor(t, "busy", func() bool { return get().Status == core.StatusBusy })

	fa.report("blocked", "permission", "Allow `npm test`?")
	waitFor(t, "waiting", func() bool { return get().Status == core.StatusWaiting })
	if got := get().StatusDetail; got != "Allow `npm test`?" {
		t.Errorf("waiting detail %q", got)
	}

	fa.report("working", "", "")
	waitFor(t, "busy again once answered", func() bool { return get().Status == core.StatusBusy })

	fa.report("failed", "", "quota exceeded")
	waitFor(t, "error", func() bool { return get().Status == core.StatusError })
	if got := get().StatusDetail; got != "quota exceeded" {
		t.Errorf("error detail %q", got)
	}

	fa.report("working", "", "")
	waitFor(t, "busy", func() bool { return get().Status == core.StatusBusy })
	fa.report("done", "", "")
	waitFor(t, "done", func() bool { return get().Status == core.StatusDone })
}

func TestAgentStateEvent(t *testing.T) {
	at := time.Now()
	for _, c := range []struct {
		st   term.AgentState
		kind core.EventKind
		text string
	}{
		{term.AgentState{State: "working"}, core.HookPreToolUse, ""},
		{term.AgentState{State: "blocked", Kind: "question", Msg: "Which one?"}, core.HookNotification, "Which one?"},
		{term.AgentState{State: "blocked", Kind: "plan"}, core.HookNotification, "plan to review"},
		{term.AgentState{State: "blocked", Kind: "auth"}, core.HookNotification, "sign in"},
		{term.AgentState{State: "blocked"}, core.HookNotification, "waiting for you"},
		{term.AgentState{State: "idle"}, core.HookStop, ""},
		{term.AgentState{State: "done"}, core.HookStop, ""},
		{term.AgentState{State: "failed"}, core.HookStopFailure, "an error"},
	} {
		c.st.At = at
		e, ok := agentStateEvent(c.st)
		if !ok || e.Kind != c.kind || e.Time != at || e.Agent != "Antigravity" {
			t.Errorf("%+v: %+v %v", c.st, e, ok)
			continue
		}
		if got := e.Message + e.Error; got != c.text {
			t.Errorf("%+v: text %q, want %q", c.st, got, c.text)
		}
	}
	if _, ok := agentStateEvent(term.AgentState{State: "sleeping"}); ok {
		t.Error("an unknown state made an event")
	}
}

// agy's done report carries the reply. Its Stop hook has usually ended the turn
// already, with no text, yet the reply still becomes the Inbox's done item.
func TestAntigravityReplyReachesTheInbox(t *testing.T) {
	p := testPaths(t)
	fh, fa := &fakeHosts{paths: p}, &fakeAgent{}
	start(t, p, func(c *Config) { c.Hosts = fh; c.FrontEvery = 20 * time.Millisecond; c.HostAgent = fa.get })
	c := dial(t, p)
	s := newSess(t, c, ipc.NewRequest{Cwd: "/w/api", Harness: core.HarnessAntigravity})
	in := ipc.HookContext{HostSession: s.ID}
	t0 := time.Now()
	must(t, c.Call(ipc.OpEvent, hookEvent("antigravity:PreInvocation", `{"conversationId":"c1","lastUserInput":"say ping"}`, t0, in), nil))
	must(t, c.Call(ipc.OpEvent, hookEvent("antigravity:Stop", `{"conversationId":"c1","terminationReason":"NO_TOOL_CALL"}`, t0.Add(time.Millisecond), in), nil))
	waitFor(t, "done from the hook", func() bool { return sessionByID(t, c, s.ID).Status == core.StatusDone })

	fa.report("done", "", "ping")
	waitFor(t, "the reply in the Inbox", func() bool {
		for _, it := range snapshot(t, c).Inbox {
			if it.SessionID == s.ID && it.Kind == core.InboxDone && it.Text == "ping" {
				return true
			}
		}
		return false
	})
}

// agy waiting on a permission makes an Inbox item with the answers its screen
// shows; picking one types its number, as agy takes it, and agy working again
// closes the item.
func TestAntigravityPermissionInTheInbox(t *testing.T) {
	p := testPaths(t)
	fh, fa, typed := &fakeHosts{paths: p}, &fakeAgent{}, &sent{}
	var mu sync.Mutex
	screen, asked := "", 0
	start(t, p, func(c *Config) {
		c.Hosts, c.FrontEvery, c.HostAgent, c.SendToHost = fh, 20*time.Millisecond, fa.get, typed.send
		c.HostScreen = func(string) (string, bool) {
			mu.Lock()
			defer mu.Unlock()
			asked++
			return screen, true
		}
	})
	c := dial(t, p)
	s := newSess(t, c, ipc.NewRequest{Cwd: "/w/api", Harness: core.HarnessAntigravity})
	in := ipc.HookContext{HostSession: s.ID}
	mu.Lock()
	screen = agyPermissionScreen
	mu.Unlock()
	permission := func() core.InboxItem {
		t.Helper()
		fa.report("blocked", "permission", "Approve Bash: ls -la notes2.txt")
		var item core.InboxItem
		waitFor(t, "the permission in the Inbox", func() bool {
			for _, it := range snapshot(t, c).Inbox {
				if it.SessionID == s.ID && it.Kind == core.InboxPermission {
					item = it
					return true
				}
			}
			return false
		})
		return item
	}

	// Before agy's first hook acmd doesn't know its conversation yet, but agy
	// runs there, so the answer is typed.
	item := permission()
	must(t, c.Call(ipc.OpInboxAnswer, ipc.InboxAnswer{ID: item.ID, Option: 1}, nil))
	if typed.all() != "1" {
		t.Errorf("before the first hook, typed %q", typed.all())
	}
	typed.keys = nil
	must(t, c.Call(ipc.OpEvent, hookEvent("antigravity:PreInvocation", `{"conversationId":"c1","lastUserInput":"ls"}`, time.Now(), in), nil))
	waitFor(t, "the hook closes it", func() bool { return len(snapshot(t, c).Inbox) == 0 })

	item = permission()
	if item.Text != "Approve Bash: ls -la notes2.txt" || len(item.Options) != 4 || item.Options[3] != "No, cancel" {
		t.Errorf("item: %+v", item)
	}
	time.Sleep(100 * time.Millisecond) // more ticks: a report reads the screen once
	mu.Lock()
	if asked != 2 {
		t.Errorf("screen read %d times", asked)
	}
	mu.Unlock()

	must(t, c.Call(ipc.OpInboxAnswer, ipc.InboxAnswer{ID: item.ID, Option: 1}, nil))
	if typed.all() != "1" {
		t.Errorf("typed %q", typed.all())
	}

	// Answered in agy itself instead: working closes the next item.
	fa.report("blocked", "question", "Which file?")
	waitFor(t, "the question", func() bool {
		q := snapshot(t, c).Inbox
		return len(q) == 1 && q[0].Kind == core.InboxQuestion && q[0].Text == "Which file?"
	})
	fa.report("working", "", "")
	waitFor(t, "closed once agy works", func() bool { return len(snapshot(t, c).Inbox) == 0 })

	// A sign in is answered in the session: waiting, but no item.
	fa.report("blocked", "auth", "Sign in")
	waitFor(t, "waiting", func() bool { return sessionByID(t, c, s.ID).Status == core.StatusWaiting })
	if q := snapshot(t, c).Inbox; len(q) != 0 {
		t.Errorf("sign in made an item: %+v", q)
	}
}

// agyPermissionScreen is the bottom of a real agy 1.5.10 screen at a permission
// prompt (core's tests have it with more context).
const agyPermissionScreen = `Requesting permission for:
   ls -la notes2.txt
Run this command?
> 1. Yes, run command
  2. Yes, and always allow in this conversation for commands that start with
'ls'
  3. Yes, and always allow for commands that start with 'ls' (Persist to
settings.json)
  4. No, cancel
  ↑/↓ Navigate · tab Amend · ctrl+g edit/expand command
esc to cancel                                     plan · Gemini 3.8 Flash · high
`

// A permission prompt agy reports becomes an Inbox item with the choices on its
// screen, and answering one types its number. It works before agy's first hook,
// when the session has no conversation id yet.
func TestAntigravityPermissionAnsweredFromTheInbox(t *testing.T) {
	p := testPaths(t)
	typed := &sent{}
	fa := &fakeAgent{}
	start(t, p, func(c *Config) {
		c.Hosts = &fakeHosts{paths: p}
		c.SendToHost = typed.send
		c.FrontEvery = 20 * time.Millisecond
		c.HostAgent = fa.get
		c.HostScreen = func(string) (string, bool) { return agyPermissionScreen, true }
	})
	c := dial(t, p)
	s := newSess(t, c, ipc.NewRequest{Cwd: "/w/api", Harness: core.HarnessAntigravity})

	fa.report("blocked", "permission", "Approve Bash: ls -la notes2.txt")
	var item core.InboxItem
	waitFor(t, "the permission in the Inbox", func() bool {
		for _, it := range snapshot(t, c).Inbox {
			if it.SessionID == s.ID && it.Kind == core.InboxPermission {
				item = it
				return true
			}
		}
		return false
	})
	if item.Text != "Approve Bash: ls -la notes2.txt" || len(item.Options) != 4 || item.Options[3] != "No, cancel" {
		t.Fatalf("item = %q %q", item.Text, item.Options)
	}
	if got := item.Options[1]; got != "Yes, and always allow in this conversation for commands that start with 'ls'" {
		t.Errorf("a wrapped option = %q", got)
	}
	if sessionByID(t, c, s.ID).ClaudeSessionID != "" {
		t.Fatal("the test needs a session without a conversation id")
	}
	must(t, c.Call(ipc.OpInboxAnswer, ipc.InboxAnswer{ID: item.ID, Option: 1}, nil))
	if typed.all() != "1" {
		t.Errorf("typed %q, want the option's number", typed.all())
	}
}
