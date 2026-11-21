package daemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/remote/wire"
)

// TestHostAPI runs the host API against a real daemon: clients see and use
// only Remote sessions, and what they start is Remote.
func TestHostAPI(t *testing.T) {
	p := testPaths(t)
	base := filepath.Dir(p.StateDir)
	p.ConfigDir = filepath.Join(base, "c")
	home := filepath.Join(base, "home")
	must(t, os.MkdirAll(filepath.Join(home, "proj"), 0o750))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	must(t, writeSetting(p.Config(), "remote.host", "port", port))
	must(t, writeSetting(p.Config(), "remote.host", "enabled", true))
	must(t, writeSetting(p.Config(), "", "claude_args", []string{"--own"}))
	must(t, writeSetting(p.Config(), "", "default_harness", "terminal"))
	fh := &fakeHosts{paths: p}
	start(t, p, func(c *Config) {
		c.ConfigFile, c.Hosts, c.Home = p.Config(), fh, home
		c.Harnesses = func() []string { return []string{core.HarnessClaude, core.HarnessTerminal} }
	})
	c := dial(t, p)
	local := newSess(t, c, ipc.NewRequest{Cwd: "/w/local", Name: "local"})

	client, err := identity.Ensure(filepath.Join(base, "client"))
	must(t, err)
	must(t, c.Call(ipc.OpRemoteAuthorize, ipc.RemoteRef{Fingerprint: client.Fingerprint(), Name: "laptop"}, nil))
	var st ipc.RemoteStatus
	must(t, c.Call(ipc.OpRemoteStatus, nil, &st))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := wire.Dial(ctx, st.Address, client, st.Fingerprint, wire.Hello{Version: "x"})
	must(t, err)
	defer conn.Close()

	var info wire.Info
	must(t, conn.Call(ctx, wire.OpInfo, nil, &info))
	if info.API != api.Version || !slices.Contains(info.Ops, api.OpSessionAttach) || !slices.Contains(info.Ops, api.OpEvents) {
		t.Errorf("info = %+v", info)
	}

	events, err := conn.Open(ctx, api.OpEvents, nil, nil)
	must(t, err)
	defer events.Close()
	var ev api.Event
	must(t, events.Receive(&ev))
	if ev.Type != api.EventSnapshot || len(ev.Sessions) != 0 {
		t.Fatalf("snapshot = %+v", ev)
	}

	// A session started by the client is Remote, with this host's own args
	// first, in the host's default harness.
	var s api.Session
	must(t, conn.Call(ctx, api.OpSessionNew, api.NewSession{Dir: "~/proj", Harness: core.HarnessClaude, Args: []string{"--theirs"}}, &s))
	if s.Dir != filepath.Join(home, "proj") || s.Harness != core.HarnessClaude {
		t.Errorf("new = %+v", s)
	}
	reqs := fh.requests()
	if r := reqs[len(reqs)-1]; !slices.Equal(r.Args, []string{"--own", "--theirs"}) {
		t.Errorf("args = %v", r.Args)
	}
	snap := snapshot(t, c)
	if i := slices.IndexFunc(snap.Sessions, func(x *core.Session) bool { return x.ID == s.ID }); i < 0 || !snap.Sessions[i].Remote {
		t.Errorf("the host's sidebar doesn't show it as Remote: %+v", snap.Sessions)
	}
	must(t, events.Receive(&ev))
	if ev.Type != api.EventAdded || ev.Session.ID != s.ID {
		t.Errorf("added = %+v", ev)
	}
	var term api.Session
	must(t, conn.Call(ctx, api.OpSessionNew, api.NewSession{Dir: "proj"}, &term))
	if term.Harness != core.HarnessTerminal {
		t.Errorf("default harness: %+v", term)
	}

	var l api.SessionList
	must(t, conn.Call(ctx, api.OpSessionsList, nil, &l))
	if len(l.Sessions) != 2 {
		t.Errorf("list = %+v", l)
	}
	if err := conn.Call(ctx, api.OpSessionRename, api.Rename{ID: local.ID, Name: "x"}, nil); !wire.IsCode(err, wire.CodeNotFound) {
		t.Errorf("rename a local session: %v", err)
	}
	var renamed api.Session
	must(t, conn.Call(ctx, api.OpSessionRename, api.Rename{ID: s.ID, Name: "from afar"}, &renamed))
	if renamed.Name != "from afar" {
		t.Errorf("renamed = %+v", renamed)
	}

	// Attach: Hosting counts it, and ending the session ends the stream.
	stream, err := conn.Open(ctx, api.OpSessionAttach, api.SessionRef{ID: s.ID}, nil)
	must(t, err)
	_, err = stream.Write([]byte{'a', 0, 0, 0, 0})
	must(t, err)
	attached := func() bool {
		var clients []ipc.RemoteClient
		must(t, c.Call(ipc.OpRemoteClients, nil, &clients))
		return len(clients) == 1 && len(clients[0].Connections) == 1 && slices.Contains(clients[0].Connections[0].Sessions, s.ID)
	}
	for deadline := time.Now().Add(5 * time.Second); !attached(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("not attached")
		}
	}
	var harnesses api.Harnesses
	must(t, conn.Call(ctx, api.OpHarnessesList, nil, &harnesses))
	if len(harnesses.Harnesses) != 2 || harnesses.Default != core.HarnessTerminal {
		t.Errorf("harnesses = %+v", harnesses)
	}
	must(t, conn.Call(ctx, api.OpSessionEnd, api.End{ID: s.ID}, nil))
	ended := make(chan struct{})
	go func() {
		_, _ = stream.Read(make([]byte, 1<<16))
		_, _ = stream.Read(make([]byte, 1<<16))
		close(ended)
	}()
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the attach stream outlived the session")
	}
	if got := snapshot(t, c); got.Sessions[slices.IndexFunc(got.Sessions, func(x *core.Session) bool { return x.ID == s.ID })].Status != core.StatusArchived {
		t.Error("not archived")
	}
	var recent api.FolderList
	must(t, conn.Call(ctx, api.OpFoldersRecent, nil, &recent))
	if len(recent.Folders) != 1 || recent.Folders[0].Path != filepath.Join(home, "proj") {
		t.Errorf("recent = %+v", recent)
	}

	// Finish keeps it listed.
	must(t, conn.Call(ctx, api.OpSessionEnd, api.End{ID: term.ID, Finish: true}, nil))
	must(t, conn.Call(ctx, api.OpSessionsList, nil, &l))
	if len(l.Sessions) != 1 || l.Sessions[0].ID != term.ID || l.Sessions[0].Status != string(core.StatusFinished) {
		t.Errorf("after finish: %+v", l)
	}
}

func TestHostBackendDefaults(t *testing.T) {
	d := &Daemon{}
	installed, def := hostBackend{d}.Harnesses()
	if len(installed) != 3 || def != "" {
		t.Errorf("harnesses = %v, %q", installed, def)
	}
	d.cfg.ConfigFile = filepath.Join(t.TempDir(), "config.toml")
	must(t, os.WriteFile(d.cfg.ConfigFile, []byte("[broken"), 0o600))
	if _, def := (hostBackend{d}).Harnesses(); def != "" {
		t.Errorf("default from a broken config = %q", def)
	}
}
