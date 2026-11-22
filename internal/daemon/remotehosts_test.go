package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
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
	"github.com/brettmostert/agent-command/internal/remote/hostapi"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/store"
	"github.com/brettmostert/agent-command/internal/term"
)

// otherDaemon is a Remote host's daemon, in memory.
type otherDaemon struct {
	file     *config.File
	mu       sync.Mutex
	sessions []*core.Session
	groups   []*core.Group
	sets     []ipc.Set
	inbox    []core.InboxItem
	ran      []string // the ops run on it through call
	notices  []ipc.Notice
	sockets  map[string]string
	rev      uint64
	ch       chan struct{}
}

func (b *otherDaemon) State(context.Context) (hostapi.State, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := hostapi.State{Inbox: slices.Clone(b.inbox), Notices: slices.Clone(b.notices)}
	for _, s := range b.sessions {
		c := *s
		st.Sessions = append(st.Sessions, &c)
	}
	return st, nil
}

func (b *otherDaemon) Changes() (uint64, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rev, b.ch
}

func (b *otherDaemon) change(fn func()) {
	b.mu.Lock()
	fn()
	b.rev++
	close(b.ch)
	b.ch = make(chan struct{})
	b.mu.Unlock()
}

func (b *otherDaemon) find(id string) *core.Session {
	for _, s := range b.sessions {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (b *otherDaemon) get(id string) core.Session {
	b.mu.Lock()
	defer b.mu.Unlock()
	return *b.find(id)
}

func (b *otherDaemon) NewSession(_ context.Context, req ipc.NewRequest) (*core.Session, error) {
	s := &core.Session{ID: "new1", Name: req.Name, Cwd: req.Cwd, Harness: req.Harness, Status: core.StatusStarting, Remote: true, CreatedAt: time.Now()}
	b.change(func() { b.sessions = append(b.sessions, s) })
	c := *s
	return &c, nil
}

func (b *otherDaemon) Rename(_ context.Context, id, name string) error {
	b.change(func() { b.find(id).Name = name })
	return nil
}

func (b *otherDaemon) End(_ context.Context, id string, finish bool) error {
	b.change(func() {
		if finish {
			b.find(id).Status = core.StatusFinished
		} else {
			b.find(id).Status = core.StatusArchived
		}
	})
	return nil
}

func (b *otherDaemon) HostSocket(id string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sockets[id]
}

func (b *otherDaemon) Harnesses() ([]string, string) { return []string{core.HarnessTerminal}, "" }

func (b *otherDaemon) Config() ipc.ConfigState { return ipc.ConfigOf(b.file) }

// snapshot is the host's state as its socket would send it.
func (b *otherDaemon) snapshot() ipc.Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return ipc.Snapshot{Sessions: ipc.SessionsOf(b.sessions), Groups: ipc.GroupsOf(b.groups), Sets: slices.Clone(b.sets),
		Inbox: slices.Clone(b.inbox)}
}

// Call runs what a paired device asks, as far as these tests need: the
// snapshot, a session started, resumed, renamed, archived or moved; anything
// else answers with the op.
func (b *otherDaemon) Call(ctx context.Context, op string, data json.RawMessage) (json.RawMessage, error) {
	b.mu.Lock()
	b.ran = append(b.ran, op+" "+string(data))
	b.mu.Unlock()
	switch op {
	case ipc.OpSnapshot:
		return json.Marshal(b.snapshot())
	case ipc.OpNew:
		var req ipc.NewRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return nil, err
		}
		s, err := b.NewSession(ctx, req)
		if err != nil {
			return nil, err
		}
		return json.Marshal(ipc.SessionOf(s))
	case ipc.OpResume:
		return json.Marshal(map[string]string{"op": op})
	case ipc.OpMutate:
		var m ipc.Mutation
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, err
		}
		ids := m.IDs
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
		res := ipc.Results{Results: []ipc.Result{}}
		for _, id := range ids {
			b.change(func() {
				s := b.find(id)
				if s == nil {
					res.Results = append(res.Results, ipc.Result{ID: id, Code: ipc.CodeNotFound, Error: "no session " + id})
					return
				}
				switch m.Kind {
				case ipc.MutRename:
					s.Name = m.Text
				case ipc.MutArchive:
					s.Status = core.StatusArchived
				case ipc.MutMove:
					s.GroupID = m.GroupID
				}
				res.Results = append(res.Results, ipc.Result{ID: id, OK: true})
			})
		}
		return json.Marshal(res)
	}
	return json.Marshal(map[string]string{"op": op})
}

func (b *otherDaemon) calls() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.ran)
}

func (b *otherDaemon) SetConfig(set ipc.ConfigSet) (ipc.ConfigState, error) {
	_, err := b.file.SetIf(set.Values, set.Versions)
	return ipc.ConfigOf(b.file), err
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// TestSessionsFromARemoteHost: a session added from a host is in the
// snapshot with Host set, and its socket, where a local host's would be,
// attaches with term.Attach.
func TestSessionsFromARemoteHost(t *testing.T) {
	p := testPaths(t)
	base := filepath.Dir(p.StateDir)
	p.ConfigDir = filepath.Join(base, "c")
	other := filepath.Join(base, "o")

	// The other machine: Hosting, with one Remote session running a shell.
	b := &otherDaemon{sockets: map[string]string{}, ch: make(chan struct{})}
	hostCfg := filepath.Join(other, "config.toml")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	must(t, writeSetting(hostCfg, "remote.host", "enabled", true))
	must(t, writeSetting(hostCfg, "remote.host", "port", port))
	hostFile := config.OpenFile(hostCfg)
	b.file = hostFile
	h := host.New(host.Options{Settings: func() config.Remote { return hostFile.Config().Remote }, IdentityDir: filepath.Join(other, "id")})
	t.Cleanup(h.Close)
	hostapi.Register(h, b, hostapi.Options{Home: other})
	h.Reload()
	sock := filepath.Join(other, "r1.sock")
	ready := make(chan struct{})
	srv := &term.Server{Socket: sock, Shell: []string{"/bin/sh"}, Dir: other, Env: []string{"PS1=$ ", "PATH=/usr/bin:/bin"},
		Ready: func() { close(ready) }}
	srvCtx, stopSrv := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- srv.Serve(srvCtx) }()
	<-ready
	t.Cleanup(func() { stopSrv(); logErr(t, <-served) })
	b.change(func() {
		b.sockets["r1"] = sock
		b.groups = []*core.Group{{ID: "g1", Name: "Backend", SortKey: 1, CreatedAt: time.Now()}}
		b.sessions = append(b.sessions, &core.Session{ID: "r1", Name: "api", Cwd: other, Harness: core.HarnessTerminal, GroupID: "g1",
			Status: core.StatusWaiting, Remote: true, CreatedAt: time.Now()})
		b.inbox = []core.InboxItem{{ID: 5, SessionID: "r1", Kind: core.InboxPermission, Text: "Bash", Options: core.PermissionOptions,
			State: core.InboxOpen, At: time.Now()}}
	})
	me, err := identity.Ensure(p.RemoteIdentityDir())
	must(t, err)
	_, err = h.Authorize(me.Fingerprint(), "laptop")
	must(t, err)
	_, err = h.SetScope("laptop", config.ScopeAdmin) // as pairings made before scopes are
	must(t, err)

	// This machine.
	must(t, writeSetting(p.Config(), "notify", "enabled", true))
	start(t, p, func(c *Config) {
		c.ConfigFile = p.Config()
		c.ReconcileEvery = 20 * time.Millisecond
		c.Hosts = &fakeHosts{paths: p}
	})
	c := dial(t, p)
	call := func(op string, in, out any) error {
		t.Helper()
		return c.Call(op, in, out)
	}
	var rh ipc.RemoteHost
	must(t, call(ipc.OpRemoteHostAdd, ipc.RemoteHostEdit{Name: "studio", Address: h.Status().Address, Fingerprint: h.Status().Fingerprint}, &rh))
	if rh.Name != "studio" || rh.OurFingerprint != me.Fingerprint() {
		t.Errorf("added = %+v", rh)
	}
	var browse ipc.RemoteBrowse
	must(t, call(ipc.OpRemoteBrowse, ipc.RemoteHostRef{Host: "studio"}, &browse))
	if len(browse.Sessions) != 1 || browse.Sessions[0].ID != "r1" || browse.Sessions[0].Host != "studio" {
		t.Fatalf("browse = %+v", browse)
	}
	snapshot := func() ipc.Snapshot {
		t.Helper()
		var snap ipc.Snapshot
		must(t, call(ipc.OpSnapshot, nil, &snap))
		return snap
	}
	// The host's tree shows here as it has it, with its own ids.
	var item core.InboxItem
	var s *ipc.Session
	waitFor(t, "the host's tree", func() bool {
		snap := snapshot()
		if len(snap.RemoteHosts) != 1 || snap.RemoteHosts[0].State != ipc.HostConnected || len(snap.Inbox) != 1 {
			return false
		}
		item = snap.Inbox[0]
		for _, x := range snap.Sessions {
			if x.ID == "r1" {
				s = x
			}
		}
		groups := 0
		for _, g := range snap.Groups {
			if g.Host == "studio" {
				groups++
			}
		}
		return s != nil && groups == 1
	})
	if s.Host != "studio" || s.RemoteID != "r1" || s.GroupID != "g1" || s.Name != "api" {
		t.Errorf("session = %+v", s)
	}
	if item.ID >= 0 || item.Host != "studio" || item.SessionID != "r1" {
		t.Errorf("inbox = %+v", item)
	}

	// Attached and typed in, as a local session is.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &syncBuf{}
	in := make(chan []byte, 4)
	done := make(chan error, 1)
	go func() { done <- term.Attach(ctx, p.HostSocket(s.ID), in, nil, 24, 80, out) }()
	in <- []byte("echo remote-$((20+22))\n")
	waitFor(t, "the output", func() bool { return strings.Contains(out.String(), "remote-42") })

	// The inbox: its history, read, and an answer typed through the socket.
	var items []core.InboxItem
	must(t, call(ipc.OpInbox, ipc.InboxRequest{SessionID: s.ID}, &items))
	if len(items) != 1 || items[0].ID != item.ID {
		t.Errorf("history = %+v", items)
	}
	must(t, call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutInboxRead, ItemIDs: []int64{item.ID}, Flag: true}, nil))
	must(t, call(ipc.OpInboxAnswer, ipc.InboxAnswer{ID: item.ID, Option: 3}, nil))
	waitFor(t, "the answer", func() bool { text, _ := term.ScreenText(sock); return strings.Contains(text, "$ 3") })
	if snap := snapshot(); len(snap.Inbox) != 0 {
		t.Errorf("inbox after the answer: %+v", snap.Inbox)
	}
	if err := call(ipc.OpInboxAnswer, ipc.InboxAnswer{ID: item.ID, Option: 1}, nil); err == nil {
		t.Error("answered twice")
	}

	// A notice from the host names it.
	b.change(func() {
		b.notices = []ipc.Notice{{Seq: 1, SessionID: "r1", Kind: "waiting", Title: "⚠ api needs you"}}
	})
	waitFor(t, "the notice", func() bool {
		for _, n := range snapshot().Notices {
			if n.SessionID == s.ID && n.Host == "studio" && n.Title == "⚠ api needs you · studio" {
				return true
			}
		}
		return false
	})

	// Changes go to the host by its ids, and its tree follows here.
	must(t, call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRename, ID: s.ID, Text: "renamed"}, nil))
	if b.get("r1").Name != "renamed" {
		t.Errorf("host's name %q", b.get("r1").Name)
	}
	waitFor(t, "the rename here", func() bool {
		for _, x := range snapshot().Sessions {
			if x.ID == "r1" {
				return x.Name == "renamed"
			}
		}
		return false
	})
	var res ipc.Results
	local := newSess(t, c, ipc.NewRequest{Cwd: "/w", Harness: core.HarnessTerminal, Name: "here"})
	must(t, call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRename, IDs: []string{s.ID, local.ID}, Text: "both"}, &res))
	if len(res.Results) != 2 || res.Results[0].ID != s.ID || !res.Results[0].OK || res.Results[1].ID != local.ID || !res.Results[1].OK {
		t.Errorf("a batch across here and the host: %+v", res)
	}
	if err := call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutMove, IDs: []string{s.ID}, GroupID: store.DefaultGroupID}, &res); err != nil ||
		res.Results[0].OK {
		t.Errorf("moved a host's session into a group here: %+v %v", res, err)
	}
	must(t, call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutMove, IDs: []string{s.ID}, GroupID: "g1"}, nil))
	must(t, call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "Ops", Host: "studio"}, nil))
	var resumed map[string]string
	must(t, call(ipc.OpResume, ipc.Mutation{ID: s.ID}, &resumed))
	if resumed["op"] != ipc.OpResume {
		t.Errorf("resume = %v", resumed)
	}
	ran := strings.Join(b.calls(), "\n")
	for _, want := range []string{`"kind":"session.rename","id":"r1"`, `"kind":"group.create","text":"Ops"`, ipc.OpResume + " "} {
		if !strings.Contains(ran, want) {
			t.Errorf("the host didn't run %s:\n%s", want, ran)
		}
	}
	if strings.Contains(ran, `"host"`) {
		t.Errorf("the host was told its own name:\n%s", ran)
	}

	// Other ops reach the host.
	var harnesses api.Harnesses
	must(t, call(ipc.OpRemoteHarnesses, ipc.RemoteHostRef{Host: "studio"}, &harnesses))
	var ranOp map[string]string
	must(t, call(ipc.OpRemoteCall, ipc.RemoteCall{Host: "studio", Op: ipc.OpStatus}, &ranOp))
	if ranOp["op"] != ipc.OpStatus {
		t.Errorf("remote.call = %v", ranOp)
	}
	if err := call(ipc.OpRemoteCall, ipc.RemoteCall{Host: "studio", Op: ipc.OpSpeechSay}, nil); err == nil || !strings.Contains(err.Error(), "can't be run from another machine") {
		t.Errorf("speech on the host: %v", err)
	}
	if err := call(ipc.OpRemoteCall, "x", nil); err == nil {
		t.Error("a call that isn't one")
	}
	// Its settings: the host's own, read and changed from here.
	var hostSettings ipc.ConfigState
	must(t, call(ipc.OpRemoteConfigGet, ipc.RemoteHostRef{Host: "studio"}, &hostSettings))
	if hostSettings.Values["remote.host.enabled"] != true {
		t.Errorf("host config = %+v", hostSettings.Values)
	}
	must(t, call(ipc.OpRemoteConfigSet, ipc.RemoteConfigSet{Host: "studio", Values: map[string]any{"notify.cooldown": "45s"}}, &hostSettings))
	if hostSettings.Values["notify.cooldown"] != "45s" || b.file.Config().Notify.Cooldown != "45s" {
		t.Errorf("after set = %+v", hostSettings.Values["notify.cooldown"])
	}
	if err := call(ipc.OpRemoteConfigSet, ipc.RemoteConfigSet{Host: "studio", Values: map[string]any{"speech.speed": 1.2}}, nil); err == nil {
		t.Error("changed the host's device settings")
	}
	if err := call(ipc.OpRemoteConfigGet, "nope", nil); err == nil {
		t.Error("took a ref that isn't one")
	}
	if err := call(ipc.OpRemoteConfigSet, "nope", nil); err == nil {
		t.Error("took a change that isn't one")
	}
	var folders api.FolderList
	must(t, call(ipc.OpRemoteFoldersRecent, ipc.RemoteHostRef{Host: "studio"}, &folders))
	must(t, call(ipc.OpRemoteFoldersList, ipc.RemoteFolderRequest{Host: "studio", Path: "~"}, &folders))
	var folder api.Folder
	must(t, call(ipc.OpRemoteFoldersMkdir, ipc.RemoteFolderRequest{Host: "studio", Path: "~/made"}, &folder))
	var hosts []ipc.RemoteHost
	must(t, call(ipc.OpRemoteHosts, nil, &hosts))
	must(t, call(ipc.OpRemoteHostRetry, ipc.RemoteHostRef{Host: "studio"}, &rh))
	// A new session on the host: it's the host's, and shows in its tree.
	var fresh ipc.Session
	must(t, call(ipc.OpRemoteNew, ipc.RemoteNewRequest{Host: "studio", Dir: "~", Harness: core.HarnessTerminal, Name: "fresh"}, &fresh))
	if fresh.Host != "studio" || fresh.ID != "new1" || fresh.RemoteID != "new1" {
		t.Errorf("new = %+v", fresh)
	}
	waitFor(t, "the new one here", func() bool {
		for _, x := range snapshot().Sessions {
			if x.ID == "new1" {
				return true
			}
		}
		return false
	})
	if err := call(ipc.OpRemoteBrowse, ipc.RemoteHostRef{Host: "nope"}, nil); err == nil {
		t.Error("browsed nothing")
	}

	// Archived on the host: it stays listed here, archived, with no socket.
	must(t, call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutArchive, ID: s.ID}, nil))
	if b.get("r1").Status != core.StatusArchived {
		t.Errorf("host's status %q", b.get("r1").Status)
	}
	waitFor(t, "archived here", func() bool {
		for _, x := range snapshot().Sessions {
			if x.ID == s.ID {
				return x.Status == core.StatusArchived
			}
		}
		return false
	})
	must(t, call(ipc.OpRemoteHostEdit, ipc.RemoteHostEdit{Host: "studio", Name: "mac"}, &rh))
	waitFor(t, "named by its new name", func() bool {
		for _, x := range snapshot().Sessions {
			if x.ID == "r1" {
				return x.Host == "mac"
			}
		}
		return false
	})
	must(t, call(ipc.OpRemoteHostRemove, ipc.RemoteHostRef{Host: "mac"}, nil))
	if snap := snapshot(); len(snap.Sessions) != 1 { // the one made here
		t.Errorf("sessions after removing the host = %+v", snap.Sessions)
	}
	if err := call(ipc.OpRemoteHostRemove, ipc.RemoteHostRef{Host: "mac"}, nil); err == nil {
		t.Error("removed it twice")
	}
	cancel()
	logErr(t, <-done)
}

// logErr notes an error a test doesn't mind.
func logErr(t *testing.T, err error) {
	if err != nil {
		t.Log(err)
	}
}

func TestClientBackend(t *testing.T) {
	ctx := context.Background()
	p := testPaths(t)
	must(t, p.Ensure())
	st, err := store.Open(ctx, p.DB())
	must(t, err)
	t.Cleanup(func() { st.Close() })
	d := &Daemon{store: st, cfg: Config{Paths: p, Log: log.New(io.Discard, "", 0), Now: time.Now}}
	b := clientBackend{d}
	// No notifier: nothing; notices off: only withdrawals.
	b.Notice("r1", "mac", ipc.Notice{Kind: "waiting"})
	d.notify = newNotifier(config.Notify{Enabled: false})
	b.Notice("r1", "mac", ipc.Notice{Kind: "waiting"})
	b.Notice("r1", "mac", ipc.Notice{Kind: "withdraw"})
	if n := d.notices(); len(n) != 1 || n[0].Kind != "withdraw" || n[0].Host != "mac" {
		t.Errorf("notices = %+v", n)
	}
	b.Changed()
	local, hosts := splitInboxIDs([]int64{3, -1, 4})
	if fmt.Sprint(local, hosts) != "[3 4] [-1]" {
		t.Errorf("split = %v %v", local, hosts)
	}
	if d.withHostInbox(nil) != nil || d.remoteHosts() != nil {
		t.Error("no client: nothing to add")
	}
	if out, done, err := d.forwardToHost(ctx, ipc.Request{Op: ipc.OpPing}); out != nil || done || err != nil {
		t.Error("no client: nothing goes to a host")
	}
}

// Subscribe sends the host's state on every change, as a snapshot, and its
// new notices as they come.
func (b *otherDaemon) Subscribe(ctx context.Context, _ ipc.SubscribeRequest, send func(ipc.Event) error) error {
	var rev, sent uint64
	for {
		_, next := b.Changes()
		snap := b.snapshot()
		rev++
		if err := send(ipc.Event{Rev: rev, Type: ipc.EventSnapshot, Snapshot: &snap}); err != nil {
			return nil
		}
		b.mu.Lock()
		notices := slices.Clone(b.notices)
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
