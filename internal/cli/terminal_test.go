package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/daemon"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/tui"
	"github.com/brettmostert/agent-command/internal/version"
)

// helloRecorder keeps the client each hello names.
func helloRecorder(f *fakeDaemon) func() []*ipc.ClientHello {
	var mu sync.Mutex
	var got []*ipc.ClientHello
	f.handle(ipc.OpHello, func(b json.RawMessage) (any, error) {
		var h ipc.Hello
		_ = json.Unmarshal(b, &h)
		mu.Lock()
		got = append(got, h.Client)
		mu.Unlock()
		return ipc.Hello{Proto: ipc.Proto, Version: version.Get().Version}, nil
	})
	return func() []*ipc.ClientHello {
		mu.Lock()
		defer mu.Unlock()
		return append([]*ipc.ClientHello(nil), got...)
	}
}

func fakeRunning(t *testing.T, running bool) {
	t.Helper()
	orig := sessionRunning
	sessionRunning = func(string) bool { return running }
	t.Cleanup(func() { sessionRunning = orig })
}

func attachSnapshot() ipc.Snapshot {
	return ipc.Snapshot{Sessions: ipc.SessionsOf([]*core.Session{
		{ID: "L1", Name: "api", Kind: core.KindHosted, Status: core.StatusBusy, CreatedAt: time.Now().Add(-time.Hour)},
		{ID: "r2", Name: "docs", Kind: core.KindHosted, Host: "studio", Status: core.StatusWaiting, CreatedAt: time.Now()},
	})}
}

// acmd attach attaches to a session here or a host's, through its socket
// here, as a terminal client.
func TestAttach(t *testing.T) {
	f := newFakeDaemon(t)
	hellos := helloRecorder(f)
	f.reply(ipc.OpSnapshot, attachSnapshot())
	f.reply(ipc.OpClientPresence, nil)
	f.reply(ipc.OpRemoteBrowse, sampleBrowse)
	fakeRunning(t, true)

	fakeAttach(t, false, nil)
	_, err := acmd(t, "attach", "api")
	errContains(t, err, "needs a terminal")

	sockets := fakeAttach(t, true, nil)
	mustRun(t, "attach", "api")
	mustRun(t, "attach", "docs") // a host's session, as it shows here
	mustRun(t, "--host", "studio", "attach", "docs")
	if want := []string{f.p.HostSocket("L1"), f.p.HostSocket("r2"), f.p.HostSocket("r2")}; strings.Join(*sockets, " ") != strings.Join(want, " ") {
		t.Errorf("attached to %v, want %v", *sockets, want)
	}
	if !f.calledWith(ipc.OpClientPresence, `"presence":"active"`) || !f.calledWith(ipc.OpRemoteBrowse, `"host":"studio"`) {
		t.Errorf("calls: %v", f.called())
	}
	// It came as this machine's terminal client, the same one each time.
	var ids []string
	for _, h := range hellos() {
		if h != nil {
			if h.Kind != ipc.KindTerminal || !h.OnHost || !strings.Contains(strings.Join(h.Caps, ","), ipc.CapNotify) {
				t.Errorf("hello client = %+v", h)
			}
			ids = append(ids, h.ID)
		}
	}
	if len(ids) != 3 || ids[0] != ids[1] || ids[1] != ids[2] || !strings.HasPrefix(ids[0], "terminal-") {
		t.Errorf("terminal client ids %v", ids)
	}
	if b, err := os.ReadFile(terminalClientFile(f.p)); err != nil || strings.TrimSpace(string(b)) != ids[0] {
		t.Errorf("the id file: %q %v", b, err)
	}

	// --json prints the session once detached.
	out := mustRun(t, "--json", "attach", "api")
	var s ipc.Session
	if err := json.Unmarshal([]byte(out), &s); err != nil || s.ID != "L1" {
		t.Errorf("--json: %q %v", out, err)
	}

	fakeRunning(t, false)
	_, err = acmd(t, "attach", "api")
	errContains(t, err, `"api" isn't running: resume it with acmd resume L1`)
	fakeRunning(t, true)
	_, err = acmd(t, "attach", "nope")
	if err == nil {
		t.Error("attached to a session that isn't there")
	}
	fakeAttach(t, true, errors.New("socket gone"))
	_, err = acmd(t, "attach", "api")
	errContains(t, err, "socket gone")
}

func inboxSnapshot() ipc.Snapshot {
	s := attachSnapshot()
	s.Inbox = []core.InboxItem{
		{ID: 7, SessionID: "L1", Kind: core.InboxPermission, Text: "Bash: make test", Options: core.PermissionOptions, State: core.InboxOpen},
		{ID: 14, SessionID: "r2", Host: "studio", Kind: core.InboxDone, Text: "Docs are up to date.", State: core.InboxOpen},
		{ID: 9, SessionID: "L1", Kind: core.InboxQuestion, Text: "Which?", Options: []string{"A", "B"}, Multipart: true, State: core.InboxOpen},
		{ID: 3, SessionID: "L1", Kind: core.InboxDone, Text: "old", State: core.InboxResponded},
	}
	return s
}

func TestInbox(t *testing.T) {
	f := newFakeDaemon(t)
	f.reply(ipc.OpSnapshot, inboxSnapshot())
	f.reply(ipc.OpInboxAnswer, nil)
	f.reply(ipc.OpMutate, nil)

	out := mustRun(t, "inbox")
	for _, want := range []string{"#  SESSION", "1  api", "studio", "permission", "Bash: make test  (1 Yes · 2 Yes, and don't ask again · 3 No)",
		"2  docs", "done", "Docs are up to date."} {
		if !strings.Contains(out, want) {
			t.Errorf("inbox lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "old") {
		t.Errorf("an answered item is listed:\n%s", out)
	}
	var items []core.InboxItem
	if err := json.Unmarshal([]byte(mustRun(t, "--json", "inbox")), &items); err != nil || len(items) != 3 || items[1].Host != "studio" {
		t.Errorf("--json: %+v %v", items, err)
	}

	// By number and option number, by id and option text, a host's by host:id
	// with a reply.
	if out := mustRun(t, "inbox", "answer", "1", "1"); !strings.Contains(out, `Answered the permission in "api".`) ||
		!f.calledWith(ipc.OpInboxAnswer, `"id":7`, `"option":1`) {
		t.Errorf("answer 1 1: %q %v", out, f.called())
	}
	mustRun(t, "inbox", "answer", "7", "yes,", "and", "don't", "ask", "again")
	if !f.calledWith(ipc.OpInboxAnswer, `"id":7`, `"option":2`) {
		t.Errorf("answer by text: %v", f.called())
	}
	if out := mustRun(t, "inbox", "answer", "studio:14", "run the tests too"); !strings.Contains(out, `"docs" on studio`) ||
		!f.calledWith(ipc.OpInboxAnswer, `"host":"studio"`, `"id":14`, `"text":"run the tests too"`) {
		t.Errorf("answer a host's: %q %v", out, f.called())
	}
	for args, want := range map[string]string{
		"1 4":       "pick an option from 1 to 3",
		"1 Maybe":   `"Maybe" isn't one of the options: 1 Yes`,
		"3 A":       "several parts: answer it in the session (acmd attach L1)",
		"99 1":      "no Inbox item 99 is waiting",
		"lab:1 x":   "lab has no Inbox item",
		"first yes": "an item is its number",
	} {
		_, err := acmd(t, append([]string{"inbox", "answer"}, strings.Fields(args)...)...)
		errContains(t, err, want)
	}

	if out := mustRun(t, "inbox", "dismiss", "2", "#1"); !strings.Contains(out, "Dismissed 2.") ||
		!f.calledWith(ipc.OpMutate, `"kind":"inbox.dismiss"`, `{"host":"studio","id":14}`, `{"id":7}`) {
		t.Errorf("dismiss: %q %v", out, f.called())
	}

	f.reply(ipc.OpSnapshot, ipc.Snapshot{})
	if out := mustRun(t, "inbox"); !strings.Contains(out, "Nothing is waiting.") {
		t.Errorf("empty inbox: %q", out)
	}
}

func noticeEvent(t *testing.T, typ string, n ipc.Notice) ipc.Event {
	t.Helper()
	b, err := json.Marshal(n)
	must(t, err)
	return ipc.Event{Type: typ, Entity: ipc.EntityNotice, Data: b}
}

func TestWatcherPrintsNoticesAndRings(t *testing.T) {
	var b bytes.Buffer
	at := time.Date(2026, 11, 23, 9, 41, 0, 0, time.Local)
	w := watcher{out: &b, me: "terminal-me", now: func() time.Time { return at }}
	mine := ipc.Notice{Kind: "waiting", Title: "⚠ api needs you", Body: "Bash", To: []string{"mac", "terminal-me"}}
	other := ipc.Notice{Kind: "done", Title: "✓ docs finished", Host: "studio", To: []string{"mac"}}
	for _, ev := range []ipc.Event{
		noticeEvent(t, ipc.EventAdded, mine),
		noticeEvent(t, ipc.EventAdded, other),                                                     // another client's
		noticeEvent(t, ipc.EventAdded, ipc.Notice{Kind: "withdraw", To: []string{"terminal-me"}}), // taken back
		noticeEvent(t, ipc.EventRemoved, mine),                                                    // gone from the list
		{Type: ipc.EventSnapshot},
	} {
		must(t, w.event(ev))
	}
	if got := b.String(); got != "\a09:41 ⚠ api needs you: Bash\n" {
		t.Errorf("watch printed %q", got)
	}

	b.Reset()
	w.all = true
	must(t, w.event(noticeEvent(t, ipc.EventAdded, other)))
	if got := b.String(); got != "09:41 ✓ docs finished (on studio)\n" {
		t.Errorf("--all printed %q (no bell: it isn't this terminal's)", got)
	}

	b.Reset()
	w.all, w.json = false, true
	must(t, w.event(noticeEvent(t, ipc.EventAdded, mine)))
	var ev ipc.Event
	if err := json.Unmarshal(b.Bytes(), &ev); err != nil || ev.Entity != ipc.EntityNotice || strings.Contains(b.String(), "\a") {
		t.Errorf("--json printed %q (%v)", b.String(), err)
	}
	if err := w.event(ipc.Event{Type: ipc.EventAdded, Entity: ipc.EntityNotice, Data: json.RawMessage("nope")}); err == nil {
		t.Error("a garbled notice")
	}
}

func TestWatchTakesNoHost(t *testing.T) {
	newFakeDaemon(t)
	_, err := acmd(t, "--host", "studio", "watch")
	errContains(t, err, "doesn't take --host")
}

// syncBuffer is a bytes.Buffer safe to read while a command writes it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// acmd watch is a terminal client: with notify.route = one, a notice goes to
// it, and it prints it with the bell.
func TestWatchGetsTheNoticesRoutedHere(t *testing.T) {
	origHost := targetHost
	targetHost = ""
	t.Cleanup(func() { targetHost = origHost })
	dir, err := os.MkdirTemp("", "acmdw")
	must(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	p := paths.Paths{StateDir: filepath.Join(dir, "s"), RuntimeDir: filepath.Join(dir, "r"), ConfigDir: filepath.Join(dir, "c")}
	t.Setenv("ACMD_STATE_DIR", p.StateDir)
	t.Setenv("ACMD_RUNTIME_DIR", p.RuntimeDir)
	t.Setenv("ACMD_CONFIG_DIR", p.ConfigDir)
	t.Setenv("ACMD_NO_AUTO_INIT", "1")
	orig := spawnDaemon
	spawnDaemon = func(paths.Paths) error { return errors.New("tests start the daemon themselves") }
	t.Cleanup(func() { spawnDaemon = orig })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	fellBack := make(chan ipc.Notice, 4)
	go func() {
		done <- daemon.Run(ctx, daemon.Config{Paths: p, Version: version.Get().Version, Log: log.New(io.Discard, "", 0),
			Notify:   &config.Notify{Enabled: true, OnWaiting: true, Route: config.RouteOne},
			Fallback: func(n ipc.Notice) { fellBack <- n }, TailEvery: time.Hour, ReconcileEvery: time.Hour})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("daemon: %v", err)
		}
	})
	var c *ipc.Conn
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if c, err = ipc.Dial(ctx, p.Socket(), ipc.HelloFrom(version.Get().Version)); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
	}
	defer c.Close()

	out := &syncBuffer{}
	wctx, stop := context.WithCancel(context.Background())
	watched := make(chan error, 1)
	go func() {
		root := NewRootCmd("acmd", out, io.Discard)
		root.SetArgs([]string{"watch"})
		watched <- root.ExecuteContext(wctx)
	}()
	defer func() {
		stop()
		if err := <-watched; err != nil {
			t.Errorf("watch: %v", err)
		}
	}()
	// Registered and active: the daemon lists it.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var clients []ipc.Client
		must(t, c.Call(ipc.OpClients, nil, &clients))
		if len(clients) == 1 && clients[0].Kind == ipc.KindTerminal && clients[0].Presence == ipc.PresenceActive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("clients = %+v", clients)
		}
	}

	at := time.Now()
	for i, ev := range []struct{ name, payload string }{
		{"SessionStart", `{"session_id":"c-1","source":"startup","cwd":"/w/api"}`},
		{"UserPromptSubmit", `{"session_id":"c-1","prompt":"go"}`},
		{"PermissionRequest", `{"session_id":"c-1","tool_name":"Bash"}`},
	} {
		must(t, c.Call(ipc.OpEvent, ipc.HookEvent{Name: ev.name, At: at.Add(time.Duration(i) * time.Second), Payload: json.RawMessage(ev.payload)}, nil))
	}
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(out.String(), "needs you: permission: Bash"); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("watch printed %q", out.String())
		}
	}
	if !strings.HasPrefix(out.String(), "\a") {
		t.Errorf("no bell: %q", out.String())
	}
	select {
	case n := <-fellBack:
		t.Errorf("the OS showed it too: %+v", n)
	default:
	}
}

// acmd --terminal runs the terminal UI as this machine's terminal client,
// with the daemon's ops, its own event stream and the attach acmd attach uses.
func TestTerminalUIRunsAsATerminalClient(t *testing.T) {
	f := newFakeDaemon(t)
	hellos := helloRecorder(f)
	f.reply(ipc.OpClientPresence, nil)
	f.reply(ipc.OpSnapshot, attachSnapshot())
	sockets := fakeAttach(t, true, nil)
	fakeRunning(t, true)
	fakeUIEnv(t, uiEnv{goos: "darwin", terminal: true, hasApp: true})
	var got tui.Options
	orig := runTerminalUIProgram
	runTerminalUIProgram = func(o tui.Options, _ io.Reader, _ io.Writer) error {
		got = o
		var snap ipc.Snapshot
		if err := o.Daemon.Call(ipc.OpSnapshot, nil, &snap); err != nil {
			return err
		}
		return o.Attach(*snap.Sessions[0]).Run()
	}
	t.Cleanup(func() { runTerminalUIProgram = orig })

	mustRun(t, "--terminal")
	if !strings.HasPrefix(got.Me, "terminal-") || got.Subscribe == nil {
		t.Errorf("options: %+v", got)
	}
	if len(*sockets) != 1 || (*sockets)[0] != f.p.HostSocket("L1") {
		t.Errorf("attached to %v", *sockets)
	}
	if hs := hellos(); len(hs) == 0 || hs[0] == nil || hs[0].Kind != ipc.KindTerminal {
		t.Errorf("hellos %+v", hs)
	}
	fakeRunning(t, false)
	_, err := acmd(t, "--terminal")
	errContains(t, err, `"api" isn't running`)
}
