package view

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

func plain(s string) []string {
	return strings.Split(ansi.ReplaceAllString(s, ""), "\r\n")
}

func TestRender(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	a := bg("s1", "aaaaaaaa", "refactor auth middleware for the api", core.StatusBusy)
	a.Cwd, a.LastEventAt, a.StatusDetail = "/w/api-server", now.Add(-3*time.Minute), "Bash"
	b := bg("s2", "bbbbbbbb", "", core.StatusWaiting)
	b.Cwd, b.LastEventAt, b.Unseen, b.StatusDetail = "/w/web/", now.Add(-12*time.Second), true, "permission"
	ext := bg("s3", "", "spike", core.StatusDone)
	ext.Kind, ext.Cwd, ext.LastEventAt = core.KindExternal, "/w/scratch", now.Add(-50*time.Hour)
	p := bg("s4", "dddddddd", "old", core.StatusPaused)
	p.LastEventAt = now.Add(-5 * time.Hour)
	snap := ipc.Snapshot{Sessions: []*core.Session{a, b, ext, p}, Groups: []*core.Group{{ID: "default", Name: "Default", IsDefault: true}},
		ViewState: core.ViewState{Mode: "groups", Sort: "created"}}

	lines := plain(Render(snap, &core.View{Selected: "s2"}, 32, 14, "", now))
	want := []string{
		" AGENTS  3 active · 1 paused",
		" ──────────────────────────────",
		" 1 ⠹ refactor auth middleware f…",
		"    api-server · 3m · Bash",
		"▌2 ! session bbbbbbbb •",
		"    web · 12s · permission",
		" 3 ✓ spike ↗",
		"    scratch · 2d",
		" 4 ‖ old",
		"     · 5h",
		"",
		"",
		" ──────────────────────────────",
		" 1–9 show · n new · h hide · esc",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("render:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}

	// Too short: rows are dropped, the footer stays; a note shows above it.
	lines = plain(Render(snap, nil, 32, 5, "boom", now))
	if len(lines) != 5 || lines[1] != " ──────────────────────────────" || lines[3] != " boom" {
		t.Errorf("short render: %q", lines)
	}

	// Group headers when groups exist; a tiny width is clamped.
	g := &core.Group{ID: "g1", Name: "PROJ-123", SortKey: 1}
	a.GroupID = "g1"
	snap.Groups = append(snap.Groups, g)
	out := strings.Join(plain(Render(snap, nil, 32, 0, "", now)), "\n")
	if !strings.Contains(out, " ▾ PROJ-123  1") || !strings.Contains(out, " ▾ Default  3") {
		t.Errorf("group headers:\n%s", out)
	}
	if narrow := plain(Render(snap, nil, 5, 0, "", now)); narrow[0] != " AGENTS  3 …" {
		t.Errorf("narrow width not clamped to 12: %q", narrow[0])
	}
}

type fakePanes struct {
	mu      sync.Mutex
	calls   []string
	failing bool
}

func (f *fakePanes) rec(c string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	if f.failing {
		return errors.New(c + " failed")
	}
	return nil
}
func (f *fakePanes) FocusView(context.Context) error     { return f.rec("focus-view") }
func (f *fakePanes) ToggleSidebar(context.Context) error { return f.rec("toggle") }
func (f *fakePanes) FitWidth(context.Context) error      { return f.rec("fit") }
func (f *fakePanes) FocusPane(_ context.Context, l core.PaneLoc) error {
	return f.rec("focus-pane " + l.Terminal)
}
func (f *fakePanes) got() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, ",")
}

func TestSidebarKeys(t *testing.T) {
	a := bg("s1", "aaaaaaaa", "auth", core.StatusBusy)
	a.Cwd = "/w/api"
	ext := bg("s2", "", "mine", core.StatusDone)
	ext.Kind, ext.External = core.KindExternal, &core.PaneLoc{Terminal: "kitty", KittyWindow: 4}
	lost := bg("s3", "", "lost", core.StatusDone)
	lost.Kind = core.KindExternal
	d := newFakeDaemon(a, ext, lost)
	panes := &fakePanes{}
	r, w, err := os.Pipe()
	must(t, err)
	defer r.Close()
	defer w.Close()
	out := &syncBuffer{}
	var newDir string
	sb := &Sidebar{ViewID: "v1", Client: d, Panes: panes, Keys: &TermKeys{In: r}, Out: out, Home: "/home",
		Size: func() (int, int) { return 32, 20 },
		New:  func(_ context.Context, dir string) error { newDir = dir; return errors.New("no claude here") }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sb.Run(ctx) }()
	wait := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out: %s\n%s", what, out.String())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	wait("first render", func() bool { return strings.Contains(out.String(), "auth") })

	_, _ = w.WriteString("1")
	wait("select 1", func() bool { return d.selected() == "s1" && strings.Contains(panes.got(), "focus-view") })
	wait("redraw with the bar", func() bool { return strings.Contains(ansi.ReplaceAllString(out.String(), ""), "▌1") })
	_, _ = w.WriteString("2")
	wait("focus external", func() bool { return strings.Contains(panes.got(), "focus-pane kitty") })
	_, _ = w.WriteString("3")
	wait("unknown pane", func() bool { return strings.Contains(out.String(), "don't know which pane") })
	_, _ = w.WriteString("h")
	wait("hide", func() bool { return strings.Contains(panes.got(), "toggle") })
	_, _ = w.WriteString("n")
	wait("new", func() bool { return strings.Contains(out.String(), "no claude here") })
	if newDir != "/w/api" {
		t.Errorf("new in %q, want the shown session's folder", newDir)
	}
	before := strings.Count(panes.got(), "focus-view")
	_, _ = w.WriteString("\x1b[A") // an arrow key: ignored
	time.Sleep(150 * time.Millisecond)
	_, _ = w.WriteString("\x1b") // Esc: back to the view
	wait("esc", func() bool { return strings.Count(panes.got(), "focus-view") == before+1 })

	// The view closes: the sidebar exits.
	d.change(func() { d.gone = true })
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Errorf("sidebar ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sidebar kept running after its view closed")
	}
	cancel()
}

func TestNewFolder(t *testing.T) {
	t0 := time.Now()
	a := &core.Session{ID: "a", Cwd: "/a", LastEventAt: t0}
	b := &core.Session{ID: "b", Cwd: "/b", LastEventAt: t0.Add(time.Minute)}
	snap := ipc.Snapshot{Sessions: []*core.Session{a, b}}
	for _, c := range []struct {
		sel, want string
		snap      ipc.Snapshot
	}{{"a", "/a", snap}, {"", "/b", snap}, {"", "/home", ipc.Snapshot{}}} {
		if got := NewFolder(c.snap, &core.View{Selected: c.sel}, "/home"); got != c.want {
			t.Errorf("NewFolder(selected %q) = %s, want %s", c.sel, got, c.want)
		}
	}
}

func TestAge(t *testing.T) {
	now := time.Now()
	for d, want := range map[time.Duration]string{10 * time.Second: "10s", 5 * time.Minute: "5m", 3 * time.Hour: "3h", 72 * time.Hour: "3d", -time.Second: "0s"} {
		if got := age(now, &core.Session{LastEventAt: now.Add(-d)}); got != want {
			t.Errorf("age(%v) = %s, want %s", d, got, want)
		}
	}
	if got := age(now, &core.Session{CreatedAt: now.Add(-2 * time.Minute)}); got != "2m" {
		t.Errorf("age from CreatedAt = %s", got)
	}
}
