package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ghostty"
	"github.com/brettmostert/agent-command/internal/ghostty/ghosttytest"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/kitty"
	"github.com/brettmostert/agent-command/internal/kitty/kittytest"
)

func TestAdoptAndSplitOut(t *testing.T) {
	e := viewEnvFor(t)
	ksim, gsim := kittytest.New(), ghosttytest.New()
	origK, origG, origWait := kittyAt, ghosttyApp, adoptWait
	kittyAt = func(string) *kitty.Kitty { return ksim.Kitty() }
	ghosttyApp = func() *ghostty.App { return gsim.App() }
	adoptWait = 10 * time.Second
	t.Cleanup(func() { kittyAt, ghosttyApp, adoptWait = origK, origG, origWait })
	ctx := context.Background()
	t0 := time.Now()
	send := func(name, payload string, at time.Time, hc ipc.HookContext) {
		t.Helper()
		must(t, e.conn.Call(ipc.OpEvent, ipc.HookEvent{Name: name, At: at, Payload: []byte(payload), Context: hc}, nil))
	}
	api := e.folder(t, "api", false)
	kctx := ipc.HookContext{KittyWindowID: "2", KittyListenOn: "unix:/tmp/k"}
	send("SessionStart", `{"session_id":"ext-k","source":"startup","cwd":"`+api+`"}`, t0, kctx)
	send("UserPromptSubmit", `{"session_id":"ext-k","prompt":"busy work"}`, t0.Add(time.Second), kctx)

	// Without --yes and a terminal: refused.
	if _, err := acmd(t, "adopt", "busy work"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("no confirmation: %v", err)
	}
	t.Setenv("KITTY_LISTEN_ON", "unix:/tmp/k") // the shortcut's Kitty
	origPoll := adoptPoll
	adoptPoll = 20 * time.Millisecond
	t.Cleanup(func() { adoptPoll = origPoll })
	// When acmd types /bg, "Claude" forks the session into the background.
	go func() {
		// Watch long enough for a loaded machine (the full suite runs
		// packages in parallel): the waits below come first.
		for i := 0; i < 1500; i++ {
			if ksim.Called("send-text --match id:2 /bg") {
				send("SessionStart", `{"session_id":"bbbb0000-fork","source":"startup","cwd":"`+api+`"}`, t0.Add(4*time.Second), ipc.HookContext{})
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	// Busy, then waiting on a question: acmd waits (the shortcut finds the
	// session by its pane) and types /bg only once Claude is done.
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := acmd(t, "adopt", "--kitty-window", "2", "--yes")
		done <- result{out, err}
	}()
	time.Sleep(150 * time.Millisecond)
	send("PermissionRequest", `{"session_id":"ext-k","tool_name":"Bash"}`, t0.Add(2*time.Second), kctx)
	time.Sleep(150 * time.Millisecond)
	if ksim.Called("send-text --match id:2 /bg") {
		t.Fatal("typed /bg while Claude was busy or waiting on a question")
	}
	send("Stop", `{"session_id":"ext-k"}`, t0.Add(3*time.Second), kctx)
	var r result
	select {
	case r = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("adopt didn't finish after Claude became idle")
	}
	if r.err != nil || !strings.Contains(r.out, "Waiting for Claude to finish in that pane (working)") ||
		!strings.Contains(r.out, "(it's waiting for you: answer it first)") ||
		!strings.Contains(r.out, `Moved "busy work" into acmd (claude attach bbbb0000).`) {
		t.Errorf("adopt: %v\n%s", r.err, r.out)
	}
	if !ksim.Called("close-window --match id:2") {
		t.Errorf("the pane Claude left wasn't closed:\n%s", strings.Join(ksim.Calls, "\n"))
	}
	if _, err := acmd(t, "adopt", "busy work", "--yes"); err == nil {
		t.Error("adopted twice")
	}

	// An external session whose process ended is resumed instead.
	send("SessionStart", `{"session_id":"cafe0000-gone","source":"startup","cwd":"`+api+`"}`, t0, ipc.HookContext{TermProgram: "ghostty", TTY: "/dev/ttys001"})
	send("SessionEnd", `{"session_id":"cafe0000-gone","reason":"other"}`, t0.Add(time.Second), ipc.HookContext{TermProgram: "ghostty"})
	out := mustRun(t, "adopt", "cafe0000-gone", "--yes")
	if !strings.Contains(out, "Moved session cafe0000 into acmd") {
		t.Errorf("resume fallback:\n%s", out)
	}
	for _, c := range gsim.Calls {
		if strings.HasPrefix(c, "close") {
			t.Errorf("closed a pane for a resumed session: %s", c)
		}
	}

	// Typing into a Ghostty pane finds it by tty.
	must(t, typeInto(ctx, core.PaneLoc{Terminal: "ghostty", TTY: "/dev/ttys001"}, "/bg"))
	if last := gsim.Calls[len(gsim.Calls)-1]; last != "type T1 /bg" {
		t.Errorf("ghostty type: %s", last)
	}
	if err := typeInto(ctx, core.PaneLoc{Terminal: "unknown"}, "/bg"); err == nil {
		t.Error("typed into an unknown pane")
	}

	// Split out: needs a view; Kitty tab and window, Ghostty tab and window.
	if _, err := acmd(t, "split-out", "busy work"); err == nil {
		t.Error("split out without a view")
	}
	var kv core.View
	must(t, e.conn.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutViewOpen, Pane: &core.PaneLoc{Terminal: "kitty", KittySocket: "unix:/tmp/k", KittyWindow: 1}}, &kv))
	mustRun(t, "split-out", "busy work")
	mustRun(t, "split-out", "busy work", "--window", "--sidebar")
	calls := strings.Join(ksim.Calls, "\n")
	if !strings.Contains(calls, "launch --type=tab --match id:1 --cwd "+api) || !strings.Contains(calls, "view --pinned ") ||
		!strings.Contains(calls, "launch --type=os-window --match id:1") || !strings.Contains(calls, "view --start ") {
		t.Errorf("kitty split-out calls:\n%s", calls)
	}
	for _, bad := range [][]string{{"split-out", "busy work", "--tab", "--window"}, {"split-out", "busy work", "--plain", "--sidebar"}} {
		if _, err := acmd(t, bad...); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	gv := &core.View{Pane: core.PaneLoc{Terminal: "ghostty", GhosttyTerm: "T1"}}
	var s *core.Session
	for _, x := range e.snapshot(t).Sessions {
		if x.Name == "busy work" {
			s = x
		}
	}
	must(t, splitOut(ctx, gv, s, false, false))
	must(t, splitOut(ctx, gv, s, true, true))
	g := strings.Join(gsim.Calls, "\n")
	if !strings.Contains(g, "tab "+api+" '") || !strings.Contains(g, "window "+api+" '") {
		t.Errorf("ghostty split-out:\n%s", g)
	}
	if err := splitOut(ctx, gv, &core.Session{Kind: core.KindExternal}, false, false); err == nil {
		t.Error("split out an external session")
	}
	if err := splitOut(ctx, &core.View{}, s, false, false); err == nil {
		t.Error("split out without a terminal")
	}
}
