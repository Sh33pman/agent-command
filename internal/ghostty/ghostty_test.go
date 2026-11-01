package ghostty_test

import (
	"context"
	"errors"
	"testing"

	"github.com/brettmostert/agent-command/internal/ghostty"
	"github.com/brettmostert/agent-command/internal/ghostty/ghosttytest"
)

func TestAdapter(t *testing.T) {
	ctx := context.Background()
	s := ghosttytest.New()
	a := s.App()
	id, err := a.TermForTTY(ctx, "/dev/ttys001")
	if err != nil || id != "T1" {
		t.Fatalf("TermForTTY: %q %v", id, err)
	}
	if _, err := a.TermForTTY(ctx, "/dev/ttys999"); !errors.Is(err, ghostty.ErrNotFound) {
		t.Errorf("unknown tty: %v", err)
	}
	side, err := a.AddSidebarLeft(ctx, "T1", "'acmd' 'sidebar'", "/home", []string{"A=1", "B=2"})
	if err != nil || side == "" || s.Focused != "T1" {
		t.Fatalf("add: %q %v focused %s", side, err, s.Focused)
	}
	if got := s.Terms[1]; got.Command != "'acmd' 'sidebar'" || got.Cwd != "/home" || len(got.Env) != 2 {
		t.Errorf("sidebar: %+v", got)
	}
	if _, err := a.AddSidebarLeft(ctx, "nope", "x", "/", nil); !errors.Is(err, ghostty.ErrNotFound) {
		t.Errorf("add to a missing terminal: %v", err)
	}
	must(t, a.Action(ctx, "T1", "toggle_split_zoom"))
	if !s.Zoomed["T1"] {
		t.Error("not zoomed")
	}
	must(t, a.Focus(ctx, side))
	if ok, err := a.Exists(ctx, side); !ok || err != nil {
		t.Errorf("exists: %v %v", ok, err)
	}
	must(t, a.NewTab(ctx, "T1", "'vi'", "/w", nil))
	must(t, a.Close(ctx, side))
	if ok, _ := a.Exists(ctx, side); ok {
		t.Error("still exists after close")
	}
	for _, err := range []error{a.Action(ctx, side, "x"), a.Focus(ctx, side), a.Close(ctx, side), a.NewTab(ctx, side, "x", "/", nil)} {
		if !errors.Is(err, ghostty.ErrNotFound) {
			t.Errorf("missing terminal: %v", err)
		}
	}
	if ok, err := a.Running(ctx); !ok || err != nil {
		t.Errorf("running: %v %v", ok, err)
	}
	must(t, a.ReloadConfig(ctx))
	s.Denied = true
	if _, err := a.Running(ctx); !errors.Is(err, ghostty.ErrNotPermitted) {
		t.Errorf("denied: %v", err)
	}
}

func TestQuoteAndResize(t *testing.T) {
	if got := ghostty.Quote([]string{"/opt/acmd", "sidebar", "it's"}); got != `'/opt/acmd' 'sidebar' 'it'\''s'` {
		t.Errorf("Quote: %s", got)
	}
	for _, c := range []struct {
		delta int
		px    float64
		want  string
	}{{4, 7.7, "resize_split:right,31"}, {-2, 8, "resize_split:left,16"}, {0, 8, "resize_split:right,0"}} {
		if got := ghostty.ResizeAction(c.delta, c.px); got != c.want {
			t.Errorf("ResizeAction(%d, %v) = %s, want %s", c.delta, c.px, got, c.want)
		}
	}
}

func TestOsascript(t *testing.T) {
	// Real osascript, no app involved: arguments and errors.
	a := ghostty.New()
	out, err := a.Run(context.Background(), "on run argv\nreturn \"got \" & item 1 of argv\nend run", "it's \"quoted\"")
	if err != nil || out != `got it's "quoted"` {
		t.Errorf("osascript: %q %v", out, err)
	}
	if _, err := a.Run(context.Background(), `error "Not authorized to send Apple events to Ghostty." number -1743`); !errors.Is(err, ghostty.ErrNotPermitted) {
		t.Errorf("-1743: %v", err)
	}
	if _, err := a.Run(context.Background(), `error "boom" number 7`); err == nil || errors.Is(err, ghostty.ErrNotPermitted) {
		t.Errorf("other error: %v", err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
