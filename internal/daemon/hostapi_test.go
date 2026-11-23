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
	fh := &fakeHosts{paths: p}
	start(t, p, func(c *Config) {
		c.ConfigFile, c.Hosts, c.Home = p.Config(), fh, home
		c.Installed = func(h core.Harness) bool { return h.Kind == core.HarnessClaude }
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

	// A session started by the client is Remote, in the host's Claude
	// harness, with its own args (claude_args, moved into it) first.
	var s api.Session
	must(t, conn.Call(ctx, api.OpSessionNew, api.NewSession{Dir: "~/proj", HarnessKind: core.HarnessClaude, Args: []string{"--theirs"}}, &s))
	if s.Dir != filepath.Join(home, "proj") || s.HarnessKind != core.HarnessClaude {
		t.Errorf("new = %+v", s)
	}
	reqs := fh.requests()
	if r := reqs[len(reqs)-1]; r.Harness == nil || !slices.Equal(r.Harness.Args, []string{"--own"}) || !slices.Equal(r.Args, []string{"--theirs"}) {
		t.Errorf("request = %+v", r)
	}
	snap := snapshot(t, c)
	if i := slices.IndexFunc(snap.Sessions, func(x *ipc.Session) bool { return x.ID == s.ID }); i < 0 || !snap.Sessions[i].Remote {
		t.Errorf("the host's sidebar doesn't show it as Remote: %+v", snap.Sessions)
	}
	must(t, events.Receive(&ev))
	if ev.Type != api.EventAdded || ev.Session.ID != s.ID {
		t.Errorf("added = %+v", ev)
	}
	var term api.Session
	must(t, conn.Call(ctx, api.OpSessionNew, api.NewSession{Dir: "proj", HarnessKind: core.HarnessTerminal}, &term))
	if term.HarnessKind != core.HarnessTerminal || term.HarnessID != "" {
		t.Errorf("a terminal: %+v", term)
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
	// The snapshot says so, without asking for the clients.
	var withAttach ipc.Snapshot
	must(t, c.Call(ipc.OpSnapshot, nil, &withAttach))
	if withAttach.Remote == nil || withAttach.Remote.Attached[s.ID] != 1 {
		t.Errorf("snapshot's attached = %+v", withAttach.Remote)
	}
	var harnesses api.Harnesses
	must(t, conn.Call(ctx, api.OpHarnessesList, nil, &harnesses))
	if len(harnesses.Harnesses) != 2 || harnesses.Default != harnesses.Harnesses[0].ID || !harnesses.Harnesses[0].Installed ||
		harnesses.Harnesses[1].Installed || !harnesses.Terminal || s.HarnessID != harnesses.Default {
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
	if got := snapshot(t, c); got.Sessions[slices.IndexFunc(got.Sessions, func(x *ipc.Session) bool { return x.ID == s.ID })].Status != core.StatusArchived {
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
