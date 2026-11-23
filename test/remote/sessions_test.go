package remote

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/remote/wire"
	"github.com/brettmostert/agent-command/internal/term"
)

// hostSession is a host's session as it shows here, in the host's tree,
// once it does: its id is the host's.
func (m *machine) hostSession(host, id string) *ipc.Session {
	m.t.Helper()
	var s *ipc.Session
	m.waitFor("the host's session here", func() bool {
		s = m.session(id)
		return s != nil && s.Host == host
	})
	if s.RemoteID != id || s.Kind != core.KindHosted {
		m.t.Fatalf("here = %+v", s)
	}
	return s
}

// TestTheHostsTreeShowsHere: a client sees all of a host's sessions, as the
// host has them (Direct Connect). The host API's own session ops, for the
// app's older Remote features, still see only Remote sessions.
func TestTheHostsTreeShowsHere(t *testing.T) {
	host := newMachine(t, "host", hostConfig(freePort(t), true))
	host.start()
	shared := host.newSession("shared", true)
	private := host.newSession("private", false)
	laptop := newMachine(t, "laptop", clientConfig)
	laptop.start()
	laptop.pair("studio", host)

	var browse ipc.RemoteBrowse
	laptop.call(ipc.OpRemoteBrowse, ipc.RemoteHostRef{Host: "studio"}, &browse)
	if len(browse.Sessions) != 2 || browse.Sessions[0].HarnessKind != core.HarnessTerminal || browse.Sessions[0].Host != "studio" {
		t.Fatalf("browse = %+v", browse)
	}
	laptop.hostSession("studio", shared.ID)
	laptop.hostSession("studio", private.ID)

	// The host API itself, as any authorized client could call it.
	c := dialHost(t, host)
	var list api.SessionList
	if err := c.Call(context.Background(), api.OpSessionsList, nil, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].ID != shared.ID {
		t.Errorf("sessions.list = %+v", list)
	}
	for _, op := range []struct {
		name string
		call func() error
	}{
		{"rename", func() error {
			return c.Call(context.Background(), api.OpSessionRename, api.Rename{ID: private.ID, Name: "taken"}, nil)
		}},
		{"end", func() error { return c.Call(context.Background(), api.OpSessionEnd, api.End{ID: private.ID}, nil) }},
		{"finish", func() error {
			return c.Call(context.Background(), api.OpSessionEnd, api.End{ID: private.ID, Finish: true}, nil)
		}},
	} {
		if err := op.call(); !wire.IsCode(err, wire.CodeNotFound) {
			t.Errorf("%s of a session that isn't Remote: %v, want not_found", op.name, err)
		}
	}
	s := host.session(private.ID)
	if s.Name != "private" || s.Status == core.StatusArchived || s.Core().Stopped() || !term.Running(host.p.HostSocket(private.ID)) {
		t.Errorf("the private session changed: %+v", s)
	}
}

// dialHost connects to host's Hosting as a client of its own, authorized
// there, to call the host API directly.
func dialHost(t *testing.T, host *machine) *wire.Conn {
	t.Helper()
	id, err := identity.Ensure(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	host.call(ipc.OpRemoteAuthorize, ipc.RemoteRef{Fingerprint: id.Fingerprint(), Name: "direct"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := wire.Dial(ctx, host.address(), id, host.status().Fingerprint, wire.Hello{Version: binVersion, Name: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestAttach: a remote pane gets the replay and live output, its input
// reaches the host's terminal and its size applies there; a pane on the
// host attaches at the same time.
func TestAttach(t *testing.T) {
	host := newMachine(t, "host", hostConfig(freePort(t), true))
	host.start()
	s := host.newSession("shared", true)
	host.typeIn(s.ID, "echo before-$((40+2))\n")
	host.waitFor("the output", func() bool { return strings.Contains(host.screen(s.ID), "before-42") })

	laptop := newMachine(t, "laptop", clientConfig)
	laptop.start()
	laptop.pair("studio", host)
	local := laptop.hostSession("studio", s.ID)
	if got := laptop.session(local.ID); got == nil || got.Host != "studio" || got.Name != "shared" || got.HostGone {
		t.Fatalf("snapshot = %+v", got)
	}

	remote := attach(t, laptop.p.HostSocket(local.ID), 24, 80)
	remote.sees(t, "before-42") // the replay
	here := attach(t, host.p.HostSocket(s.ID), 24, 80)
	here.sees(t, "before-42")

	remote.in <- []byte("echo from-laptop-$((3+4))\n")
	host.waitFor("the laptop's input", func() bool { return strings.Contains(host.screen(s.ID), "from-laptop-7") })
	here.sees(t, "from-laptop-7")
	remote.sees(t, "from-laptop-7")

	here.in <- []byte("echo from-host-$((5+6))\n")
	remote.sees(t, "from-host-11") // live

	remote.size <- [2]int{31, 97}
	remote.in <- []byte("stty size\n")
	remote.sees(t, "31 97")

	var clients []ipc.RemoteClient
	host.call(ipc.OpRemoteClients, nil, &clients)
	if len(clients) != 1 || len(clients[0].Connections) != 1 || len(clients[0].Connections[0].Sessions) != 1 ||
		clients[0].Connections[0].Sessions[0] != s.ID {
		t.Errorf("clients = %+v", clients)
	}
}

// TestCreateOnHost: a session made from the client runs on the host, in the
// folder and with the harness asked for, and shows here in the host's tree.
func TestCreateOnHost(t *testing.T) {
	host := newMachine(t, "host", hostConfig(freePort(t), true))
	host.start()
	laptop := newMachine(t, "laptop", clientConfig)
	laptop.start()
	laptop.pair("studio", host)

	var harnesses api.Harnesses
	laptop.call(ipc.OpRemoteHarnesses, ipc.RemoteHostRef{Host: "studio"}, &harnesses)
	var folder api.Folder
	laptop.call(ipc.OpRemoteFoldersMkdir, ipc.RemoteFolderRequest{Host: "studio", Path: "~/proj"}, &folder)
	want := filepath.Join(host.home, "proj")
	if st, err := os.Stat(want); err != nil || !st.IsDir() {
		t.Fatalf("mkdir made %+v: %v", folder, err)
	}

	var local ipc.Session
	laptop.call(ipc.OpRemoteNew, ipc.RemoteNewRequest{Host: "studio", Dir: "~/proj", HarnessKind: core.HarnessTerminal, Name: "made"}, &local)
	if local.Host != "studio" || local.RemoteID == "" || local.Name != "made" {
		t.Fatalf("new = %+v", local)
	}
	on := host.session(local.RemoteID)
	if on == nil || on.HarnessKind != core.HarnessTerminal || on.Name != "made" {
		t.Fatalf("on the host: %+v", on)
	}
	if resolved, _ := filepath.EvalSymlinks(want); on.Cwd != want && on.Cwd != resolved {
		t.Errorf("cwd %s, want %s", on.Cwd, want)
	}
	laptop.hostSession("studio", local.ID)

	host.waitFor("the session's host", func() bool { return term.Running(host.p.HostSocket(local.RemoteID)) })
	p := attach(t, laptop.p.HostSocket(local.ID), 24, 80)
	p.in <- []byte("pwd; echo made-$((1+1))\n")
	p.sees(t, "made-2")
	if !strings.Contains(p.out.String(), "proj") {
		t.Errorf("pwd: %q", p.out.String())
	}
}

// TestRemoteOff: turning Remote off on the host is about the app's older
// Remote features: a client with the host's tree keeps its pane, and the
// session stays listed (Direct Connect).
func TestRemoteOff(t *testing.T) {
	host := newMachine(t, "host", hostConfig(freePort(t), true))
	host.start()
	s := host.newSession("shared", true)
	laptop := newMachine(t, "laptop", clientConfig)
	laptop.start()
	laptop.pair("studio", host)
	local := laptop.hostSession("studio", s.ID)
	p := attach(t, laptop.p.HostSocket(local.ID), 24, 80)
	p.in <- []byte("echo attached-$((2+2))\n")
	p.sees(t, "attached-4")

	host.call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRemote, Targets: ipc.Here(s.ID), Flag: false}, nil)
	laptop.waitFor("the change here", func() bool { got := laptop.session(local.ID); return got != nil && !got.Remote })
	p.in <- []byte("echo still-$((3+3))\n")
	p.sees(t, "still-6")
}

// TestRevoke: a revoked client is disconnected at once, and can't get back in.
func TestRevoke(t *testing.T) {
	host := newMachine(t, "host", hostConfig(freePort(t), true))
	host.start()
	s := host.newSession("shared", true)
	laptop := newMachine(t, "laptop", clientConfig)
	laptop.start()
	laptop.pair("studio", host)
	local := laptop.hostSession("studio", s.ID)
	p := attach(t, laptop.p.HostSocket(local.ID), 24, 80)
	p.in <- []byte("echo attached-$((2+2))\n")
	p.sees(t, "attached-4")

	revoked := time.Now()
	host.call(ipc.OpRemoteRevoke, ipc.RemoteRef{Client: "laptop"}, nil)
	if st := host.status(); st.Connected != 0 || st.Authorized != 0 {
		t.Errorf("Hosting after the revoke: %+v", st)
	}
	laptop.waitFor("the connection to drop", func() bool { return laptop.knownHost("studio").State != ipc.HostConnected })
	if d := time.Since(revoked); d > time.Second {
		t.Errorf("the client was still connected %v after the revoke", d)
	}
	laptop.call(ipc.OpRemoteHostRetry, ipc.RemoteHostRef{Host: "studio"}, nil)
	// An authorized client that's revoked is unknown again, so it asks again.
	h := laptop.waitHost("studio", ipc.HostPending)
	t.Logf("after the revoke: %s (%s)", h.State, h.Error)
	// What the pane types goes nowhere: what's typed on the host after it
	// arrives, and it doesn't.
	p.in <- []byte("echo after-revoke\n")
	host.typeIn(s.ID, "echo marker-$((1+8))\n")
	host.waitFor("the marker", func() bool { return strings.Contains(host.screen(s.ID), "marker-9") })
	if text := host.screen(s.ID); strings.Contains(text, "after-revoke") {
		t.Error("input reached the host after the revoke")
	}

	// Approved again, it's back without a hand from the pane.
	var reqs []ipc.RemoteRequest
	host.waitFor("its request", func() bool {
		host.call(ipc.OpRemoteRequests, nil, &reqs)
		return len(reqs) == 1
	})
	host.call(ipc.OpRemoteApprove, ipc.RemoteRef{ID: reqs[0].ID, Name: "laptop"}, nil)
	laptop.call(ipc.OpRemoteHostRetry, ipc.RemoteHostRef{Host: "studio"}, nil)
	laptop.waitHost("studio", ipc.HostConnected)
	host.typeIn(s.ID, "echo back-$((5+5))\n")
	p.sees(t, "back-10")
}

// TestHostRestart: the host's daemon restarts, and the client reconnects and
// reattaches its pane by itself.
func TestHostRestart(t *testing.T) {
	host := newMachine(t, "host", hostConfig(freePort(t), true))
	host.start()
	s := host.newSession("shared", true)
	laptop := newMachine(t, "laptop", clientConfig)
	laptop.start()
	laptop.pair("studio", host)
	local := laptop.hostSession("studio", s.ID)
	p := attach(t, laptop.p.HostSocket(local.ID), 24, 80)
	p.in <- []byte("echo before-$((2+2))\n")
	p.sees(t, "before-4")

	host.stop()
	laptop.waitFor("the host to go", func() bool { return laptop.knownHost("studio").State != ipc.HostConnected })
	host.start()
	laptop.waitHost("studio", ipc.HostConnected)

	host.typeIn(s.ID, "echo after-$((3+3))\n")
	p.sees(t, "after-6")
	p.in <- []byte("echo typed-$((4+4))\n")
	host.waitFor("the laptop's input", func() bool { return strings.Contains(host.screen(s.ID), "typed-8") })
	if got := laptop.session(local.ID); got == nil || got.HostGone {
		t.Errorf("snapshot = %+v", got)
	}
}

// TestTwoHosts: one client uses sessions on two hosts at once.
func TestTwoHosts(t *testing.T) {
	laptop := newMachine(t, "laptop", clientConfig)
	laptop.start()
	var panes []*pane
	for i, name := range []string{"studio", "mini"} {
		host := newMachine(t, name, hostConfig(freePort(t), true))
		host.start()
		s := host.newSession("on-"+name, true)
		laptop.pair(name, host)
		local := laptop.hostSession(name, s.ID)
		p := attach(t, laptop.p.HostSocket(local.ID), 24, 80)
		p.in <- []byte("echo " + name + "-$((" + string(rune('1'+i)) + "*10))\n")
		panes = append(panes, p)
	}
	panes[0].sees(t, "studio-10")
	panes[1].sees(t, "mini-20")
	snap := laptop.snapshot()
	if len(snap.RemoteHosts) != 2 || len(snap.Sessions) != 2 {
		t.Fatalf("snapshot: hosts %+v, sessions %d", snap.RemoteHosts, len(snap.Sessions))
	}
	for _, h := range snap.RemoteHosts {
		if h.State != ipc.HostConnected || h.Sessions != 1 {
			t.Errorf("host %+v", h)
		}
	}
	if strings.Contains(panes[0].out.String(), "mini-20") || strings.Contains(panes[1].out.String(), "studio-10") {
		t.Error("the panes are mixed up")
	}
}

// A new session on a host, retried with the same request id through this
// machine's daemon, starts one session there: the id goes along to the host.
func TestARetriedNewSessionStartsOneOnTheHost(t *testing.T) {
	host := newMachine(t, "host", hostConfig(freePort(t), true))
	host.start()
	laptop := newMachine(t, "laptop", clientConfig)
	laptop.start()
	laptop.pair("studio", host)

	before := len(host.snapshot().Sessions)
	req := ipc.RemoteNewRequest{Host: "studio", Dir: t.TempDir(), HarnessKind: core.HarnessTerminal}
	var ids []string
	for range 2 {
		c, err := laptop.dial()
		if err != nil {
			t.Fatal(err)
		}
		var s ipc.Session
		if err := c.CallOnce(ipc.OpRemoteNew, "once-1", req, &s); err != nil {
			t.Fatal(err)
		}
		c.Close()
		ids = append(ids, s.ID)
	}
	if ids[0] == "" || ids[1] != ids[0] {
		t.Errorf("ids = %v", ids)
	}
	if n := len(host.snapshot().Sessions) - before; n != 1 {
		t.Errorf("%d sessions started on the host", n)
	}
}
