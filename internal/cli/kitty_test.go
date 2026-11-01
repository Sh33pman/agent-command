package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/kitty"
	"github.com/brettmostert/agent-command/internal/kitty/kittytest"
)

func TestKittyShortcuts(t *testing.T) {
	e := viewEnvFor(t)
	sim := kittytest.New()
	orig := kittyAt
	kittyAt = func(string) *kitty.Kitty { return sim.Kitty() }
	t.Cleanup(func() { kittyAt = orig })
	ctx := context.Background()

	// A view in Kitty window 1 gets its sidebar on the left.
	var v core.View
	must(t, e.conn.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutViewOpen, Pane: &core.PaneLoc{Terminal: "kitty", KittySocket: "unix:/tmp/k", KittyWindow: 1}}, &v))
	sb, err := addSidebar(ctx, e.conn.Call, &v)
	must(t, err)
	if sb.KittyWindow == 0 || sb.RestoreLayout != "fat" || sim.Tabs[0].Windows[0].ID != sb.KittyWindow || sim.Tabs[0].Layout != "splits" {
		t.Fatalf("sidebar %+v, tab %+v", sb, sim.Tabs[0])
	}
	if got := e.snapshot(t).Views[0].Sidebar; got != sb {
		t.Errorf("daemon has sidebar %+v, want %+v", got, sb)
	}
	// The header sits right above the view pane (window 1), 1 line.
	for i, w := range sim.Tabs[0].Windows {
		if w.ID == 1 && (i == 0 || sim.Tabs[0].Windows[i-1].ID != sb.HeaderWindow || sim.Tabs[0].Windows[i-1].Lines != 1) {
			t.Errorf("header %d not above the view: %+v", sb.HeaderWindow, sim.Tabs[0].Windows)
		}
	}
	// The full-width status bar is the last window (bottom edge), 1 line.
	ws := sim.Tabs[0].Windows
	if bar := ws[len(ws)-1]; sb.BarWindow == 0 || bar.ID != sb.BarWindow || bar.Lines != 1 || bar.UserVars["acmd_role"] != "bar" {
		t.Errorf("status bar %d, windows %+v", sb.BarWindow, ws)
	}

	// toggle hides and shows; focus shows a hidden sidebar and focuses it.
	mustRun(t, "toggle", "--kitty-window", "1")
	if sim.Tabs[0].Layout != "stack" {
		t.Errorf("toggle didn't hide: %s", sim.Tabs[0].Layout)
	}
	mustRun(t, "focus", "--kitty-window", "1")
	if sim.Tabs[0].Layout != "splits" || sim.Focused != sb.KittyWindow {
		t.Errorf("focus: layout %s, focused %d", sim.Tabs[0].Layout, sim.Focused)
	}
	mustRun(t, "toggle", "--all")
	mustRun(t, "toggle", "--kitty-window", "999") // not a view window: the most recent view
	if sim.Tabs[0].Layout != "splits" {
		t.Errorf("toggle --all then toggle: %s", sim.Tabs[0].Layout)
	}

	// A closed sidebar comes back on toggle.
	must(t, sim.Kitty().Close(ctx, sb.KittyWindow))
	mustRun(t, "toggle", "--kitty-window", "1")
	v2 := e.snapshot(t).Views[0]
	if v2.Sidebar.KittyWindow == sb.KittyWindow || v2.Sidebar.KittyWindow == 0 || v2.Sidebar.RestoreLayout != "fat" {
		t.Errorf("sidebar not re-added: %+v", v2.Sidebar)
	}
	// With a new bar under it; the old bar is gone.
	if v2.Sidebar.BarWindow == 0 || v2.Sidebar.BarWindow == sb.BarWindow {
		t.Errorf("bar not re-added: %+v", v2.Sidebar)
	}
	for _, w := range sim.Tabs[0].Windows {
		if w.ID == sb.BarWindow {
			t.Error("old bar left open")
		}
	}

	// Sessions: a trusted folder, then it becomes untrusted for the shortcut.
	api := e.folder(t, "api", false)
	mustRun(t, "new", "--cwd", api, "--name", "first")
	mustRun(t, "new", "--cwd", api, "--name", "second")
	snap := e.snapshot(t)
	var first, second *core.Session
	for _, s := range snap.Sessions {
		switch s.Name {
		case "first":
			first = s
		case "second":
			second = s
		}
	}
	if first == nil || second == nil {
		t.Fatalf("sessions: %+v", snap.Sessions)
	}

	// next-attention: nothing yet; then a waiting session is shown and focused.
	if _, err := acmd(t, "next-attention", "--kitty-window", "1"); err == nil || !strings.Contains(err.Error(), "no session needs you") {
		t.Errorf("next-attention with nothing waiting: %v", err)
	}
	var claudeID string
	eventually(t, "the reconciler links the session", func() bool {
		for _, s := range e.snapshot(t).Sessions {
			if s.ID == second.ID {
				claudeID = s.ClaudeSessionID
			}
		}
		return claudeID != ""
	})
	t0 := time.Now()
	for _, ev := range []ipc.HookEvent{
		{Name: "SessionStart", At: t0, Payload: []byte(`{"session_id":"` + claudeID + `","source":"startup","cwd":"/w"}`)},
		{Name: "PermissionRequest", At: t0.Add(time.Second), Payload: []byte(`{"session_id":"` + claudeID + `","tool_name":"Bash"}`)},
	} {
		must(t, e.conn.Call(ipc.OpEvent, ev, nil))
	}
	sim.Focused = 2
	mustRun(t, "next-attention", "--kitty-window", "1")
	if e.snapshot(t).Views[0].Selected != second.ID || sim.Focused != 1 {
		t.Errorf("next-attention: selected %s, focused %d", e.snapshot(t).Views[0].Selected, sim.Focused)
	}

	// go: a view session; an external session focuses its own pane.
	mustRun(t, "go", "first", "--kitty-window", "1")
	if e.snapshot(t).Views[0].Selected != first.ID {
		t.Error("go didn't switch")
	}
	must(t, e.conn.Call(ipc.OpEvent, ipc.HookEvent{Name: "SessionStart", At: t0, Payload: []byte(`{"session_id":"ext-1","source":"startup","cwd":"/w"}`),
		Context: ipc.HookContext{KittyWindowID: "2", KittyListenOn: "unix:/tmp/k"}}, nil))
	mustRun(t, "go", "ext-1")
	if sim.Focused != 2 {
		t.Errorf("go external focused %d", sim.Focused)
	}

	// new from the shortcut in an untrusted folder: Claude's trust prompt
	// opens in an overlay over the view pane.
	must(t, os.WriteFile(filepath.Join(api, ".untrusted"), nil, 0o600))
	mustRun(t, "new", "--kitty-window", "1")
	last := sim.Calls[len(sim.Calls)-1]
	realAPI, _ := filepath.EvalSymlinks(api)
	if !strings.HasPrefix(last, "launch --type=overlay --match id:1 -- ") || !strings.Contains(last, " new --cwd "+realAPI+" --view "+v.ID) {
		t.Errorf("overlay: %s", last)
	}
	must(t, os.Remove(filepath.Join(api, ".untrusted")))
	mustRun(t, "new", "--kitty-window", "1")
	if sel := e.snapshot(t).Views[0].Selected; sel == first.ID || sel == "" {
		t.Error("new from a shortcut didn't follow into the view")
	}

	// Closing the view's sidebar gives the tab its layout back.
	v3 := e.snapshot(t).Views[0]
	must(t, sim.Kitty().Close(ctx, 1)) // the view pane is closed first (cmd+w)
	removeSidebar(ctx, v3)
	if sim.Tabs[0].Layout != "fat" {
		t.Errorf("layout after removing the sidebar: %s", sim.Tabs[0].Layout)
	}
	for _, w := range sim.Tabs[0].Windows {
		if w.ID == v3.Sidebar.BarWindow || w.ID == v3.Sidebar.KittyWindow || w.ID == v3.Sidebar.HeaderWindow {
			t.Errorf("window %d left open", w.ID)
		}
	}
	removeSidebar(ctx, &core.View{}) // no sidebar: nothing to do
}

func TestNextAttentionOrder(t *testing.T) {
	mk := func(id string, st core.Status, unseen bool, n int) *core.Session {
		return &core.Session{ID: id, Status: st, Unseen: unseen, GroupID: "default", Kind: core.KindBackground,
			CreatedAt: time.Date(2026, 1, 1, 0, 0, n, 0, time.UTC)}
	}
	done, seen, errS, wait := mk("done", core.StatusDone, true, 1), mk("seen", core.StatusDone, false, 2), mk("err", core.StatusError, false, 3), mk("wait", core.StatusWaiting, false, 4)
	snap := ipc.Snapshot{Sessions: []*core.Session{done, seen, errS, wait}, Groups: []*core.Group{{ID: "default", Name: "Default", IsDefault: true}},
		ViewState: core.ViewState{Mode: "list", Sort: "created", Filter: "nothing matches"}}
	for shown, want := range map[string]string{"": "wait", "wait": "err", "err": "wait"} {
		if got := nextAttention(snap, shown); got == nil || got.ID != want {
			t.Errorf("shown %q: got %v, want %s", shown, got, want)
		}
	}
	snap.Sessions = []*core.Session{done, seen}
	if got := nextAttention(snap, "done"); got != nil {
		t.Errorf("only the shown one needs attention: got %s", got.ID)
	}
}
