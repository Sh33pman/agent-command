package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ghostty"
	"github.com/brettmostert/agent-command/internal/ghostty/ghosttytest"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/kitty"
	"github.com/brettmostert/agent-command/internal/kitty/kittytest"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/setup"
)

func TestGhosttyView(t *testing.T) {
	e := viewEnvFor(t)
	sim := ghosttytest.New()
	orig := ghosttyApp
	ghosttyApp = func() *ghostty.App { return sim.App() }
	t.Cleanup(func() { ghosttyApp = orig })
	ctx := context.Background()

	var v core.View
	must(t, e.conn.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutViewOpen, Pane: &core.PaneLoc{Terminal: "ghostty", GhosttyTerm: "T1", TTY: "/dev/ttys001"}}, &v))
	sb, err := addSidebar(ctx, e.conn.Call, &v, "")
	must(t, err)
	if sb.Terminal != "ghostty" || sb.GhosttyTerm == "" || sim.Focused != "T1" {
		t.Fatalf("sidebar %+v focused %s", sb, sim.Focused)
	}
	if cmd := sim.Terms[1].Command; !strings.HasPrefix(cmd, "'") || !strings.HasSuffix(cmd, "'sidebar' '--view' '"+v.ID+"'") {
		t.Errorf("sidebar command: %s", cmd)
	}
	// The header splits the view pane upward (it spans only the view pane).
	if sb.HeaderTerm == "" || !strings.Contains(strings.Join(sim.Calls, "\n"), "split T1 up → "+sb.HeaderTerm) {
		t.Errorf("header %q, calls:\n%s", sb.HeaderTerm, strings.Join(sim.Calls, "\n"))
	}

	// toggle zooms the view and records it; focus unhides first.
	mustRun(t, "toggle")
	if !sim.Zoomed["T1"] || !e.snapshot(t).Views[0].SidebarHidden {
		t.Errorf("toggle: zoomed %v hidden %v", sim.Zoomed["T1"], e.snapshot(t).Views[0].SidebarHidden)
	}
	mustRun(t, "focus")
	if sim.Zoomed["T1"] || e.snapshot(t).Views[0].SidebarHidden || sim.Focused != sb.GhosttyTerm {
		t.Errorf("focus: zoomed %v focused %s", sim.Zoomed["T1"], sim.Focused)
	}
	// A closed sidebar comes back on toggle.
	must(t, sim.App().Close(ctx, sb.GhosttyTerm))
	mustRun(t, "toggle")
	if v2 := e.snapshot(t).Views[0]; v2.Sidebar.GhosttyTerm == sb.GhosttyTerm || v2.Sidebar.GhosttyTerm == "" {
		t.Errorf("not re-added: %+v", v2.Sidebar)
	}

	// go focuses the view pane.
	api := e.folder(t, "api", false)
	mustRun(t, "new", "--cwd", api, "--name", "auth")
	sim.Focused = "elsewhere"
	mustRun(t, "go", "auth")
	if sim.Focused != "T1" {
		t.Errorf("go focused %s", sim.Focused)
	}
	view := e.snapshot(t).Views[0]
	// A terminal editor opens in a tab too.
	p, _ := paths.Default()
	must(t, setup.SaveEditor(p.Config(), config.Editor{Preset: "custom", Command: []string{"/bin/echo", "{dir}"}, Resolved: "/bin/echo", RunIn: "tab"}))
	s := &core.Session{ID: "x", Cwd: api}
	must(t, openSession(ctx, s, view, openOptions{}))
	if last := sim.Calls[len(sim.Calls)-1]; last != "tab "+api+" '/bin/echo' '"+api+"'" {
		t.Errorf("editor tab: %s", last)
	}

	// Removing the sidebar closes its terminal.
	view = e.snapshot(t).Views[0]
	removeSidebar(ctx, view)
	if ok, _ := sim.App().Exists(ctx, view.Sidebar.GhosttyTerm); ok {
		t.Error("sidebar still open")
	}
	if ok, _ := sim.App().Exists(ctx, view.Sidebar.HeaderTerm); ok || view.Sidebar.HeaderTerm == "" {
		t.Errorf("header %q still open", view.Sidebar.HeaderTerm)
	}
	if err := focusPane(ctx, core.PaneLoc{Terminal: "unknown"}); err == nil {
		t.Error("focus on an unknown terminal")
	}
	if err := promptIn(ctx, &core.View{}, nil); err == nil {
		t.Error("prompt without a terminal")
	}
	if err := tabIn(ctx, &core.View{}, "/", nil); err == nil {
		t.Error("tab without a terminal")
	}
}

func TestCurrentGhosttyPane(t *testing.T) {
	sim := ghosttytest.New()
	orig, origTTY := ghosttyApp, ttyName
	ghosttyApp = func() *ghostty.App { return sim.App() }
	ttyName = func() string { return "/dev/ttys001" }
	t.Cleanup(func() { ghosttyApp, ttyName = orig, origTTY })
	if got := ttyName(); got != "/dev/ttys001" {
		t.Error(got)
	}
	p := &viewPanes{v: &core.View{Pane: core.PaneLoc{Terminal: "ghostty", GhosttyTerm: "T1"}}, width: 32}
	if err := p.FitWidth(context.Background()); err != nil { // no terminal in tests: nothing to fit
		t.Errorf("FitWidth: %v", err)
	}
}

func TestViewFocusedAndFallback(t *testing.T) {
	gsim, ksim := ghosttytest.New(), kittytest.New()
	origG, origK := ghosttyApp, kittyAt
	ghosttyApp = func() *ghostty.App { return gsim.App() }
	kittyAt = func(string) *kitty.Kitty { return ksim.Kitty() }
	t.Cleanup(func() { ghosttyApp, kittyAt = origG, origK })
	ctx := context.Background()
	gv := &core.View{Pane: core.PaneLoc{Terminal: "ghostty", GhosttyTerm: "T1"}}
	if !viewFocused(ctx, gv) {
		t.Error("ghostty: focused view not seen")
	}
	gsim.Background = true
	if viewFocused(ctx, gv) {
		t.Error("ghostty in the background counted as focused")
	}
	kv := &core.View{Pane: core.PaneLoc{Terminal: "kitty", KittySocket: "unix:/tmp/k", KittyWindow: 1}}
	ksim.Unfocused = true
	if viewFocused(ctx, kv) {
		t.Error("kitty: unfocused OS window counted")
	}
	if viewFocused(ctx, &core.View{}) {
		t.Error("no terminal counted as focused")
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	fallbackNotify(ipc.Notice{Title: "t", Body: "b", Sound: "default"}) // osascript via the (simulated) runner: no panic
}
