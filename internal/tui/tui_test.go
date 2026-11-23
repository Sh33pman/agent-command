package tui

import (
	"encoding/json"
	"errors"
	"go/build"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// fakeDaemon answers ops from a snapshot, and records the others.
type fakeDaemon struct {
	mu    sync.Mutex
	snap  ipc.Snapshot
	calls []string
	fail  error
}

func (f *fakeDaemon) Call(op string, in, out any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if op == ipc.OpSnapshot {
		b, _ := json.Marshal(f.snap)
		return json.Unmarshal(b, out)
	}
	if f.fail != nil {
		return f.fail
	}
	b, _ := json.Marshal(in)
	f.calls = append(f.calls, op+" "+string(b))
	return nil
}

func (f *fakeDaemon) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func sampleSnapshot() ipc.Snapshot {
	t0 := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	sessions := []*core.Session{
		{ID: "L1", Name: "api", Kind: core.KindHosted, Status: core.StatusBusy, GroupID: "default", CreatedAt: t0},
		{ID: "L2", Name: "web", Kind: core.KindHosted, Status: core.StatusWaiting, GroupID: "default", CreatedAt: t0.Add(time.Minute)},
		{ID: "r1", Name: "docs", Kind: core.KindHosted, Host: "studio", Status: core.StatusDone, GroupID: "g1", CreatedAt: t0},
	}
	groups := []*core.Group{
		{ID: "default", Name: "Default", IsDefault: true},
		{ID: "default", Name: "Default", IsDefault: true, Host: "studio"},
		{ID: "g1", Name: "Writing", Host: "studio", SortKey: 1, Collapsed: true},
	}
	return ipc.Snapshot{
		Sessions: ipc.SessionsOf(sessions), Groups: ipc.GroupsOf(groups),
		Inbox: []core.InboxItem{
			{ID: 7, SessionID: "L2", Kind: core.InboxPermission, Text: "Bash: make test", Options: core.PermissionOptions, State: core.InboxOpen},
			{ID: 14, SessionID: "r1", Host: "studio", Kind: core.InboxDone, Text: "Docs are done.", State: core.InboxOpen},
		},
		RemoteHosts: []ipc.RemoteHost{{Name: "studio", State: ipc.HostConnected}},
		Features:    &ipc.Features{Host: ipc.HostFeatures{OS: "darwin", Sessions: true}},
	}
}

// harness drives a Model as Bubble Tea would, running the commands it
// returns (except ticks, timers and the terminal hand-over, which it reports).
type harness struct {
	t     *testing.T
	m     Model
	d     *fakeDaemon
	execs []tea.ExecCommand
	raws  []string
	quit  bool
}

func newHarness(t *testing.T, snap ipc.Snapshot) *harness {
	t.Helper()
	d := &fakeDaemon{snap: snap}
	at := time.Date(2026, 11, 23, 9, 41, 0, 0, time.Local)
	h := &harness{t: t, d: d}
	h.m = New(Options{Daemon: d, Me: "terminal-me", Now: func() time.Time { return at },
		Attach: func(s ipc.Session) tea.ExecCommand { return fakeExec{id: s.ID} }})
	h.m.exec = func(c tea.ExecCommand, _ tea.ExecCallback) tea.Cmd {
		h.execs = append(h.execs, c)
		return nil
	}
	h.send(tea.WindowSizeMsg{Width: 160, Height: 30})
	h.send(h.m.load()())
	return h
}

// errNoMore is the end of the test's stream: it sends events itself.
var errNoMore = errors.New("no more events")

type noMore struct{}

func (noMore) NextEvent() (ipc.Event, error) { return ipc.Event{}, errNoMore }

type fakeExec struct{ id string }

func (fakeExec) Run() error          { return nil }
func (fakeExec) SetStdin(io.Reader)  {}
func (fakeExec) SetStdout(io.Writer) {}
func (fakeExec) SetStderr(io.Writer) {}

// send applies msg and runs what it returns.
func (h *harness) send(msg tea.Msg) {
	h.t.Helper()
	m, cmd := h.m.Update(msg)
	h.m = m.(Model)
	h.run(cmd)
}

func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			h.run(c)
		}
	case tea.QuitMsg:
		h.quit = true
	case tea.RawMsg:
		h.raws = append(h.raws, msg.Msg.(string))
	case refreshMsg:
		h.send(h.m.load()())
	case eventMsg:
		if errors.Is(msg.err, errNoMore) {
			return // the test's stream has no more: the test sends the events
		}
		h.send(msg)
	default:
		h.send(msg)
	}
}

func (h *harness) key(keys ...string) {
	h.t.Helper()
	for _, k := range keys {
		h.send(keyMsg(k))
	}
}

func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(k)[0]
	return tea.KeyPressMsg{Code: r, Text: k}
}

// screen is what's drawn, without its styles.
func (h *harness) screen() string { return ansi.Strip(h.m.View().Content) }

// selectedLine is the line drawn reversed.
func (h *harness) selectedLine() string {
	for _, l := range strings.Split(h.m.View().Content, "\n") {
		if strings.HasPrefix(l, reverse) {
			return ansi.Strip(l)
		}
	}
	return ""
}

func TestDrawsTheSessionsHereThenEachHost(t *testing.T) {
	h := newHarness(t, sampleSnapshot())
	s := h.screen()
	for _, want := range []string{
		"acmd · 3 sessions · 2 need you",
		"This machine", "    api", "busy",
		"    web", "waiting   permission: Bash: make test (1 Yes · 2 Yes, and don't ask again · 3 No)",
		"studio", "  Writing (1)", "    docs", "done: Docs are done.",
		keysHelp,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("screen lacks %q:\n%s", want, s)
		}
	}
	// A lone Default has no header, as in the sidebar; a host's empty one none.
	if strings.Contains(s, "Default") {
		t.Errorf("a Default header:\n%s", s)
	}
	if strings.Index(s, "This machine") > strings.Index(s, "studio") {
		t.Errorf("a host comes before this machine:\n%s", s)
	}
	if !h.m.View().AltScreen {
		t.Error("not the whole terminal")
	}
	if l := h.selectedLine(); !strings.Contains(l, "api") {
		t.Errorf("selected %q, want the first session", l)
	}
}

func TestMovesAndAttaches(t *testing.T) {
	h := newHarness(t, sampleSnapshot())
	h.key("down")
	if !strings.Contains(h.selectedLine(), "web") {
		t.Errorf("down: %q", h.selectedLine())
	}
	h.key("j") // past a section and a group header
	if !strings.Contains(h.selectedLine(), "docs") {
		t.Errorf("j: %q", h.selectedLine())
	}
	h.key("j") // the last one stays
	if !strings.Contains(h.selectedLine(), "docs") {
		t.Errorf("j at the end: %q", h.selectedLine())
	}
	h.key("k", "up", "up")
	if !strings.Contains(h.selectedLine(), "api") {
		t.Errorf("up at the top: %q", h.selectedLine())
	}
	h.key("enter")
	if len(h.execs) != 1 || h.execs[0].(fakeExec).id != "L1" {
		t.Errorf("enter attached %v", h.execs)
	}
	// Back from the attach: an error says so, and the list is read again.
	h.send(attachedMsg{errors.New(`"api" isn't running`)})
	if !strings.Contains(h.screen(), `✗ "api" isn't running`) {
		t.Errorf("attach error:\n%s", h.screen())
	}
	h.key("q")
	if !h.quit {
		t.Error("q didn't quit")
	}
}

func TestAnswersAPromptAndRepliesToAFinishedTurn(t *testing.T) {
	h := newHarness(t, sampleSnapshot())
	h.key("a") // api has nothing waiting
	if !strings.Contains(h.screen(), "Nothing is waiting there.") {
		t.Errorf("a on api:\n%s", h.screen())
	}
	h.key("j", "a")
	if !strings.Contains(h.screen(), "answer web: press 1-3") {
		t.Errorf("a on web:\n%s", h.screen())
	}
	h.key("7") // not an option
	h.key("2")
	if c := h.d.called(); len(c) != 1 || c[0] != `inbox.answer {"id":7,"option":2}` {
		t.Errorf("calls %v", c)
	}
	if !strings.Contains(h.screen(), "Answered web: Yes, and don't ask again") {
		t.Errorf("after answering:\n%s", h.screen())
	}

	h.key("j", "a", "o", "k", "!", "backspace", "enter")
	if c := h.d.called(); len(c) != 2 || c[1] != `inbox.answer {"host":"studio","id":14,"text":"ok"}` {
		t.Errorf("reply: %v", c)
	}
	h.key("a", "esc")
	if h.m.mode != modeList || len(h.d.called()) != 2 {
		t.Errorf("esc didn't cancel: mode %d, %v", h.m.mode, h.d.called())
	}

	h.d.fail = errors.New("the session is busy: go to it to reply")
	h.key("a", "x", "enter")
	if !strings.Contains(h.screen(), "✗ the session is busy") {
		t.Errorf("a refused answer:\n%s", h.screen())
	}
}

func TestStartsASessionHereOrOnTheSelectedHost(t *testing.T) {
	h := newHarness(t, sampleSnapshot())
	h.key("n", "enter")
	if !strings.Contains(h.screen(), "✗ a session here needs a folder") {
		t.Errorf("n with no folder:\n%s", h.screen())
	}
	h.key("n", "/", "w", "enter")
	h.key("j", "j", "n")
	if !strings.Contains(h.screen(), "new session on studio") {
		t.Errorf("n on a host's:\n%s", h.screen())
	}
	h.key("enter")
	if c := h.d.called(); len(c) != 2 || c[0] != `new {"cwd":"/w"}` || c[1] != `remote.new {"host":"studio","dir":"~"}` {
		t.Errorf("calls %v", c)
	}

	// A machine that runs no sessions (Windows) starts them on a host.
	snap := sampleSnapshot()
	snap.Features.Host.Sessions = false
	snap.Sessions = snap.Sessions[2:]
	h = newHarness(t, snap)
	h.key("n", "~", "enter")
	if c := h.d.called(); len(c) != 1 || c[0] != `remote.new {"host":"studio","dir":"~"}` {
		t.Errorf("client-only: %v", c)
	}
	snap.RemoteHosts = nil
	snap.Sessions = nil
	h = newHarness(t, snap)
	h.key("n")
	if !strings.Contains(h.screen(), "no host to start one on") || h.m.mode != modeList {
		t.Errorf("client-only with no host:\n%s", h.screen())
	}
	if !strings.Contains(h.screen(), "no sessions yet") {
		t.Errorf("an empty list:\n%s", h.screen())
	}
}

func TestFilters(t *testing.T) {
	h := newHarness(t, sampleSnapshot())
	h.key("/", "d", "o")
	s := h.screen()
	if strings.Contains(s, "    api") || !strings.Contains(s, "    docs") || !strings.Contains(s, "filter: do") {
		t.Errorf("filter do:\n%s", s)
	}
	h.key("enter")
	if !strings.Contains(h.screen(), "· filter: do") || h.m.mode != modeList {
		t.Errorf("filter kept:\n%s", h.screen())
	}
	h.key("esc")
	if !strings.Contains(h.screen(), "    api") {
		t.Errorf("esc cleared nothing:\n%s", h.screen())
	}
	h.key("/", "x", "esc")
	if h.m.filter != "" {
		t.Errorf("esc while typing kept %q", h.m.filter)
	}
}

func TestFollowsEventsAndRingsForItsNotices(t *testing.T) {
	h := newHarness(t, sampleSnapshot())
	notice := func(n ipc.Notice) ipc.Event {
		b, _ := json.Marshal(n)
		return ipc.Event{Type: ipc.EventAdded, Entity: ipc.EntityNotice, Data: b}
	}
	h.send(eventMsg{stream: noMore{}, ev: notice(ipc.Notice{Kind: "waiting", Title: "⚠ web needs you", Body: "Bash", To: []string{"terminal-me"}})})
	if !strings.Contains(h.screen(), "09:41 ⚠ web needs you: Bash") || len(h.raws) != 1 || h.raws[0] != "\a" {
		t.Errorf("a notice for it: raws %q\n%s", h.raws, h.screen())
	}
	h.send(eventMsg{stream: noMore{}, ev: notice(ipc.Notice{Kind: "done", Title: "✓ docs finished", Host: "studio", To: []string{"mac"}})})
	if !strings.Contains(h.screen(), "✓ docs finished (on studio)") || len(h.raws) != 1 {
		t.Errorf("another's notice rang: %q", h.raws)
	}

	// A change reads the snapshot again, keeping the selection.
	h.key("j")
	snap := sampleSnapshot()
	snap.Sessions[0].Name = "api-renamed"
	h.d.snap = snap
	h.send(eventMsg{stream: noMore{}, ev: ipc.Event{Type: ipc.EventUpdated, Entity: ipc.EntitySession, ID: "L1"}})
	if !strings.Contains(h.screen(), "api-renamed") || !strings.Contains(h.selectedLine(), "web") {
		t.Errorf("after an update (selected %q):\n%s", h.selectedLine(), h.screen())
	}
	// Events while a read is on its way read once more after it.
	h.m.loading = true
	h.send(eventMsg{stream: noMore{}, ev: ipc.Event{Type: ipc.EventUpdated, Entity: ipc.EntitySession, ID: "L1"}})
	if !h.m.dirty {
		t.Error("an event during a read was lost")
	}

	h.send(eventMsg{err: errors.New("EOF")})
	if !strings.Contains(h.screen(), "✗ live updates stopped: EOF") {
		t.Errorf("stream end:\n%s", h.screen())
	}
	h.send(streamMsg{err: errors.New("no daemon")})
	if !strings.Contains(h.screen(), "✗ no live updates: no daemon") {
		t.Errorf("no stream:\n%s", h.screen())
	}
	h.key("ctrl+c")
	if !h.quit {
		t.Error("ctrl+c didn't quit")
	}
}

func TestKeepsTheSelectionOnScreen(t *testing.T) {
	snap := sampleSnapshot()
	var many []*core.Session
	for i := range 40 {
		many = append(many, &core.Session{ID: "S" + string(rune('A'+i)), Name: "s" + string(rune('a'+i%26)) + string(rune('0'+i/26)),
			Kind: core.KindHosted, GroupID: "default", CreatedAt: time.Unix(int64(i), 0)})
	}
	snap.Sessions = ipc.SessionsOf(many)
	h := newHarness(t, snap)
	h.send(tea.WindowSizeMsg{Width: 60, Height: 10})
	for range 30 {
		h.key("j")
	}
	if h.selectedLine() == "" {
		t.Errorf("the selection scrolled off:\n%s", h.screen())
	}
	if n := strings.Count(h.screen(), "\n"); n > 10 {
		t.Errorf("%d lines in a 10-line terminal", n)
	}
}

func TestShowsWhatWentWrongReadingTheList(t *testing.T) {
	h := newHarness(t, sampleSnapshot())
	h.send(snapshotMsg{err: errors.New("daemon gone")})
	if !strings.Contains(h.screen(), "✗ daemon gone") {
		t.Errorf("snapshot error:\n%s", h.screen())
	}
	m := New(Options{Daemon: &fakeDaemon{}})
	if !strings.Contains(ansi.Strip(m.View().Content), "loading") {
		t.Error("nothing says it's loading")
	}
}

// The terminal UI is a client like any other: it reaches the daemon through
// its ops and events only, so it could be its own binary.
func TestImportsNothingOfTheDaemons(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	allowed := []string{"github.com/brettmostert/agent-command/internal/ipc", "github.com/brettmostert/agent-command/internal/core"}
	for _, imp := range append(pkg.Imports, pkg.TestImports...) {
		if strings.HasPrefix(imp, "github.com/brettmostert/agent-command/") && !slices.Contains(allowed, imp) {
			t.Errorf("internal/tui imports %s: it may use only the wire (%v)", imp, allowed)
		}
	}
}
