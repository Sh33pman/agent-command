package daemon

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/store"
)

// remoteLog records what OnRemoteChanged's subscribers are told.
type remoteLog struct {
	mu      sync.Mutex
	changes []RemoteChange
}

func (l *remoteLog) add(c RemoteChange) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.changes = append(l.changes, c)
}

func (l *remoteLog) all() []RemoteChange {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]RemoteChange(nil), l.changes...)
}

func TestRemoteFlag(t *testing.T) {
	sessionsHere(t)
	p := testPaths(t)
	cfgFile := filepath.Join(t.TempDir(), "config.toml")
	must(t, os.WriteFile(cfgFile, []byte("[remote]\nnew_sessions_remote = true\n"), 0o600))
	fh := &fakeHosts{paths: p}
	changes := &remoteLog{}
	r := start(t, p, func(c *Config) { c.Hosts, c.ConfigFile, c.OnRemoteChanged = fh, cfgFile, changes.add })
	c := dial(t, p)

	// Unset: the config's default. Set: what the request says.
	byDefault := newSess(t, c, ipc.NewRequest{Cwd: "/w/a"})
	local := newSess(t, c, ipc.NewRequest{Cwd: "/w/b", Remote: ptr(false)})
	if !byDefault.Remote || local.Remote {
		t.Fatalf("new sessions: default %v, --no-remote %v", byDefault.Remote, local.Remote)
	}
	must(t, os.WriteFile(cfgFile, []byte("[remote]\nnew_sessions_remote = false\n"), 0o600))
	if s := newSess(t, c, ipc.NewRequest{Cwd: "/w/c"}); s.Remote {
		t.Error("a config change applies to the next new session")
	}
	if s := newSess(t, c, ipc.NewRequest{Cwd: "/w/d", Remote: ptr(true)}); !s.Remote {
		t.Error("--remote makes it Remote")
	}
	if len(changes.all()) != 0 {
		t.Errorf("creating sessions isn't a change: %+v", changes.all())
	}
	var st ipc.Status
	must(t, c.Call(ipc.OpStatus, nil, &st))
	if st.RemoteSessions != 2 {
		t.Errorf("status counts %d Remote sessions, want 2", st.RemoteSessions)
	}

	// Turning it off: saved, watchers see a new revision, subscribers are told.
	var before, after ipc.WatchResponse
	must(t, c.Call(ipc.OpWatch, ipc.WatchRequest{}, &before))
	mutate(t, c, ipc.Mutation{Kind: ipc.MutRemote, Targets: ipc.Here(byDefault.ID), Flag: false})
	must(t, c.Call(ipc.OpWatch, ipc.WatchRequest{}, &after))
	if after.Rev == before.Rev {
		t.Error("watchers weren't woken")
	}
	if sessionByID(t, c, byDefault.ID).Remote {
		t.Error("still Remote after turning it off")
	}
	mutate(t, c, ipc.Mutation{Kind: ipc.MutRemote, Targets: ipc.Here(byDefault.ID), Flag: false}) // no change
	mutate(t, c, ipc.Mutation{Kind: ipc.MutRemote, Targets: ipc.Here(local.ID), Flag: true})
	want := []RemoteChange{{SessionID: byDefault.ID, Remote: false}, {SessionID: local.ID, Remote: true}}
	if got := changes.all(); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("changes = %+v, want %+v", got, want)
	}
	if err := c.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRemote, Targets: ipc.Here("nope"), Flag: true}, nil); err == nil {
		t.Error("an unknown session is an error")
	}

	// It survives a restart, and each change is in the event log.
	r.cancel()
	<-r.Done
	st2, err := store.Open(context.Background(), p.DB())
	must(t, err)
	events, err := st2.RecentEvents(context.Background(), local.ID, 10)
	must(t, err)
	st2.Close()
	if len(events) == 0 || events[0].Kind != string(core.EventRemote) || events[0].Detail != "on" {
		t.Errorf("event log: %+v", events)
	}
	start(t, p, func(c *Config) { c.Hosts = fh })
	c = dial(t, p)
	if !sessionByID(t, c, local.ID).Remote || sessionByID(t, c, byDefault.ID).Remote {
		t.Error("the Remote flags didn't survive a restart")
	}
}

func TestOnRemoteChangedStops(t *testing.T) {
	d := &Daemon{}
	var a, b remoteLog
	stopA := d.OnRemoteChanged(a.add)
	d.OnRemoteChanged(b.add)
	d.remoteChanged(RemoteChange{SessionID: "s1"})
	stopA()
	d.remoteChanged(RemoteChange{SessionID: "s2", Remote: true})
	if len(a.all()) != 1 || len(b.all()) != 2 {
		t.Errorf("a got %+v, b got %+v", a.all(), b.all())
	}
}

func TestNewSessionsRemoteDefault(t *testing.T) {
	var logged bytes.Buffer
	dir := t.TempDir()
	d := &Daemon{cfg: Config{Log: log.New(&logged, "", 0)}}
	if d.newSessionsRemote(nil) {
		t.Error("no config file: not Remote")
	}
	if !d.newSessionsRemote(ptr(true)) {
		t.Error("the request wins")
	}
	d = &Daemon{cfg: Config{Log: log.New(&logged, "", 0), ConfigFile: filepath.Join(dir, "missing.toml")}}
	if d.newSessionsRemote(nil) {
		t.Error("a missing config is the default: not Remote")
	}
	d = &Daemon{cfg: Config{Log: log.New(&logged, "", 0), ConfigFile: filepath.Join(dir, "broken.toml")}}
	must(t, os.WriteFile(d.cfg.ConfigFile, []byte("[remote\n"), 0o600))
	if d.newSessionsRemote(nil) || logged.Len() == 0 {
		t.Errorf("a broken config: not Remote, and logged (%q)", logged.String())
	}
}
