package daemon

import (
	"bytes"
	"context"
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
	"github.com/brettmostert/agent-command/internal/remote/client"
	"github.com/brettmostert/agent-command/internal/remote/host"
	"github.com/brettmostert/agent-command/internal/remote/hostapi"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/setup"
	"github.com/brettmostert/agent-command/internal/store"
	"github.com/brettmostert/agent-command/internal/term"
)

// otherDaemon is a Remote host's daemon, in memory.
type otherDaemon struct {
	mu       sync.Mutex
	sessions []*core.Session
	inbox    []core.InboxItem
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
	must(t, setup.SaveSetting(hostCfg, "remote.host", "enabled", true))
	must(t, setup.SaveSetting(hostCfg, "remote.host", "port", port))
	h := host.New(host.Options{ConfigFile: hostCfg, StateFile: filepath.Join(other, "state.json"), IdentityDir: filepath.Join(other, "id")})
	t.Cleanup(h.Close)
	hostapi.Register(h, b, hostapi.Options{Home: other})
	must(t, h.Reload())
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
		b.sessions = append(b.sessions, &core.Session{ID: "r1", Name: "api", Cwd: other, Harness: core.HarnessTerminal,
			Status: core.StatusWaiting, Remote: true, CreatedAt: time.Now()})
		b.inbox = []core.InboxItem{{ID: 5, SessionID: "r1", Kind: core.InboxPermission, Text: "Bash", Options: core.PermissionOptions,
			State: core.InboxOpen, At: time.Now()}}
	})
	me, err := identity.Ensure(p.RemoteIdentityDir())
	must(t, err)
	_, err = h.Authorize(me.Fingerprint(), "laptop")
	must(t, err)

	// This machine.
	must(t, setup.SaveSetting(p.Config(), "notify", "enabled", true))
	start(t, p, func(c *Config) { c.ConfigFile = p.Config(); c.ReconcileEvery = 20 * time.Millisecond })
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
	if len(browse.Sessions) != 1 || browse.Sessions[0].Added {
		t.Fatalf("browse = %+v", browse)
	}
	var s core.Session
	must(t, call(ipc.OpRemoteSessionAdd, ipc.RemoteHostRef{Host: "studio", ID: "r1"}, &s))
	if s.Host != "studio" || s.RemoteID != "r1" || s.Kind != core.KindHosted || s.Name != "api" || s.ID == "r1" {
		t.Fatalf("added = %+v", s)
	}
	snapshot := func() ipc.Snapshot {
		t.Helper()
		var snap ipc.Snapshot
		must(t, call(ipc.OpSnapshot, nil, &snap))
		return snap
	}
	var item core.InboxItem
	waitFor(t, "the snapshot", func() bool {
		snap := snapshot()
		if len(snap.RemoteHosts) != 1 || snap.RemoteHosts[0].State != ipc.HostConnected || len(snap.Inbox) != 1 {
			return false
		}
		item = snap.Inbox[0]
		return len(snap.Sessions) == 1 && snap.Sessions[0].Host == "studio"
	})
	if item.ID >= 0 || item.Host != "studio" || item.SessionID != s.ID {
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

	// Mutations: rename goes to the host; Remote can't be set here.
	must(t, call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRename, ID: s.ID, Text: "renamed"}, nil))
	if b.get("r1").Name != "renamed" {
		t.Errorf("host's name %q", b.get("r1").Name)
	}
	if err := call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRename, ID: s.ID, Text: " "}, nil); err == nil {
		t.Error("renamed to nothing")
	}
	if err := call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRemote, ID: s.ID, Flag: true}, nil); err == nil {
		t.Error("made a host's session Remote here")
	}
	var resumed core.Session
	must(t, call(ipc.OpResume, ipc.Mutation{ID: s.ID}, &resumed))

	// Other ops reach the host.
	var harnesses api.Harnesses
	must(t, call(ipc.OpRemoteHarnesses, ipc.RemoteHostRef{Host: "studio"}, &harnesses))
	var folders api.FolderList
	must(t, call(ipc.OpRemoteFoldersRecent, ipc.RemoteHostRef{Host: "studio"}, &folders))
	must(t, call(ipc.OpRemoteFoldersList, ipc.RemoteFolderRequest{Host: "studio", Path: "~"}, &folders))
	var folder api.Folder
	must(t, call(ipc.OpRemoteFoldersMkdir, ipc.RemoteFolderRequest{Host: "studio", Path: "~/made"}, &folder))
	var hosts []ipc.RemoteHost
	must(t, call(ipc.OpRemoteHosts, nil, &hosts))
	must(t, call(ipc.OpRemoteHostRetry, ipc.RemoteHostRef{Host: "studio"}, &rh))
	var fresh core.Session
	// A split from the host's session starts the new one on that host, in a
	// set with it.
	must(t, call(ipc.OpRemoteNew, ipc.RemoteNewRequest{Host: "studio", Dir: "~", Harness: core.HarnessTerminal, Name: "fresh", SetWith: s.ID}, &fresh))
	if fresh.Host != "studio" || fresh.RemoteID != "new1" {
		t.Errorf("new = %+v", fresh)
	}
	if sets := snapshot().Sets; len(sets) != 1 || sets[0].Host != "studio" || fresh.Parent != sets[0].ID || fresh.Order != 2 {
		t.Errorf("split on the host: sets %+v, fresh in %q at %d", sets, fresh.Parent, fresh.Order)
	}
	must(t, call(ipc.OpRemoteSessionRemove, ipc.RemoteHostRef{ID: fresh.ID}, nil))
	if sets := snapshot().Sets; len(sets) != 0 {
		t.Errorf("removing one of two left the set: %+v", sets)
	}
	if err := call(ipc.OpRemoteBrowse, ipc.RemoteHostRef{Host: "nope"}, nil); err == nil {
		t.Error("browsed nothing")
	}

	// End: archived on the host, then here; purged here only.
	must(t, call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutArchive, ID: s.ID}, nil))
	if b.get("r1").Status != core.StatusArchived {
		t.Errorf("host's status %q", b.get("r1").Status)
	}
	waitFor(t, "gone", func() bool {
		for _, x := range snapshot().Sessions {
			if x.ID == s.ID {
				return x.Status == core.StatusArchived && x.HostGone
			}
		}
		return false
	})
	if err := call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutUnarchive, ID: s.ID}, nil); err == nil {
		t.Error("brought back a session the host no longer offers")
	}
	if err := call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutFinish, ID: s.ID}, nil); err == nil {
		t.Error("finished a session the host no longer offers")
	}
	must(t, call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutPurge, IDs: []string{s.ID}}, nil))
	if snap := snapshot(); len(snap.Sessions) != 0 {
		t.Errorf("sessions = %+v", snap.Sessions)
	}
	must(t, call(ipc.OpRemoteHostEdit, ipc.RemoteHostEdit{Host: "studio", Name: "mac"}, &rh))
	must(t, call(ipc.OpRemoteHostRemove, ipc.RemoteHostRef{Host: "mac"}, nil))
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

	rs := api.Session{ID: "r1", Name: "api", Harness: core.HarnessClaude, Dir: "/w", Status: "busy", Created: time.Now()}
	s, err := b.Add(ctx, "studio", rs, "")
	must(t, err)
	again, err := b.Add(ctx, "studio", rs, "")
	must(t, err)
	if again.ID != s.ID || s.GroupID != store.DefaultGroupID {
		t.Errorf("added twice: %+v, %+v", s, again)
	}
	unseen := true
	must(t, b.Update(ctx, s.ID, client.Update{Session: api.Session{ID: "r1", Name: "api", Status: "waiting"}, Unseen: &unseen}))
	must(t, b.Update(ctx, "nope", client.Update{}))
	got, err := st.Session(ctx, s.ID)
	must(t, err)
	if got.Status != core.StatusWaiting || !got.Unseen {
		t.Errorf("updated = %+v", got)
	}
	must(t, b.Gone(ctx, s.ID))
	must(t, b.Gone(ctx, s.ID)) // unchanged
	// Archived, then added again: back.
	must(t, d.archive(ctx, s.ID))
	back, err := b.Add(ctx, "studio", rs, "")
	must(t, err)
	if back.ID != s.ID || back.Status != core.StatusBusy || back.HostGone {
		t.Errorf("back = %+v", back)
	}
	added, err := b.Added(ctx)
	must(t, err)
	if len(added) != 1 || added[0] != (client.Added{ID: s.ID, Host: "studio", RemoteID: "r1"}) {
		t.Errorf("added = %+v", added)
	}
	must(t, b.RenameHost(ctx, "studio", "mac"))
	got, err = st.Session(ctx, s.ID)
	must(t, err)
	if got.Host != "mac" {
		t.Errorf("host = %q", got.Host)
	}
	// No notifier: nothing; notices off: only withdrawals.
	b.Notice(s.ID, "mac", api.Notice{Kind: "waiting"})
	d.notify = newNotifier(config.Notify{Enabled: false})
	b.Notice(s.ID, "mac", api.Notice{Kind: "waiting"})
	b.Notice(s.ID, "mac", api.Notice{Kind: "withdraw"})
	if n := d.notices(); len(n) != 1 || n[0].Kind != "withdraw" || n[0].Host != "mac" {
		t.Errorf("notices = %+v", n)
	}
	b.Changed()
	must(t, b.Remove(ctx, s.ID))
	must(t, b.Remove(ctx, s.ID))
	local, hosts := splitInboxIDs([]int64{3, -1, 4})
	if fmt.Sprint(local, hosts) != "[3 4] [-1]" {
		t.Errorf("split = %v %v", local, hosts)
	}
	if d.withHostInbox(nil) != nil || d.remoteHosts() != nil {
		t.Error("no client: nothing to add")
	}
	if done, err := d.mutateOnHost(ctx, ipc.Mutation{Kind: ipc.MutSeen, ID: s.ID}); done || err != nil {
		t.Errorf("seen went to the host: %v", err)
	}
}
