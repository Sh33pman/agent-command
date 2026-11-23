package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/host"
	"github.com/brettmostert/agent-command/internal/remote/hostapi"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/remote/wire"
	"github.com/brettmostert/agent-command/internal/term"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// shortDir is a temp dir short enough for unix sockets (macOS allows 104
// bytes).
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "acmdc")
	must(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// daemon is a host's daemon in memory (hostapi.Backend).
type daemon struct {
	mu       sync.Mutex
	sessions []*core.Session
	groups   []*core.Group
	sets     []ipc.Set
	inbox    []core.InboxItem
	notices  []ipc.Notice
	sockets  map[string]string
	created  int
	rev      uint64
	ch       chan struct{}
	file     *config.File
	called   []string // ops run through call, with the UI that asked
}

func newDaemon() *daemon { return &daemon{sockets: map[string]string{}, ch: make(chan struct{})} }

func (b *daemon) State(context.Context) (hostapi.State, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := hostapi.State{Inbox: slices.Clone(b.inbox), Notices: slices.Clone(b.notices)}
	for _, s := range b.sessions {
		c := *s
		st.Sessions = append(st.Sessions, &c)
	}
	return st, nil
}

func (b *daemon) Changes() (uint64, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rev, b.ch
}

func (b *daemon) change(fn func()) {
	b.mu.Lock()
	fn()
	b.rev++
	close(b.ch)
	b.ch = make(chan struct{})
	b.mu.Unlock()
}

func (b *daemon) find(id string) *core.Session {
	for _, s := range b.sessions {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (b *daemon) session(id string) core.Session {
	b.mu.Lock()
	defer b.mu.Unlock()
	return *b.find(id)
}

func (b *daemon) add(s core.Session) { b.change(func() { b.sessions = append(b.sessions, &s) }) }

func (b *daemon) edit(id string, fn func(*core.Session)) { b.change(func() { fn(b.find(id)) }) }

func (b *daemon) NewSession(_ context.Context, req ipc.NewRequest) (*core.Session, error) {
	b.mu.Lock()
	b.created++
	id := "new" + strconv.Itoa(b.created)
	b.mu.Unlock()
	s := core.Session{ID: id, Name: req.Name, Cwd: req.Cwd, HarnessKind: req.HarnessKind, Status: core.StatusStarting,
		Remote: req.Remote != nil && *req.Remote, CreatedAt: time.Now()}
	b.add(s)
	return &s, nil
}

func (b *daemon) Rename(_ context.Context, id, name string) error {
	b.edit(id, func(s *core.Session) { s.Name = name })
	return nil
}

func (b *daemon) End(_ context.Context, id string, finish bool) error {
	b.edit(id, func(s *core.Session) {
		if finish {
			s.Status = core.StatusFinished
		} else {
			s.Status = core.StatusArchived
		}
	})
	return nil
}

func (b *daemon) HostSocket(id string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.sockets[id]; ok {
		return s
	}
	return "/nonexistent/" + id + ".sock"
}

// The host's settings live in its remoteHost's file (see newRemoteHost).
func (b *daemon) Config() ipc.ConfigState { return ipc.ConfigOf(b.file) }

// Call answers snapshot with the host's state, and any other op with the op
// it was asked to run.
func (b *daemon) Call(ctx context.Context, op string, data json.RawMessage) (json.RawMessage, error) {
	b.mu.Lock()
	ui := ""
	if c := ipc.ClientOf(ctx); c != nil {
		ui = " from " + c.ID
	}
	b.called = append(b.called, op+" "+string(data)+ui)
	b.mu.Unlock()
	switch op {
	case ipc.OpSnapshot:
		return json.Marshal(b.snapshot())
	case ipc.OpNew:
		var req ipc.NewRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return nil, err
		}
		s, err := b.NewSession(context.Background(), req)
		if err != nil {
			return nil, err
		}
		return json.Marshal(ipc.SessionOf(s))
	}
	return json.Marshal(map[string]string{"op": op})
}

// Allow checks as the daemon does: the catalog's rule.
func (b *daemon) Allow(ctx context.Context, op string) error { return hostapi.AllowByCatalog(ctx, op) }

func (b *daemon) SetConfig(set ipc.ConfigSet) (ipc.ConfigState, error) {
	_, err := b.file.SetIf(set.Values, set.Versions)
	return ipc.ConfigOf(b.file), err
}

func (b *daemon) Harnesses() ipc.Harnesses {
	return ipc.Harnesses{Default: "hc", Terminal: true, Harnesses: []ipc.Harness{
		{ID: "hc", Name: "Claude", Kind: core.HarnessClaude, Title: "Claude", Installed: true}}}
}

// remoteHost is another machine running Hosting with the host API.
type remoteHost struct {
	t    *testing.T
	name string
	dir  string
	home string
	cfg  string
	file *config.File
	data *host.MemoryData
	b    *daemon
	h    *host.Host
}

func newRemoteHost(t *testing.T, name string) *remoteHost {
	t.Helper()
	dir := shortDir(t)
	r := &remoteHost{t: t, name: name, dir: dir, home: filepath.Join(dir, "home"), cfg: filepath.Join(dir, "config.toml"), b: newDaemon()}
	must(t, os.Mkdir(r.home, 0o750))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	r.file, r.data = config.OpenFile(r.cfg), &host.MemoryData{}
	r.b.file = r.file
	_, err = r.file.Set(map[string]any{"remote.host.enabled": true, "remote.host.port": port, "remote.host.port_fallback": false})
	must(t, err)
	r.start()
	return r
}

// start runs Hosting, as the host's daemon does when it starts.
func (r *remoteHost) start() {
	r.t.Helper()
	h := host.New(host.Options{Settings: func() config.Remote { return r.file.Config().Remote }, Data: r.data,
		IdentityDir: filepath.Join(r.dir, "id"), Version: "1.0.0"})
	hostapi.Register(h, r.b, hostapi.Options{Home: r.home})
	h.Reload()
	r.h = h
	r.t.Cleanup(h.Close)
	if st := h.Status(); !st.Listening {
		r.t.Fatalf("%s isn't listening: %+v", r.name, st)
	}
}

// restart stops Hosting and starts it again on the same port, as the host's
// daemon does when it's killed and comes back.
func (r *remoteHost) restart(down time.Duration) {
	r.h.Close()
	time.Sleep(down)
	r.start()
}

func (r *remoteHost) addr() string        { return r.h.Status().Address }
func (r *remoteHost) fingerprint() string { return r.h.Status().Fingerprint }

func (r *remoteHost) authorize(fp string) {
	r.t.Helper()
	_, err := r.h.Authorize(fp, "laptop")
	must(r.t, err)
	_, err = r.h.SetScope("laptop", config.ScopeAdmin) // as pairings made before scopes are
	must(r.t, err)
}

// terminal runs a shell under a term.Server for session id.
func (r *remoteHost) terminal(id string) string {
	r.t.Helper()
	sock := filepath.Join(r.dir, id+".sock")
	ready := make(chan struct{})
	srv := &term.Server{Socket: sock, Shell: []string{"/bin/sh"}, Dir: r.dir, Env: []string{"PS1=$ ", "PATH=/usr/bin:/bin"},
		Ready: func() { close(ready) }}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx) }()
	<-ready
	r.t.Cleanup(func() {
		cancel()
		logErr(r.t, <-served)
	})
	r.b.mu.Lock()
	r.b.sockets[id] = sock
	r.b.mu.Unlock()
	return sock
}

// local is the client's daemon in memory (Backend): the notices and changes
// the client passes on.
type local struct {
	mu      sync.Mutex
	notices []string
	changes int
}

func newLocal() *local { return &local{} }

func (l *local) Notice(id, host string, n ipc.Notice) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.notices = append(l.notices, id+" "+host+" "+n.Kind+" "+n.Title)
}

func (l *local) Changed() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.changes++
}

func (l *local) noticesSoFar() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.notices...)
}

// dials counts the client's dials.
type dials struct {
	mu sync.Mutex
	n  int
}

func (d *dials) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.n
}

// laptop is the client: its config.toml, identity, daemon and Client.
type laptop struct {
	t     *testing.T
	dir   string
	cfg   string
	idDir string
	file  *config.File
	data  *MemoryData
	b     *local
	c     *Client
	dials *dials
	o     Options
}

func newLaptop(t *testing.T, tweak ...func(*Options)) *laptop {
	t.Helper()
	dir := shortDir(t)
	l := &laptop{t: t, dir: dir, cfg: filepath.Join(dir, "config.toml"), idDir: filepath.Join(dir, "id"), b: newLocal(), dials: &dials{}}
	must(t, os.WriteFile(l.cfg, []byte("# the laptop's own\n"), 0o600))
	l.file, l.data = config.OpenFile(l.cfg), &MemoryData{}
	// As the daemon does: config.toml as it is on disk, hand edits too.
	settings := func() config.Remote { l.file.ReloadIfChanged(); return l.file.Config().Remote }
	l.o = Options{Settings: settings, Data: l.data, IdentityDir: l.idDir, Version: "1.0.0",
		Socket:     func(id string) string { return filepath.Join(dir, "h-"+id+".sock") },
		MinBackoff: 20 * time.Millisecond, MaxBackoff: 100 * time.Millisecond, DialTimeout: 3 * time.Second,
		IdleAfter: time.Minute}
	for _, fn := range tweak {
		fn(&l.o)
	}
	inner := l.o.Dial
	l.o.Dial = func(ctx context.Context, addr string, id *identity.Identity, pinned string, hello wire.Hello) (*wire.Conn, error) {
		l.dials.mu.Lock()
		l.dials.n++
		l.dials.mu.Unlock()
		if inner != nil {
			return inner(ctx, addr, id, pinned, hello)
		}
		return wire.Dial(ctx, addr, id, pinned, hello)
	}
	l.start()
	return l
}

func (l *laptop) start() {
	l.t.Helper()
	l.c = New(l.o, l.b)
	must(l.t, l.c.Start(context.Background()))
	l.t.Cleanup(l.c.Close)
}

func (l *laptop) fingerprint() string {
	l.t.Helper()
	id, err := identity.Ensure(l.idDir)
	must(l.t, err)
	return id.Fingerprint()
}

// know adds r as a known host, pinned by its fingerprint.
func (l *laptop) know(r *remoteHost) {
	l.t.Helper()
	_, err := l.c.AddHost(ipc.RemoteHostEdit{Name: r.name, Address: r.addr(), Fingerprint: r.fingerprint()})
	must(l.t, err)
}

func (l *laptop) state(name string) ipc.RemoteHost {
	l.t.Helper()
	st, err := l.c.Host(name)
	must(l.t, err)
	return st
}

func (l *laptop) waitState(name, state string) ipc.RemoteHost {
	l.t.Helper()
	var st ipc.RemoteHost
	eventually(l.t, name+" "+state, func() bool {
		st = l.state(name)
		return st.State == state
	})
	return st
}

func ctx5() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = cancel // the test ends first
	return ctx
}

// buf is output tests read while it's written.
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

// pane is term.Attach on a socket, as `acmd host attach` runs it.
type pane struct {
	out    *buf
	in     chan []byte
	size   chan [2]int
	done   chan error
	cancel context.CancelFunc
}

func attachPane(t *testing.T, sock string) *pane {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	p := &pane{out: &buf{}, in: make(chan []byte, 8), size: make(chan [2]int, 1), done: make(chan error, 1), cancel: cancel}
	go func() { p.done <- term.Attach(ctx, sock, p.in, p.size, 30, 100, p.out) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-p.done:
			if err != nil && !errors.Is(err, term.ErrEnded) {
				t.Errorf("attach: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("the pane didn't end")
		}
	})
	return p
}

// logErr notes an error a test doesn't mind.
func logErr(t *testing.T, err error) {
	if err != nil {
		t.Log(err)
	}
}

// snapshot is the host's state as its socket would send it.
func (b *daemon) snapshot() ipc.Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return ipc.Snapshot{Sessions: ipc.SessionsOf(b.sessions), Groups: ipc.GroupsOf(b.groups), Sets: append([]ipc.Set(nil), b.sets...),
		Inbox: append([]core.InboxItem(nil), b.inbox...)}
}

// Subscribe sends the host's state on every change, as a snapshot, and its
// new notices as they come.
func (b *daemon) Subscribe(ctx context.Context, _ ipc.SubscribeRequest, send func(ipc.Event) error) error {
	var rev, sent uint64
	for {
		_, next := b.Changes()
		snap := b.snapshot()
		rev++
		if err := send(ipc.Event{Rev: rev, Type: ipc.EventSnapshot, Snapshot: &snap}); err != nil {
			return nil
		}
		b.mu.Lock()
		notices := append([]ipc.Notice(nil), b.notices...)
		b.mu.Unlock()
		for _, n := range notices {
			if n.Seq > sent {
				sent = n.Seq
				data, _ := json.Marshal(n)
				rev++
				if err := send(ipc.Event{Rev: rev, Type: ipc.EventAdded, Entity: ipc.EntityNotice, ID: fmt.Sprint(n.Seq), Data: data}); err != nil {
					return nil
				}
			}
		}
		select {
		case <-next:
		case <-ctx.Done():
			return nil
		}
	}
}
