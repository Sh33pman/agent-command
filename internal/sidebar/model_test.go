package sidebar

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// fake records what the sidebar asks for.
type fake struct {
	mu        sync.Mutex
	ctx       context.Context
	snap      ipc.Snapshot
	mutations []ipc.Mutation
	shown     []string
	news      []string
	panes     []string
	fail      error
}

func (f *fake) Watch(ctx context.Context, _ uint64, _ bool) (ipc.WatchResponse, error) {
	<-ctx.Done() // tests feed snapshots themselves
	return ipc.WatchResponse{}, ctx.Err()
}
func (f *fake) Snapshot(context.Context) (ipc.Snapshot, error) { return f.snap, nil }
func (f *fake) Mutate(_ context.Context, m ipc.Mutation, out any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mutations = append(f.mutations, m)
	if g, ok := out.(*core.Group); ok && m.Kind == ipc.MutGroupCreate {
		*g = core.Group{ID: "new-" + m.Text, Name: m.Text}
	}
	return f.fail
}
func (f *fake) Show(_ context.Context, s *core.Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shown = append(f.shown, s.ID)
	return nil
}
func (f *fake) New(_ context.Context, groupID, dir string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.news = append(f.news, groupID+" "+dir)
	return nil
}
func (f *fake) pane(what string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.panes = append(f.panes, what)
	return nil
}
func (f *fake) FocusView(context.Context) error     { return f.pane("focus-view") }
func (f *fake) ToggleSidebar(context.Context) error { return f.pane("toggle") }
func (f *fake) FitWidth(context.Context) error      { return f.pane("fit") }
func (f *fake) Open(_ context.Context, s *core.Session) error {
	return f.pane("open " + s.ID)
}
func (f *fake) Help(width int, groups bool) []string {
	return []string{"── Sidebar ──", "n  new session", fmt.Sprintf("groups %v width %d", groups, width)}
}

func (f *fake) last() ipc.Mutation {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.mutations) == 0 {
		return ipc.Mutation{}
	}
	return f.mutations[len(f.mutations)-1]
}

var t0 = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

func sess(id, name, group string, st core.Status, n int) *core.Session {
	return &core.Session{ID: id, Name: name, GroupID: group, Status: st, Kind: core.KindBackground, BgID: id + "0000000",
		Cwd: "/w/" + id, CreatedAt: t0.Add(time.Duration(n) * time.Second), LastEventAt: t0.Add(-time.Duration(n) * time.Minute)}
}

func groups(extra ...*core.Group) []*core.Group {
	return append([]*core.Group{{ID: "default", Name: "Default", IsDefault: true, SortKey: 1000000}}, extra...)
}

type harness struct {
	t *testing.T
	m *Model
	f *fake
}

func newHarness(t *testing.T, snap ipc.Snapshot, selected string) *harness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f := &fake{ctx: ctx, snap: snap}
	m := New(ctx, f, "v1", "/home")
	m.now = func() time.Time { return t0 }
	h := &harness{t: t, m: m, f: f}
	h.send(tea.WindowSizeMsg{Width: 34, Height: 30})
	h.send(snapMsg{snap: snap, view: &core.View{ID: "v1", Selected: selected}, rev: 1})
	return h
}

// send delivers a message and runs the commands it returns (briefly: the
// fake Watch blocks, the spinner tick is slower than the wait).
func (h *harness) send(msg tea.Msg) {
	h.t.Helper()
	_, cmd := h.m.Update(msg)
	h.run(cmd)
}

func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		switch msg := msg.(type) {
		case nil:
		case tea.BatchMsg:
			for _, c := range msg {
				h.run(c)
			}
		case snapMsg, tickMsg:
		default:
			h.send(msg)
		}
	case <-time.After(50 * time.Millisecond):
	}
}

func (h *harness) keys(ks ...string) {
	h.t.Helper()
	for _, k := range ks {
		h.send(press(k))
	}
}

func press(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	}
	r := []rune(k)[0]
	return tea.KeyPressMsg{Code: r, Text: k}
}

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (h *harness) screen() string { return ansi.Strip(h.m.render()) }

func (h *harness) wantScreen(subs ...string) {
	h.t.Helper()
	s := h.screen()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			h.t.Errorf("screen lacks %q:\n%s", sub, s)
		}
	}
}

func (h *harness) wantMutation(want ipc.Mutation) {
	h.t.Helper()
	got := h.f.last()
	if got.Kind != want.Kind || got.ID != want.ID || got.GroupID != want.GroupID || got.Text != want.Text || got.Flag != want.Flag ||
		strings.Join(got.IDs, ",") != strings.Join(want.IDs, ",") || got.Color != want.Color {
		h.t.Errorf("last mutation %+v, want %+v", got, want)
	}
}

func listSnap() ipc.Snapshot {
	a := sess("a", "refactor auth middleware", "default", core.StatusBusy, 1)
	a.StatusDetail = "Bash"
	b := sess("b", "fix flaky e2e test", "default", core.StatusWaiting, 2)
	b.Unseen, b.StatusDetail = true, "permission"
	c := sess("c", "docs", "default", core.StatusDone, 3)
	return ipc.Snapshot{Sessions: []*core.Session{a, b, c}, Groups: groups(), ViewState: core.ViewState{Mode: "groups", Sort: "created"}}
}

func TestRenderAndNavigate(t *testing.T) {
	h := newHarness(t, listSnap(), "b")
	h.wantScreen(" AGENTS  ▸ 3 active · 0 paused", " 1", "refactor auth middleware", "a · 1m · Bash", "▌2 ! fix flaky e2e test •",
		"groups · created ↑", "n new ? keys")
	// The cursor starts on the shown session; its details are in the footer.
	h.wantScreen(" fix flaky e2e test", " /w/b")
	if strings.Contains(h.screen(), "Default") {
		t.Error("headers shown while only Default exists")
	}
	h.keys("down", "enter")
	h.keys("up", "k", "j", "1")
	if strings.Join(h.f.shown, ",") != "c,a" {
		t.Errorf("shown %v", h.f.shown)
	}
	// Compact mode: one line per session.
	h.keys("c")
	if strings.Contains(h.screen(), "a · 1m · Bash") {
		t.Error("compact still shows detail lines")
	}
	h.keys("c", "?")
	h.wantScreen(" Shortcuts", "n  new session", "groups false width 33")
	h.keys("down", "up", "x")
	if strings.Contains(h.screen(), " Shortcuts") {
		t.Error("help didn't close")
	}
	h.keys("s")
	h.wantScreen("Split out isn't available yet.")
	h.keys("E")
	h.wantScreen("Opening /w/a…")
	h.keys("h", "esc")
	if strings.Join(h.f.panes, ",") != "fit,open a,toggle,focus-view" {
		t.Errorf("panes %v", h.f.panes)
	}
}

func TestViewKeys(t *testing.T) {
	h := newHarness(t, listSnap(), "")
	for _, c := range []struct {
		key  string
		want ipc.Mutation
	}{
		{"v", ipc.Mutation{Kind: ipc.MutViewMode, Text: "list"}},
		{"o", ipc.Mutation{Kind: ipc.MutViewSort, Text: "name"}},
		{"O", ipc.Mutation{Kind: ipc.MutViewSort, Text: "created", Flag: true}},
		{"A", ipc.Mutation{Kind: ipc.MutViewArchived, Flag: true}},
	} {
		h.keys(c.key)
		h.wantMutation(c.want)
	}
	// Filter: live while typing, saved on Enter, Esc clears it.
	h.keys("/")
	h.typeText("flaky")
	h.wantScreen("Filter:", "fix flaky e2e test")
	if strings.Contains(h.screen(), "docs") {
		t.Error("filter not applied while typing")
	}
	h.keys("enter")
	h.wantMutation(ipc.Mutation{Kind: ipc.MutViewFilter, Text: "flaky"})
	h.f.snap.ViewState.Filter = "flaky"
	h.send(snapMsg{snap: h.f.snap, view: &core.View{ID: "v1"}, rev: 2})
	h.wantScreen("/flaky")
	h.keys("esc")
	h.wantMutation(ipc.Mutation{Kind: ipc.MutViewFilter, Text: ""})
	// A filter typed then abandoned changes nothing.
	n := len(h.f.mutations)
	h.keys("/", "z", "esc")
	if len(h.f.mutations) != n {
		t.Error("esc in the filter saved it")
	}
}

func TestRenameDescribeArchive(t *testing.T) {
	h := newHarness(t, listSnap(), "a")
	h.keys("r")
	h.wantScreen("Rename:")
	for range len("refactor auth middleware") {
		h.keys("backspace")
	}
	h.typeText("auth")
	h.keys("enter")
	h.wantMutation(ipc.Mutation{Kind: ipc.MutRename, ID: "a", Text: "auth"})
	h.keys("e")
	h.typeText("the middleware rewrite")
	h.keys("enter")
	h.wantMutation(ipc.Mutation{Kind: ipc.MutDescribe, ID: "a", Text: "the middleware rewrite"})

	// Archive asks first; anything but y cancels.
	h.keys("a")
	h.wantScreen("Archive “refactor auth", "middleware”? Running ones are", "y yes · any other key: no")
	h.keys("n")
	h.wantScreen("Canceled.")
	h.keys("a", "y")
	h.wantMutation(ipc.Mutation{Kind: ipc.MutArchive, ID: "a"})

	// Marked sessions are archived together.
	h.keys("space", "space")
	h.wantScreen("2 marked")
	h.keys("a")
	h.wantScreen("Archive 2 sessions?")
	h.keys("y")
	if got := h.f.mutations[len(h.f.mutations)-2:]; got[0].Kind != ipc.MutArchive || got[1].Kind != ipc.MutArchive {
		t.Errorf("bulk archive: %+v", got)
	}
	if len(h.m.marked) != 0 {
		t.Error("marks kept after archiving")
	}
	// Esc clears marks before leaving the sidebar.
	h.keys("space", "esc")
	if len(h.m.marked) != 0 || len(h.f.panes) != 1 {
		t.Errorf("esc with marks: %v %v", h.m.marked, h.f.panes)
	}
}

func groupSnap() ipc.Snapshot {
	snap := listSnap()
	snap.Groups = groups(&core.Group{ID: "g1", Name: "PROJ-123", Color: "blue", SortKey: 1}, &core.Group{ID: "g2", Name: "spikes", Color: "magenta", SortKey: 2, Collapsed: true})
	snap.Sessions[0].GroupID = "g1"
	snap.Sessions[1].GroupID = "g2"
	return snap
}

func TestGroups(t *testing.T) {
	h := newHarness(t, groupSnap(), "a")
	h.wantScreen("▾ ● PROJ-123", "▸ ● spikes", " 1 !", "▾ Default")
	// The collapsed group shows its urgent status; its session gets no number.
	if strings.Contains(h.screen(), "fix flaky") {
		t.Error("collapsed group's session shown")
	}
	h.keys("up") // onto the PROJ-123 header
	h.keys("enter")
	h.wantMutation(ipc.Mutation{Kind: ipc.MutGroupCollapse, GroupID: "g1", Flag: true})
	h.keys("space")
	h.wantMutation(ipc.Mutation{Kind: ipc.MutGroupCollapse, GroupID: "g1", Flag: true})
	h.keys("J")
	h.wantMutation(ipc.Mutation{Kind: ipc.MutGroupMove, GroupID: "g1", Flag: true})
	h.keys("K")
	h.wantMutation(ipc.Mutation{Kind: ipc.MutGroupMove, GroupID: "g1"})
	h.keys("r")
	h.typeText("!")
	h.keys("enter")
	h.wantMutation(ipc.Mutation{Kind: ipc.MutGroupRename, GroupID: "g1", Text: "PROJ-123!"})
	h.keys("D", "y")
	h.wantMutation(ipc.Mutation{Kind: ipc.MutGroupDelete, GroupID: "g1"})
	h.keys("a", "y")
	h.wantMutation(ipc.Mutation{Kind: ipc.MutGroupArchive, GroupID: "g1"})
	h.keys("f")
	h.wantMutation(ipc.Mutation{Kind: ipc.MutViewFocus, GroupID: "g1"})
	h.keys("z")
	h.wantMutation(ipc.Mutation{Kind: ipc.MutGroupCollapse, Flag: true})
	h.keys("down", "left") // a session: collapses its group, cursor to the header
	h.wantMutation(ipc.Mutation{Kind: ipc.MutGroupCollapse, GroupID: "g1", Flag: true})
	if h.m.cursor != "g:g1" {
		t.Errorf("cursor %s", h.m.cursor)
	}
	h.keys("down", "down", "right") // the collapsed spikes header
	h.wantMutation(ipc.Mutation{Kind: ipc.MutGroupCollapse, GroupID: "g2", Flag: false})

	// In focus mode, f again (or Esc) leaves it.
	h.f.snap.ViewState.FocusedGroup = "g1"
	h.send(snapMsg{snap: h.f.snap, view: &core.View{ID: "v1", Selected: "a"}, rev: 3})
	h.keys("f")
	h.wantMutation(ipc.Mutation{Kind: ipc.MutViewFocus})
	h.keys("esc")
	h.wantMutation(ipc.Mutation{Kind: ipc.MutViewFocus})

	// The Default group can't be renamed, deleted or archived.
	h.f.snap.ViewState.FocusedGroup = ""
	h.send(snapMsg{snap: h.f.snap, view: &core.View{ID: "v1"}, rev: 4})
	h.m.cursor = "g:default"
	n := len(h.f.mutations)
	h.keys("r")
	h.keys("D")
	h.keys("a")
	if len(h.f.mutations) != n || h.m.note != "The Default group can't be archived." {
		t.Errorf("Default group changed: %+v", h.f.mutations[n:])
	}
}

func TestPickerNewAndMove(t *testing.T) {
	h := newHarness(t, groupSnap(), "a")
	// n: the group picker, preselecting the cursor's group.
	h.keys("n")
	h.wantScreen("New session in group:", "PROJ-123", "spikes", "○ Default", "esc cancel")
	h.keys("enter")
	if got := strings.Join(h.f.news, ";"); got != "g1 /w/a" {
		t.Errorf("new: %s", got)
	}
	// Typing filters; a new name offers to create the group.
	h.keys("n")
	h.typeText("Billing")
	h.wantScreen("＋ Create group “Billing”")
	h.keys("enter")
	if got := h.f.news[len(h.f.news)-1]; got != "new-Billing /home" {
		t.Errorf("new in a created group: %s", got)
	}
	h.wantMutation(ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "Billing"})
	h.keys("n", "esc")
	h.wantScreen("Canceled.")

	// n on a header: that group, no picker. N asks for the folder.
	h.m.cursor = "g:g2"
	h.keys("n")
	if got := h.f.news[len(h.f.news)-1]; got != "g2 /w/b" {
		t.Errorf("n on a header: %s", got)
	}
	h.m.cursor = "s:a"
	h.keys("N")
	h.wantScreen("Folder:")
	for range len("/w/a") {
		h.keys("backspace")
	}
	h.typeText("/tmp/x")
	h.keys("enter") // folder, then the picker
	h.keys("down", "down", "enter")
	if got := h.f.news[len(h.f.news)-1]; got != "default /tmp/x" {
		t.Errorf("N: %s", got)
	}

	// m moves the marked sessions (here: one) to the picked group.
	h.m.cursor = "s:c"
	h.keys("space")
	h.m.cursor = "s:a"
	h.keys("space", "m")
	h.wantScreen("Move 2 sessions to:")
	h.keys("up", "up", "enter")
	h.wantMutation(ipc.Mutation{Kind: ipc.MutMove, IDs: []string{"a", "c"}, GroupID: "g1"})
	if len(h.m.marked) != 0 {
		t.Error("marks kept after moving")
	}
}

func TestPickerWithoutGroups(t *testing.T) {
	h := newHarness(t, listSnap(), "a")
	h.keys("n") // only Default: no picker
	if got := strings.Join(h.f.news, ";"); got != "default /w/a" {
		t.Errorf("new: %s", got)
	}
	h.wantScreen("Starting a session in /w/a…")
}

func TestTabAndErrors(t *testing.T) {
	h := newHarness(t, groupSnap(), "a")
	h.keys("tab") // the waiting session, in the collapsed group: expand it, show it
	if h.f.shown[len(h.f.shown)-1] != "b" || h.m.cursor != "s:b" {
		t.Errorf("tab: shown %v cursor %s", h.f.shown, h.m.cursor)
	}
	h.wantMutation(ipc.Mutation{Kind: ipc.MutGroupCollapse, GroupID: "g2", Flag: false})
	h.send(snapMsg{snap: h.f.snap, view: &core.View{ID: "v1", Selected: "b"}, rev: 5})
	h.keys("tab")
	h.wantScreen("No session needs you.")

	h.f.fail = errors.New("daemon says no")
	h.keys("v")
	h.wantScreen("daemon says no")

	// The view closes: the program ends with ErrClosed.
	_, cmd := h.m.Update(goneMsg{})
	if cmd == nil || !errors.Is(h.m.Err(), ErrClosed) {
		t.Errorf("gone: %v", h.m.Err())
	}
	_, _ = h.m.Update(fatalMsg{errors.New("socket gone")})
	if h.m.Err() == nil || h.m.Err().Error() != "socket gone" {
		t.Errorf("fatal: %v", h.m.Err())
	}
}

func TestMouse(t *testing.T) {
	h := newHarness(t, groupSnap(), "")
	click := func(btn tea.MouseButton, y int) { h.send(tea.MouseClickMsg{Button: btn, Y: y, X: 3}) }
	// Lines: 0 header, 1 rule, 2 PROJ-123, 3-4 session a, 5 spikes, 6 Default, 7-8 session c.
	click(tea.MouseLeft, 3)
	if strings.Join(h.f.shown, ",") != "a" {
		t.Errorf("click: %v", h.f.shown)
	}
	click(tea.MouseLeft, 5)
	h.wantMutation(ipc.Mutation{Kind: ipc.MutGroupCollapse, GroupID: "g2", Flag: false})
	// Double-click renames.
	h.m.now = func() time.Time { return t0.Add(time.Second) }
	click(tea.MouseLeft, 7)
	click(tea.MouseLeft, 8)
	h.wantScreen("Rename:")
	h.keys("esc")
	click(tea.MouseLeft, 0) // the header: nothing
	click(tea.MouseLeft, 29)

	// Right-click: a session menu; choose Archive with the mouse.
	click(tea.MouseRight, 3)
	h.wantScreen("Show", "Edit description", "Open in editor", "Move to group…", "Archive")
	click(tea.MouseLeft, h.m.menu.y+5)
	h.wantScreen("Archive “refactor auth")
	h.keys("n")
	// Group menu with keys; Esc closes; a click outside closes.
	click(tea.MouseRight, 2)
	h.wantScreen("New session here", "Color: magenta", "Collapse others", "Delete group")
	h.keys("down", "down", "enter")
	h.wantMutation(ipc.Mutation{Kind: ipc.MutGroupColor, GroupID: "g1", Color: "magenta"})
	click(tea.MouseRight, 2)
	h.keys("esc")
	click(tea.MouseRight, 2)
	click(tea.MouseRight, 1)
	if h.m.mode != modeNormal {
		t.Error("menu still open")
	}
	click(tea.MouseRight, 6) // Default: no rename/delete
	if strings.Contains(h.screen(), "Delete group") {
		t.Error("Default's menu offers delete")
	}
	h.keys("down", "down", "enter") // Collapse others
	if h.f.last().Kind != ipc.MutGroupCollapse {
		t.Errorf("collapse others: %+v", h.f.last())
	}

	// The picker takes clicks too.
	h.m.cursor = "s:c"
	h.keys("m")
	click(tea.MouseLeft, bodyTop+2)
	h.wantMutation(ipc.Mutation{Kind: ipc.MutMove, IDs: []string{"c"}, GroupID: "g1"})

	// Wheel scrolls.
	h.send(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	h.send(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
}

func TestScrollAndNarrow(t *testing.T) {
	snap := listSnap()
	for i := range 20 {
		snap.Sessions = append(snap.Sessions, sess(string(rune('d'+i)), "extra", "default", core.StatusPaused, 10+i))
	}
	h := newHarness(t, snap, "")
	h.send(tea.WindowSizeMsg{Width: 20, Height: 12})
	for range 15 {
		h.keys("down")
	}
	if h.m.top == 0 {
		t.Error("didn't scroll to the cursor")
	}
	if !strings.Contains(h.screen(), "extra") {
		t.Errorf("narrow screen:\n%s", h.screen())
	}
	for _, l := range strings.Split(h.screen(), "\n") {
		if w := ansi.StringWidth(l); w != 20 {
			t.Errorf("line width %d: %q", w, l)
		}
	}
}

func TestAge(t *testing.T) {
	for d, want := range map[time.Duration]string{10 * time.Second: "10s", 5 * time.Minute: "5m", 3 * time.Hour: "3h", 72 * time.Hour: "3d", -time.Second: "0s"} {
		if got := age(t0, &core.Session{LastEventAt: t0.Add(-d)}); got != want {
			t.Errorf("age(%v) = %s, want %s", d, got, want)
		}
	}
	if got := age(t0, &core.Session{CreatedAt: t0.Add(-2 * time.Minute)}); got != "2m" {
		t.Errorf("age from CreatedAt = %s", got)
	}
	if nextSort("cwd") != "created" || nextSort("bogus") != "created" || nextColor("red") != "blue" || nextColor("") != "blue" {
		t.Error("cycles")
	}
	if tilde("/home/x", "/home") != "~/x" || tilde("/other", "/home") != "/other" {
		t.Error("tilde")
	}
	if got := wrap(" a long question that wraps", 12); len(got) < 2 {
		t.Errorf("wrap: %q", got)
	}
}
