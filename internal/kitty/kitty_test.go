package kitty_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/kitty"
	"github.com/brettmostert/agent-command/internal/kitty/kittytest"
)

func TestAddSidebarLeft(t *testing.T) {
	s := kittytest.New()
	k := s.Kitty()
	ctx := context.Background()
	sb, prev, err := k.AddSidebarLeft(ctx, kitty.SidebarSpec{View: 1, ViewID: "V1", Width: 32, Command: []string{"/bin/acmd", "sidebar", "--view", "V1"},
		Env: []string{"ACMD_STATE_DIR=/x"}})
	if err != nil {
		t.Fatal(err)
	}
	if prev != "fat" || s.Tabs[0].Layout != "splits" {
		t.Errorf("layout: prev %q, now %q", prev, s.Tabs[0].Layout)
	}
	w := s.Tabs[0].Windows
	if w[0].ID != sb || w[0].Columns != 32 || w[0].UserVars["acmd_role"] != "sidebar" || w[0].UserVars["acmd_view"] != "V1" {
		t.Errorf("sidebar not on the left at 32 cols: %+v", w)
	}
	if s.Focused != 1 {
		t.Errorf("focus not back on the view pane: %d", s.Focused)
	}
	if !strings.Contains(strings.Join(s.Calls, "\n"), "launch --location=vsplit --match id:1 --var acmd_role=sidebar --var acmd_view=V1 --title acmd --env ACMD_STATE_DIR=/x -- /bin/acmd sidebar --view V1") {
		t.Errorf("launch call:\n%s", strings.Join(s.Calls, "\n"))
	}

	// Hide/show.
	if err := k.ToggleSidebar(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if h, _ := k.Hidden(ctx, 1); !h {
		t.Error("not hidden after toggle")
	}
	before := len(s.Calls)
	if err := k.SetWidth(ctx, sb, 40); err != nil || len(s.Calls) != before+1 {
		t.Errorf("resized while hidden: %v %v", err, s.Calls[before:])
	}
	_ = k.ToggleSidebar(ctx, 1)
	if h, _ := k.Hidden(ctx, 1); h {
		t.Error("still hidden")
	}

	// Drift and correction; close; restore layout.
	s.Tabs[0].Windows[0].Columns = 24
	if err := k.SetWidth(ctx, sb, 32); err != nil || s.Tabs[0].Windows[0].Columns != 32 {
		t.Errorf("width after drift: %d %v", s.Tabs[0].Windows[0].Columns, err)
	}
	must(t, k.Close(ctx, sb))
	must(t, k.SetLayout(ctx, 1, prev))
	must(t, k.Focus(ctx, 2))
	if s.Tabs[0].Layout != "fat" || len(s.Tabs[0].Windows) != 2 || s.Focused != 2 {
		t.Errorf("after close: %+v focused %d", s.Tabs[0], s.Focused)
	}
	must(t, k.Overlay(ctx, 1, []string{"/bin/acmd", "new"}, []string{"A=1"}))
	if last := s.Calls[len(s.Calls)-1]; last != "launch --type=overlay --match id:1 --env A=1 -- /bin/acmd new" {
		t.Errorf("overlay: %s", last)
	}
}

func TestAdapterErrors(t *testing.T) {
	ctx := context.Background()
	s := kittytest.New()
	k := s.Kitty()
	if _, _, err := k.AddSidebarLeft(ctx, kitty.SidebarSpec{View: 99}); err == nil {
		t.Error("missing view window accepted")
	}
	for _, fail := range []string{"ls", "goto-layout", "launch", "focus-window", "action", "resize-window"} {
		s := kittytest.New()
		s.Fail = fail
		if _, _, err := s.Kitty().AddSidebarLeft(ctx, kitty.SidebarSpec{View: 1, Width: 30, Command: []string{"x"}}); err == nil {
			t.Errorf("%s failing wasn't reported", fail)
		}
	}
	s = kittytest.New()
	s.Launch = "oops"
	if _, _, err := s.Kitty().AddSidebarLeft(ctx, kitty.SidebarSpec{View: 1, Width: 30, Command: []string{"x"}}); err == nil {
		t.Error("bad launch output accepted")
	}
	if err := k.SetWidth(ctx, 99, 30); err == nil {
		t.Error("SetWidth on a missing window")
	}
	if _, err := k.Hidden(ctx, 99); err == nil {
		t.Error("Hidden on a missing window")
	}
	if _, err := (&kitty.Kitty{Run: s.Run}).LS(ctx); err == nil || !strings.Contains(err.Error(), "restart Kitty") {
		t.Errorf("no socket: %v", err)
	}
	bad := &kitty.Kitty{Kitten: "/k/kitten", To: "unix:/tmp/k", Run: func(context.Context, string, string, ...string) ([]byte, error) { return []byte("{"), nil }}
	if _, err := bad.LS(ctx); err == nil {
		t.Error("bad ls JSON accepted")
	}
}

func TestFindHelpers(t *testing.T) {
	ws := []kitty.OSWindow{{Tabs: []kitty.Tab{{ID: 1, Windows: []kitty.Window{{ID: 5, UserVars: map[string]string{"acmd_role": "sidebar"}}}}}}}
	if w, tab, ok := kitty.Find(ws, 5); !ok || w.ID != 5 || tab.ID != 1 {
		t.Error("Find")
	}
	if _, _, ok := kitty.Find(ws, 6); ok {
		t.Error("Find missing")
	}
	if w, _, ok := kitty.FindVar(ws, "acmd_role", "sidebar"); !ok || w.ID != 5 {
		t.Error("FindVar")
	}
	if _, _, ok := kitty.FindVar(ws, "acmd_role", "x"); ok {
		t.Error("FindVar missing")
	}
	if p := kitty.FindKitten(func(string) (string, error) { return "/on/path/kitten", nil }); p != "/on/path/kitten" {
		t.Errorf("FindKitten = %s", p)
	}
	_ = kitty.FindKitten(func(string) (string, error) { return "", errors.New("no") })
}

func TestExecRunner(t *testing.T) {
	// A stand-in kitten: /bin/echo prints its arguments.
	run := kitty.New("/bin/echo", "unix:/s").Run
	out, err := run(context.Background(), "/bin/echo", "unix:/s", "ls")
	if err != nil || strings.TrimSpace(string(out)) != "@ --to unix:/s ls" {
		t.Errorf("execRunner: %q %v", out, err)
	}
	if _, err := run(context.Background(), "/usr/bin/false", "unix:/s", "ls"); err == nil {
		t.Error("failure not reported")
	}
	if k := kitty.New("/k", "unix:/s"); k.Run == nil || k.To != "unix:/s" {
		t.Error("New")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
