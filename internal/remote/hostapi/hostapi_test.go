package hostapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/host"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/remote/wire"
	"github.com/brettmostert/agent-command/internal/term"
)

// backend is a daemon in memory.
type backend struct {
	mu        sync.Mutex
	sessions  []*core.Session
	inbox     []core.InboxItem
	notices   []ipc.Notice
	sockets   map[string]string
	installed []string
	def       string
	created   []ipc.NewRequest
	stateErr  error
	file      *config.File

	rev uint64
	ch  chan struct{}
}

func newBackend() *backend {
	return &backend{sockets: map[string]string{}, installed: []string{core.HarnessClaude, core.HarnessTerminal}, ch: make(chan struct{})}
}

func (b *backend) State(context.Context) (State, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stateErr != nil {
		return State{}, b.stateErr
	}
	st := State{Inbox: slices.Clone(b.inbox), Notices: slices.Clone(b.notices)}
	for _, s := range b.sessions {
		c := *s
		st.Sessions = append(st.Sessions, &c)
	}
	return st, nil
}

func (b *backend) Changes() (uint64, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rev, b.ch
}

// change edits the state, and tells whoever waits.
func (b *backend) change(fn func()) {
	b.mu.Lock()
	fn()
	b.rev++
	close(b.ch)
	b.ch = make(chan struct{})
	b.mu.Unlock()
}

func (b *backend) session(id string) *core.Session {
	for _, s := range b.sessions {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (b *backend) add(s core.Session) {
	b.change(func() { b.sessions = append(b.sessions, &s) })
}

func (b *backend) edit(id string, fn func(*core.Session)) {
	b.change(func() { fn(b.session(id)) })
}

func (b *backend) NewSession(_ context.Context, req ipc.NewRequest) (*core.Session, error) {
	if req.Name == "fail" {
		return nil, errors.New("boom")
	}
	s := &core.Session{ID: "new1", Name: req.Name, Cwd: req.Cwd, Harness: req.Harness, Status: core.StatusStarting,
		Remote: req.Remote != nil && *req.Remote, CreatedAt: time.Now()}
	b.change(func() {
		b.created = append(b.created, req)
		b.sessions = append(b.sessions, s)
	})
	c := *s
	return &c, nil
}

func (b *backend) Rename(_ context.Context, id, name string) error {
	b.edit(id, func(s *core.Session) { s.Name = name })
	return nil
}

func (b *backend) End(_ context.Context, id string, finish bool) error {
	b.edit(id, func(s *core.Session) {
		if finish {
			s.Status = core.StatusFinished
		} else {
			s.Status = core.StatusArchived
		}
	})
	return nil
}

func (b *backend) HostSocket(id string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.sockets[id]; ok {
		return s
	}
	return "/nonexistent/" + id + ".sock"
}

func (b *backend) Config() ipc.ConfigState { return ipc.ConfigOf(b.file) }

func (b *backend) SetConfig(values map[string]any) (ipc.ConfigState, error) {
	_, err := b.file.Set(values)
	return ipc.ConfigOf(b.file), err
}

func (b *backend) Harnesses() ([]string, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.installed), b.def
}

// fixture is a host with the API, and a client connected to it.
type fixture struct {
	t    *testing.T
	dir  string
	home string
	b    *backend
	h    *host.Host
	c    *wire.Conn
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// shortDir is a temp dir short enough for a unix socket (macOS allows 104
// bytes).
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "acmdh")
	must(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := shortDir(t)
	f := &fixture{t: t, dir: dir, home: filepath.Join(dir, "home"), b: newBackend()}
	must(t, os.Mkdir(f.home, 0o750))
	cfg := filepath.Join(dir, "config.toml")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	f.b.file = config.OpenFile(cfg)
	_, err = f.b.file.Set(map[string]any{"remote.host.enabled": true, "remote.host.port": port})
	must(t, err)
	f.h = host.New(host.Options{Settings: func() config.Remote { return f.b.file.Config().Remote },
		IdentityDir: filepath.Join(dir, "id"), Version: "1.0.0"})
	t.Cleanup(f.h.Close)
	Register(f.h, f.b, Options{Home: f.home})
	f.h.Reload()
	client, err := identity.Ensure(filepath.Join(dir, "client"))
	must(t, err)
	_, err = f.h.Authorize(client.Fingerprint(), "laptop")
	must(t, err)
	st := f.h.Status()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f.c, err = wire.Dial(ctx, st.Address, client, st.Fingerprint, wire.Hello{Version: "1.0.0", Name: "laptop"})
	must(t, err)
	t.Cleanup(func() { _ = f.c.Close() })
	return f
}

func (f *fixture) call(op string, args, reply any) error {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return f.c.Call(ctx, op, args, reply)
}

func (f *fixture) open(op string, args any) (*wire.Stream, error) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return f.c.Open(ctx, op, args, nil)
}

func wantCode(t *testing.T, what string, err error, code string) {
	t.Helper()
	if !wire.IsCode(err, code) {
		t.Errorf("%s: %v, want code %s", what, err, code)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

var t0 = time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)

// three sessions: one Remote, one archived Remote, one local.
func (f *fixture) seed() {
	f.b.add(core.Session{ID: "r1", Name: "api", Cwd: "/w/api", Harness: core.HarnessClaude, Status: core.StatusWaiting, StatusDetail: "permission",
		Remote: true, CreatedAt: t0, LastEventAt: t0.Add(time.Hour)})
	f.b.add(core.Session{ID: "r2", Name: "old", Cwd: "/w/old", Status: core.StatusArchived, Remote: true, CreatedAt: t0})
	f.b.add(core.Session{ID: "l1", Name: "local", Cwd: "/w/local", Status: core.StatusBusy, CreatedAt: t0, LastEventAt: t0.Add(2 * time.Hour)})
	f.b.change(func() {
		f.b.inbox = []core.InboxItem{
			{ID: 1, SessionID: "r1", Kind: core.InboxPermission, Text: "Bash", Options: core.PermissionOptions, State: core.InboxOpen, At: t0},
			{ID: 2, SessionID: "l1", Kind: core.InboxQuestion, Text: "secret", State: core.InboxOpen, At: t0},
		}
	})
}

func TestListShowsOnlyRemoteSessions(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.b.add(core.Session{ID: "r3", Name: "web", Cwd: "/w/web", Status: core.StatusDone, Remote: true, CreatedAt: t0.Add(3 * time.Hour)})
	var l api.SessionList
	must(t, f.call(api.OpSessionsList, nil, &l))
	if len(l.Sessions) != 2 || l.Sessions[0].ID != "r3" || l.Sessions[1].ID != "r1" {
		t.Fatalf("list = %+v", l)
	}
	s := l.Sessions[1]
	want := api.Session{ID: "r1", Name: "api", Harness: "claude", Dir: "/w/api", Status: "waiting", StatusDetail: "permission",
		NeedsYou: 1, Inbox: 1, LastActivity: t0.Add(time.Hour), Created: t0}
	if s != want {
		t.Errorf("r1 = %+v, want %+v", s, want)
	}
	if l.Sessions[0].Harness != "claude" || !l.Sessions[0].LastActivity.Equal(t0.Add(3*time.Hour)) {
		t.Errorf("r3 = %+v", l.Sessions[0])
	}

	f.b.change(func() { f.b.stateErr = errors.New("db gone") })
	wantCode(t, "a broken store", f.call(api.OpSessionsList, nil, &l), wire.CodeInternal)
}

func TestNonRemoteSessionsAreNotFound(t *testing.T) {
	f := newFixture(t)
	f.seed()
	for _, id := range []string{"l1", "r2", "nope"} {
		wantCode(t, "rename "+id, f.call(api.OpSessionRename, api.Rename{ID: id, Name: "x"}, nil), wire.CodeNotFound)
		wantCode(t, "end "+id, f.call(api.OpSessionEnd, api.End{ID: id}, nil), wire.CodeNotFound)
		_, err := f.open(api.OpSessionAttach, api.SessionRef{ID: id})
		wantCode(t, "attach "+id, err, wire.CodeNotFound)
	}
	// Not found reads the same whether the session exists or not.
	err1 := f.call(api.OpSessionEnd, api.End{ID: "l1"}, nil)
	err2 := f.call(api.OpSessionEnd, api.End{ID: "zz"}, nil)
	if strings.ReplaceAll(err1.Error(), "l1", "zz") != err2.Error() {
		t.Errorf("errors differ: %q, %q", err1, err2)
	}
	if s := f.b.session("l1"); s.Name != "local" || s.Status != core.StatusBusy {
		t.Errorf("the local session changed: %+v", s)
	}
}

func TestRenameAndEnd(t *testing.T) {
	f := newFixture(t)
	f.seed()
	var s api.Session
	must(t, f.call(api.OpSessionRename, api.Rename{ID: "r1", Name: "  api v2 "}, &s))
	if s.Name != "api v2" || f.b.session("r1").Name != "api v2" {
		t.Errorf("renamed: %+v", s)
	}
	wantCode(t, "an empty name", f.call(api.OpSessionRename, api.Rename{ID: "r1", Name: " "}, nil), wire.CodeBadRequest)
	wantCode(t, "bad arguments", f.call(api.OpSessionRename, json.RawMessage(`[1]`), nil), wire.CodeBadRequest)

	must(t, f.call(api.OpSessionEnd, api.End{ID: "r1", Finish: true}, nil))
	if st := f.b.session("r1").Status; st != core.StatusFinished {
		t.Errorf("finished: %v", st)
	}
	must(t, f.call(api.OpSessionEnd, api.End{ID: "r1"}, nil))
	if st := f.b.session("r1").Status; st != core.StatusArchived {
		t.Errorf("ended: %v", st)
	}
	wantCode(t, "end again", f.call(api.OpSessionEnd, api.End{ID: "r1"}, nil), wire.CodeNotFound)
}

func TestNewSession(t *testing.T) {
	f := newFixture(t)
	must(t, os.MkdirAll(filepath.Join(f.home, "src", "app"), 0o750))
	must(t, os.WriteFile(filepath.Join(f.home, "file"), nil, 0o600))

	var s api.Session
	must(t, f.call(api.OpSessionNew, api.NewSession{Dir: "~/src/app", Name: " app ", Prompt: "hi", Args: []string{"--x"}}, &s))
	app := filepath.Join(f.home, "src", "app")
	if s.ID != "new1" || s.Dir != app || s.Harness != core.HarnessClaude || s.Name != "app" {
		t.Errorf("new = %+v", s)
	}
	r := f.b.created[0]
	if r.Remote == nil || !*r.Remote || r.Cwd != app || r.Harness != core.HarnessClaude || r.Prompt != "hi" || !slices.Equal(r.Args, []string{"--x"}) {
		t.Errorf("request = %+v", r)
	}
	if !f.b.session("new1").Remote {
		t.Error("the new session isn't Remote")
	}

	// The host's default harness, when it's installed.
	f.b.def = core.HarnessTerminal
	must(t, f.call(api.OpSessionNew, api.NewSession{Dir: "src"}, &s))
	if r := f.b.created[1]; r.Harness != core.HarnessTerminal || r.Cwd != filepath.Join(f.home, "src") {
		t.Errorf("default harness: %+v", r)
	}

	for _, tc := range []struct {
		what string
		n    api.NewSession
		code string
	}{
		{"no such folder", api.NewSession{Dir: "/nonexistent/x"}, wire.CodeNotFound},
		{"a file", api.NewSession{Dir: "~/file"}, api.CodeNotFolder},
		{"an unknown harness", api.NewSession{Dir: "~", Harness: "vim"}, wire.CodeBadRequest},
		{"a harness that isn't installed", api.NewSession{Dir: "~", Harness: core.HarnessAntigravity}, wire.CodeNotFound},
		{"the daemon fails", api.NewSession{Dir: "~", Name: "fail"}, wire.CodeInternal},
	} {
		wantCode(t, tc.what, f.call(api.OpSessionNew, tc.n, nil), tc.code)
	}
	wantCode(t, "bad arguments", f.call(api.OpSessionNew, json.RawMessage(`"x"`), nil), wire.CodeBadRequest)
}

func TestHarnesses(t *testing.T) {
	f := newFixture(t)
	var h api.Harnesses
	f.b.def = core.HarnessAntigravity // chosen, but not installed
	must(t, f.call(api.OpHarnessesList, nil, &h))
	if len(h.Harnesses) != 2 || h.Harnesses[0] != (api.Harness{Name: "claude", Title: "Claude Code"}) ||
		h.Harnesses[1] != (api.Harness{Name: "terminal", Title: "Terminal"}) || h.Default != "" {
		t.Errorf("harnesses = %+v", h)
	}
	f.b.installed = []string{core.HarnessAntigravity, "other"}
	must(t, f.call(api.OpHarnessesList, nil, &h))
	if len(h.Harnesses) != 1 || h.Harnesses[0].Title != "Antigravity CLI" || h.Default != core.HarnessAntigravity {
		t.Errorf("harnesses = %+v", h)
	}
}

func TestFolders(t *testing.T) {
	f := newFixture(t)
	for _, d := range []string{"b", "A", ".git", "c/deeper"} {
		must(t, os.MkdirAll(filepath.Join(f.home, d), 0o750))
	}
	must(t, os.WriteFile(filepath.Join(f.home, "notes.txt"), nil, 0o600))
	must(t, os.Symlink(filepath.Join(f.home, "c"), filepath.Join(f.home, "link")))
	must(t, os.Symlink(filepath.Join(f.home, "notes.txt"), filepath.Join(f.home, "filelink")))

	names := func(l api.FolderList) string {
		var out []string
		for _, d := range l.Folders {
			n := d.Name
			if d.Hidden {
				n += "(h)"
			}
			out = append(out, n)
		}
		return strings.Join(out, ",")
	}
	list := func(p api.FolderPath) api.FolderList {
		t.Helper()
		var l api.FolderList
		must(t, f.call(api.OpFoldersList, p, &l))
		return l
	}
	l := list(api.FolderPath{Path: "~"})
	if got := names(l); got != "A,b,c,link" || l.Path != f.home || l.Home != f.home || l.Parent != filepath.Dir(f.home) {
		t.Errorf("home = %s %+v", got, l)
	}
	l = list(api.FolderPath{Hidden: true})
	if got := names(l); got != "A,b,c,link,.git(h)" {
		t.Errorf("with hidden = %s", got)
	}
	l = list(api.FolderPath{Path: "c"})
	if got := names(l); got != "deeper" || l.Folders[0].Path != filepath.Join(f.home, "c", "deeper") {
		t.Errorf("c = %s %+v", got, l)
	}
	var root api.FolderList
	must(t, f.call(api.OpFoldersList, api.FolderPath{Path: "/"}, &root))
	if root.Path != "/" || root.Parent != "" {
		t.Errorf("root = %+v", root)
	}
	wantCode(t, "list a missing folder", f.call(api.OpFoldersList, api.FolderPath{Path: "~/nope"}, nil), wire.CodeNotFound)
	wantCode(t, "list a file", f.call(api.OpFoldersList, api.FolderPath{Path: "notes.txt"}, nil), api.CodeNotFolder)
	wantCode(t, "bad arguments", f.call(api.OpFoldersList, json.RawMessage(`1`), nil), wire.CodeBadRequest)

	var made api.Folder
	must(t, f.call(api.OpFoldersMkdir, api.FolderPath{Path: "~/new"}, &made))
	if made.Path != filepath.Join(f.home, "new") || made.Name != "new" {
		t.Errorf("mkdir = %+v", made)
	}
	if info, err := os.Stat(made.Path); err != nil || !info.IsDir() {
		t.Errorf("not made: %v", err)
	}
	wantCode(t, "mkdir again", f.call(api.OpFoldersMkdir, api.FolderPath{Path: "new"}, nil), api.CodeExists)
	wantCode(t, "mkdir without a parent", f.call(api.OpFoldersMkdir, api.FolderPath{Path: "x/y"}, nil), wire.CodeNotFound)
	wantCode(t, "mkdir nothing", f.call(api.OpFoldersMkdir, api.FolderPath{}, nil), wire.CodeBadRequest)
	wantCode(t, "bad arguments", f.call(api.OpFoldersMkdir, json.RawMessage(`1`), nil), wire.CodeBadRequest)

	if os.Geteuid() != 0 { // root may go anywhere
		locked := filepath.Join(f.home, "locked")
		must(t, os.Mkdir(locked, 0o750))
		must(t, os.Chmod(locked, 0))
		t.Cleanup(func() { _ = os.Chmod(locked, 0o750) })
		err := f.call(api.OpFoldersList, api.FolderPath{Path: locked}, nil)
		wantCode(t, "list a locked folder", err, api.CodePermission)
		if err != nil && !strings.Contains(err.Error(), "no permission") {
			t.Errorf("message: %v", err)
		}
		wantCode(t, "mkdir in a locked folder", f.call(api.OpFoldersMkdir, api.FolderPath{Path: "locked/x"}, nil), api.CodePermission)
	}
}

func TestRecentFolders(t *testing.T) {
	f := newFixture(t)
	a, b := filepath.Join(f.home, "a"), filepath.Join(f.home, ".b")
	must(t, os.Mkdir(a, 0o750))
	must(t, os.Mkdir(b, 0o750))
	f.b.add(core.Session{ID: "1", Cwd: a, CreatedAt: t0, LastEventAt: t0.Add(time.Hour)})
	f.b.add(core.Session{ID: "2", Cwd: b, CreatedAt: t0.Add(2 * time.Hour)})
	f.b.add(core.Session{ID: "3", Cwd: a, CreatedAt: t0})
	f.b.add(core.Session{ID: "4", Cwd: filepath.Join(f.home, "gone"), CreatedAt: t0.Add(3 * time.Hour)})
	f.b.add(core.Session{ID: "5", CreatedAt: t0.Add(4 * time.Hour)})
	var l api.FolderList
	must(t, f.call(api.OpFoldersRecent, nil, &l))
	if len(l.Folders) != 2 || l.Folders[0] != (api.Folder{Name: ".b", Path: b, Hidden: true}) || l.Folders[1].Path != a || l.Home != f.home {
		t.Errorf("recent = %+v", l)
	}
	f.b.change(func() { f.b.stateErr = errors.New("db gone") })
	wantCode(t, "a broken store", f.call(api.OpFoldersRecent, nil, &l), wire.CodeInternal)
}

func TestResolve(t *testing.T) {
	a := &API{home: "/home/me"}
	for _, tc := range [][2]string{{"", "/home/me"}, {"\t~\n", "/home/me"}, {"~/src", "/home/me/src"}, {"src/x", "/home/me/src/x"},
		{"/etc/../tmp", "/tmp"}, {"~other", "/home/me/~other"}} {
		in, want := tc[0], tc[1]
		if got := a.resolve(in); got != want {
			t.Errorf("resolve(%q) = %q, want %q", in, got, want)
		}
	}
	if err := fsError("/x", errors.New("odd")); !wire.IsCode(err, wire.CodeInternal) {
		t.Errorf("an odd error: %v", err)
	}
	if err := fsError("/x", &os.PathError{Op: "open", Path: "/x", Err: errors.New("odd")}); !wire.IsCode(err, wire.CodeInternal) ||
		!strings.Contains(err.Error(), "/x: odd") {
		t.Errorf("an odd path error: %v", err)
	}
}

func TestRegisterDefaultsHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	a := Register(nopRegistrar{}, newBackend(), Options{})
	if a.home != home {
		t.Errorf("home = %q, want %q", a.home, home)
	}
}

type nopRegistrar struct{}

func (nopRegistrar) Handle(string, wire.Handler)             {}
func (nopRegistrar) HandleStream(string, wire.StreamHandler) {}
func (nopRegistrar) Attach(*wire.Conn, string, io.Closer) func() {
	return func() {}
}

// terminal runs a shell under a term.Server for session id.
func (f *fixture) terminal(id string) string {
	f.t.Helper()
	sock := filepath.Join(f.dir, id+".sock")
	ready := make(chan struct{})
	srv := &term.Server{Socket: sock, Shell: []string{"/bin/sh"}, Dir: f.dir, Env: []string{"PS1=$ ", "PATH=/usr/bin:/bin"},
		Ready: func() { close(ready) }}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx) }()
	<-ready
	f.t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			f.t.Errorf("serve: %v", err)
		}
	})
	f.b.mu.Lock()
	f.b.sockets[id] = sock
	f.b.mu.Unlock()
	return sock
}

// proxy is a local socket that relays each connection to an attach stream
// for id, as a client daemon does: panes attach to it with term.Attach.
func (f *fixture) proxy(id string) string {
	f.t.Helper()
	sock := filepath.Join(f.dir, "p-"+id+".sock")
	ln, err := net.Listen("unix", sock)
	must(f.t, err)
	f.t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				s, err := f.open(api.OpSessionAttach, api.SessionRef{ID: id})
				if err != nil {
					return
				}
				defer s.Close()
				go func() { _, _ = io.Copy(s, c); _ = s.Close() }()
				_, _ = io.Copy(c, s)
			}()
		}
	}()
	return sock
}

// buf is output that tests can read while it's written.
type buf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *buf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *buf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type pane struct {
	out    *buf
	in     chan []byte
	size   chan [2]int
	done   chan error
	cancel context.CancelFunc
}

func attachPane(t *testing.T, sock string, rows, cols int) *pane {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	p := &pane{out: &buf{}, in: make(chan []byte, 8), size: make(chan [2]int, 1), done: make(chan error, 1), cancel: cancel}
	go func() { p.done <- term.Attach(ctx, sock, p.in, p.size, rows, cols, p.out) }()
	t.Cleanup(func() {
		cancel()
		if err := <-p.done; err != nil && !errors.Is(err, term.ErrEnded) {
			t.Errorf("attach: %v", err)
		}
	})
	return p
}

func (p *pane) ended(t *testing.T, what string, within time.Duration) {
	t.Helper()
	select {
	case err := <-p.done:
		if !errors.Is(err, term.ErrEnded) {
			t.Errorf("%s: attach returned %v", what, err)
		}
		p.done <- err // for the cleanup
	case <-time.After(within):
		t.Fatalf("%s: the pane is still attached after %v", what, within)
	}
}

func (f *fixture) attached(id string) int {
	n := 0
	for _, c := range f.h.Clients() {
		for _, conn := range c.Connections {
			if slices.Contains(conn.Sessions, id) {
				n++
			}
		}
	}
	return n
}

func TestAttachRelaysTheTerminal(t *testing.T) {
	f := newFixture(t)
	f.seed()
	sock := f.terminal("r1")
	// Output from before the attach comes back in the replay.
	must(t, term.Send(sock, []byte("echo before-$((2+3))\n")))
	eventually(t, "the shell ran it", func() bool {
		text, _ := term.ScreenText(sock)
		return strings.Contains(text, "before-5")
	})

	p := attachPane(t, f.proxy("r1"), 30, 100)
	eventually(t, "the replay", func() bool { return strings.Contains(p.out.String(), "before-5") })
	eventually(t, "Hosting counts the attach", func() bool { return f.attached("r1") == 1 })

	// Input reaches the shell, and the attach's size is the terminal's.
	p.in <- []byte("stty size; echo in-$((1+1))\n")
	eventually(t, "the input's output", func() bool { return strings.Contains(p.out.String(), "in-2") })
	if !strings.Contains(p.out.String(), "30 100") {
		t.Errorf("size after attach:\n%s", p.out)
	}
	// A resize goes through.
	p.size <- [2]int{40, 120}
	time.Sleep(100 * time.Millisecond)
	p.in <- []byte("stty size\n")
	eventually(t, "the new size", func() bool { return strings.Contains(p.out.String(), "40 120") })

	// A local pane attached at the same time sees the same.
	local := attachPane(t, sock, 40, 120)
	p.in <- []byte("echo both-$((3+3))\n")
	eventually(t, "the local pane", func() bool { return strings.Contains(local.out.String(), "both-6") })

	// Hosting drops it: the pane ends within a second.
	if n := f.h.CloseSession("r1"); n != 1 {
		t.Errorf("CloseSession closed %d", n)
	}
	p.ended(t, "CloseSession", time.Second)
	eventually(t, "the attach released", func() bool { return f.attached("r1") == 0 })
	select {
	case err := <-local.done:
		t.Fatalf("the local pane ended too: %v", err)
	default:
	}
}

func TestAttachEndsWhenNoLongerVisible(t *testing.T) {
	for _, tc := range []struct {
		what string
		fn   func(*core.Session)
	}{
		{"Remote off", func(s *core.Session) { s.Remote = false }},
		{"archived", func(s *core.Session) { s.Status = core.StatusArchived }},
	} {
		t.Run(tc.what, func(t *testing.T) {
			f := newFixture(t)
			f.seed()
			f.terminal("r1")
			p := attachPane(t, f.proxy("r1"), 24, 80)
			p.in <- []byte("echo up-$((4+4))\n")
			eventually(t, "attached", func() bool { return strings.Contains(p.out.String(), "up-8") })
			f.b.edit("r1", tc.fn)
			p.ended(t, tc.what, time.Second)
		})
	}
}

func TestAttachEndsWithTheTerminal(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.terminal("r1")
	p := attachPane(t, f.proxy("r1"), 24, 80)
	p.in <- []byte("echo bye-$((5+5)); exit\n")
	p.ended(t, "the shell exited", 5*time.Second)
	if !strings.Contains(p.out.String(), "bye-10") {
		t.Errorf("the last output is lost:\n%s", p.out)
	}
}

func TestAttachToAnEndedTerminal(t *testing.T) {
	f := newFixture(t)
	f.seed()
	_, err := f.open(api.OpSessionAttach, api.SessionRef{ID: "r1"})
	wantCode(t, "no terminal", err, api.CodeNotRunning)
	_, err = f.open(api.OpSessionAttach, json.RawMessage(`[]`))
	wantCode(t, "bad arguments", err, wire.CodeBadRequest)
}

// attachFrame is term's first frame: version 1, rows, cols.
func attachFrame(rows, cols uint16) []byte {
	return []byte{'a', 0, 0, 0, 6, 0, 1, byte(rows >> 8), byte(rows), byte(cols >> 8), byte(cols)}
}

func inputFrame(s string) []byte {
	n := len(s)
	return append([]byte{'i', byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}, s...)
}

func TestAttachDropsAClientTooFarBehind(t *testing.T) {
	old := relayQueue
	relayQueue = 64 << 10
	t.Cleanup(func() { relayQueue = old })
	f := newFixture(t)
	f.seed()
	f.terminal("r1")
	s, err := f.open(api.OpSessionAttach, api.SessionRef{ID: "r1"})
	must(t, err)
	defer s.Close()
	_, err = s.Write(append(attachFrame(24, 80), inputFrame("head -c 4000000 /dev/zero | tr '\\0' x\n")...))
	must(t, err)
	// The client reads nothing: the host drops it, and the session carries on.
	eventually(t, "the attach dropped", func() bool { return f.attached("r1") == 0 })
	_, _ = io.Copy(io.Discard, s) // what was in flight, then the end
	sock := f.b.HostSocket("r1")
	if !term.Running(sock) {
		t.Error("the terminal ended")
	}
}

func TestAttachWhenTheClientGoes(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.terminal("r1")
	s, err := f.open(api.OpSessionAttach, api.SessionRef{ID: "r1"})
	must(t, err)
	_, err = s.Write(attachFrame(24, 80))
	must(t, err)
	eventually(t, "attached", func() bool { return f.attached("r1") == 1 })
	_ = s.Close()
	eventually(t, "released", func() bool { return f.attached("r1") == 0 })
}

// events reads lines of api.Event.
type events struct {
	t *testing.T
	s *wire.Stream
}

func (f *fixture) events() *events {
	f.t.Helper()
	s, err := f.open(api.OpEvents, nil)
	must(f.t, err)
	f.t.Cleanup(func() { _ = s.Close() })
	return &events{t: f.t, s: s}
}

func (e *events) next() api.Event {
	e.t.Helper()
	_ = e.s.SetDeadline(time.Now().Add(5 * time.Second))
	var ev api.Event
	must(e.t, e.s.Receive(&ev))
	return ev
}

// expect reads events until one of typ, failing on any other kind.
func (e *events) expect(typ ...string) []api.Event {
	e.t.Helper()
	var out []api.Event
	for _, want := range typ {
		ev := e.next()
		if ev.Type != want {
			e.t.Fatalf("event %d is %+v, want %s", len(out), ev, want)
		}
		out = append(out, ev)
	}
	return out
}

func TestEvents(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.b.change(func() { f.b.notices = []ipc.Notice{{Seq: 3, SessionID: "r1", Kind: "waiting"}} })
	e := f.events()
	snap := e.next()
	if snap.Type != api.EventSnapshot || len(snap.Sessions) != 1 || snap.Sessions[0].ID != "r1" ||
		len(snap.Inbox) != 1 || snap.Inbox[0].ID != 1 || snap.Inbox[0].Text != "Bash" {
		t.Fatalf("snapshot = %+v", snap)
	}

	// A local session's changes, inbox and notices don't show.
	f.b.edit("l1", func(s *core.Session) { s.Name = "secret" })
	f.b.change(func() {
		f.b.inbox = append(f.b.inbox, core.InboxItem{ID: 3, SessionID: "l1", Kind: core.InboxDone, State: core.InboxOpen})
		f.b.notices = append(f.b.notices, ipc.Notice{Seq: 4, SessionID: "l1", Kind: "done", Title: "secret"})
	})
	// The Remote session's do.
	f.b.edit("r1", func(s *core.Session) { s.Status, s.StatusDetail = core.StatusBusy, "" })
	ev := e.expect(api.EventChanged)[0]
	if ev.Session.ID != "r1" || ev.Session.Status != "busy" {
		t.Errorf("changed = %+v", ev.Session)
	}
	f.b.change(func() {
		f.b.inbox[0].State = core.InboxStale
		f.b.notices = append(f.b.notices, ipc.Notice{Seq: 5, SessionID: "r1", Kind: "done", Title: "✓ api finished", Emitter: "app"})
	})
	evs := e.expect(api.EventChanged, api.EventInbox, api.EventNotice)
	if evs[0].Session.Inbox != 0 || evs[1].ID != "r1" || len(evs[1].Inbox) != 0 ||
		*evs[2].Notice != (api.Notice{Seq: 5, SessionID: "r1", Kind: "done", Title: "✓ api finished"}) {
		t.Errorf("events = %+v", evs)
	}

	// Turned Remote: added. Turned local, or archived: removed.
	f.b.edit("l1", func(s *core.Session) { s.Remote = true })
	evs = e.expect(api.EventAdded, api.EventInbox)
	if evs[0].Session.ID != "l1" || evs[0].Session.Name != "secret" || evs[1].ID != "l1" || len(evs[1].Inbox) != 2 {
		t.Errorf("added = %+v", evs)
	}
	f.b.edit("l1", func(s *core.Session) { s.Remote = false })
	if ev := e.expect(api.EventRemoved)[0]; ev.ID != "l1" {
		t.Errorf("removed = %+v", ev)
	}
	f.b.edit("r1", func(s *core.Session) { s.Status = core.StatusArchived })
	if ev := e.expect(api.EventRemoved)[0]; ev.ID != "r1" {
		t.Errorf("removed = %+v", ev)
	}
	// A new Remote session.
	var s api.Session
	must(t, f.call(api.OpSessionNew, api.NewSession{Dir: "~"}, &s))
	if ev := e.expect(api.EventAdded)[0]; ev.Session.ID != "new1" {
		t.Errorf("added = %+v", ev)
	}

	// A broken store skips a change; the next one catches up.
	f.b.change(func() { f.b.stateErr = errors.New("db gone") })
	f.b.change(func() { f.b.stateErr = nil; f.b.session("new1").Name = "named" })
	if ev := e.expect(api.EventChanged)[0]; ev.Session.Name != "named" {
		t.Errorf("changed = %+v", ev)
	}

	// The client going ends the stream on the host too.
	_ = e.s.Close()
}

func TestEventsFailsWithABrokenStore(t *testing.T) {
	f := newFixture(t)
	f.b.stateErr = errors.New("db gone")
	_, err := f.open(api.OpEvents, nil)
	wantCode(t, "events", err, wire.CodeInternal)
}

func TestEventsResyncASlowClient(t *testing.T) {
	b := newBackend()
	b.add(core.Session{ID: "r1", Name: "a", Remote: true, Status: core.StatusBusy})
	st, _ := b.State(context.Background())
	fl := &follower{out: make(chan api.Event, 2)}
	fl.out <- fl.snapshot(st)
	// Nothing reads: two changes don't fit behind the snapshot.
	for _, name := range []string{"b", "c"} {
		b.edit("r1", func(s *core.Session) { s.Name = name })
		st, _ = b.State(context.Background())
		fl.update(st)
	}
	if len(fl.out) != 1 {
		t.Fatalf("%d events queued", len(fl.out))
	}
	ev := <-fl.out
	if ev.Type != api.EventSnapshot || len(ev.Sessions) != 1 || ev.Sessions[0].Name != "c" {
		t.Errorf("resync = %+v", ev)
	}
	// From the snapshot on, changes again.
	b.edit("r1", func(s *core.Session) { s.Name = "e" })
	st, _ = b.State(context.Background())
	fl.update(st)
	if ev := <-fl.out; ev.Type != api.EventChanged || ev.Session.Name != "e" {
		t.Errorf("after the resync = %+v", ev)
	}
}

func TestEventsOverTheNetworkWithASlowClient(t *testing.T) {
	old := eventQueue
	eventQueue = 1
	t.Cleanup(func() { eventQueue = old })
	f := newFixture(t)
	f.b.add(core.Session{ID: "r1", Remote: true, Status: core.StatusBusy})
	e := f.events()
	e.expect(api.EventSnapshot)
	// Big names, so the network's buffers fill while the client doesn't read.
	big := strings.Repeat("x", 64<<10)
	for i := range 40 {
		f.b.edit("r1", func(s *core.Session) { s.Name = big + string(rune('a'+i%26)) })
		time.Sleep(5 * time.Millisecond) // each its own change, not one for them all
	}
	f.b.edit("r1", func(s *core.Session) { s.Name = "last" })
	// The client catches up: a snapshot came in somewhere, and the last name
	// arrives.
	resynced := false
	for {
		ev := e.next()
		if ev.Type == api.EventSnapshot {
			resynced = true
			if len(ev.Sessions) == 1 && ev.Sessions[0].Name == "last" {
				break
			}
		}
		if ev.Type == api.EventChanged && ev.Session.Name == "last" {
			break
		}
	}
	if !resynced {
		t.Error("no resync")
	}
}

// Clients see and change the host's settings, never its device ones.
func TestSettings(t *testing.T) {
	f := newFixture(t)
	var st ipc.ConfigState
	must(t, f.call(api.OpConfigGet, nil, &st))
	if st.Path == "" || st.Values["remote.host.enabled"] != true || st.Problem != nil {
		t.Fatalf("config = %+v", st)
	}
	for _, s := range st.Settings {
		if s.Where != config.WhereHost {
			t.Errorf("a client sees %s", s.Key)
		}
		if _, ok := st.Values[s.Key]; !ok {
			t.Errorf("no value for %s", s.Key)
		}
	}
	if _, ok := st.Values["speech.voice"]; ok {
		t.Error("a client sees the voice")
	}
	// A change, as the client types it.
	must(t, f.call(api.OpConfigSet, ipc.ConfigSet{Values: map[string]any{"notify.cooldown": "1m", "claude_args": []string{"--x"}}}, &st))
	if st.Values["notify.cooldown"] != "1m0s" || f.b.file.Config().Notify.Cooldown != "1m0s" {
		t.Errorf("after set = %+v", st.Values["notify.cooldown"])
	}
	// Device settings are refused, with the rest.
	err := f.call(api.OpConfigSet, ipc.ConfigSet{Values: map[string]any{"speech.speed": 1.5, "editor.preset": "zed", "notify.sound": ""}}, nil)
	var ce *wire.CallError
	if !errors.As(err, &ce) || ce.Code != wire.CodeBadRequest || !strings.Contains(err.Error(), "editor.preset, speech.speed belong to the host's own machine") {
		t.Errorf("device: %v", err)
	}
	if f.b.file.Config().Notify.Sound != "default" {
		t.Error("a refused change was made")
	}
	// A bad value says what's wrong.
	err = f.call(api.OpConfigSet, ipc.ConfigSet{Values: map[string]any{"remote.host.port": "x"}}, nil)
	if !errors.As(err, &ce) || ce.Code != wire.CodeBadRequest || !strings.Contains(err.Error(), "remote.host.port") {
		t.Errorf("bad value: %v", err)
	}
	if err := f.call(api.OpConfigSet, "nope", nil); err == nil {
		t.Error("took arguments that aren't a change")
	}
}
