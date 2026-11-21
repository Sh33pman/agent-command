package daemon

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/remote/wire"
)

func TestHostingOps(t *testing.T) {
	p := testPaths(t)
	p.ConfigDir = filepath.Join(filepath.Dir(p.StateDir), "c")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if err := writeSetting(p.Config(), "remote.host", "port", port); err != nil {
		t.Fatal(err)
	}
	start(t, p, func(c *Config) { c.ConfigFile = p.Config(); c.ReconcileEvery = 20 * time.Millisecond })
	c := dial(t, p)
	call := func(op string, in, out any) error {
		t.Helper()
		return c.Call(op, in, out)
	}

	// Off until it's turned on.
	var st ipc.RemoteStatus
	if err := call(ipc.OpRemoteStatus, nil, &st); err != nil || st.Enabled || st.Listening || st.ConfiguredPort != port {
		t.Fatalf("status = %+v, %v", st, err)
	}
	var snap ipc.Snapshot
	if err := call(ipc.OpSnapshot, nil, &snap); err != nil || snap.Remote == nil || snap.Remote.ConfiguredPort != port {
		t.Fatalf("snapshot's remote = %+v, %v", snap.Remote, err)
	}
	if err := call(ipc.OpRemoteHosting, ipc.RemoteRef{Enabled: true}, &st); err != nil || !st.Listening || st.Port != port {
		t.Fatalf("on: %+v, %v", st, err)
	}

	// An unknown client becomes a request; approving it lets it in.
	client, err := identity.Ensure(filepath.Join(filepath.Dir(p.StateDir), "client"))
	if err != nil {
		t.Fatal(err)
	}
	connect := func() (*wire.Conn, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return wire.Dial(ctx, st.Address, client, st.Fingerprint, wire.Hello{Version: "x"})
	}
	if _, err := connect(); !errors.Is(err, wire.ErrPending) {
		t.Fatalf("unknown client: %v", err)
	}
	var reqs []ipc.RemoteRequest
	if err := call(ipc.OpRemoteRequests, nil, &reqs); err != nil || len(reqs) != 1 || reqs[0].Fingerprint != client.Fingerprint() {
		t.Fatalf("requests = %+v, %v", reqs, err)
	}
	if err := call(ipc.OpRemoteDeny, ipc.RemoteRef{ID: "nope"}, nil); err == nil {
		t.Error("denied a request that isn't there")
	}
	var rc ipc.RemoteClient
	if err := call(ipc.OpRemoteApprove, ipc.RemoteRef{ID: reqs[0].ID, Name: "laptop"}, &rc); err != nil || rc.Name != "laptop" {
		t.Fatalf("approve = %+v, %v", rc, err)
	}
	conn, err := connect()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var clients []ipc.RemoteClient
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := call(ipc.OpRemoteClients, nil, &clients); err != nil {
			t.Fatal(err)
		}
		if len(clients) == 1 && len(clients[0].Connections) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("clients = %+v", clients)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var n ipc.RemoteDisconnected
	if err := call(ipc.OpRemoteDisconnect, ipc.RemoteRef{Client: "laptop"}, &n); err != nil || n.Closed != 1 {
		t.Errorf("disconnect = %+v, %v", n, err)
	}

	// Authorize and revoke by fingerprint.
	other, _ := identity.Ensure(filepath.Join(filepath.Dir(p.StateDir), "other"))
	if err := call(ipc.OpRemoteAuthorize, ipc.RemoteRef{Fingerprint: other.Fingerprint(), Name: "desk"}, &rc); err != nil || rc.Name != "desk" {
		t.Fatalf("authorize = %+v, %v", rc, err)
	}
	if err := call(ipc.OpRemoteRevoke, ipc.RemoteRef{Client: other.Fingerprint()}, &rc); err != nil || rc.Name != "desk" {
		t.Errorf("revoke = %+v, %v", rc, err)
	}
	// They're kept in state.db, not config.toml.
	var kept []ipc.RemoteClient
	if err := call(ipc.OpRemoteClients, nil, &kept); err != nil || len(kept) != 1 || kept[0].Name != "laptop" || kept[0].InConfig {
		t.Errorf("clients = %+v, %v", kept, err)
	}
	if cfg, err := config.Load(p.Config()); err != nil || len(cfg.Remote.AuthorizedClients) != 0 {
		t.Errorf("config.toml = %+v, %v", cfg.Remote.AuthorizedClients, err)
	}
	// Renamed through the daemon.
	if err := call(ipc.OpRemoteRename, ipc.RemoteRef{Client: "laptop", Name: "work"}, &rc); err != nil || rc.Name != "work" {
		t.Errorf("rename = %+v, %v", rc, err)
	}

	// A change to config.toml is picked up by itself, or at once with a reload.
	if err := writeSetting(p.Config(), "remote.host", "enabled", false); err != nil {
		t.Fatal(err)
	}
	if err := call(ipc.OpRemoteReload, nil, &st); err != nil || st.Listening {
		t.Errorf("reload = %+v, %v", st, err)
	}
	if err := writeSetting(p.Config(), "remote.host", "enabled", true); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Second)
	_ = os.Chtimes(p.Config(), now, now)
	for deadline := time.Now().Add(5 * time.Second); ; {
		if err := call(ipc.OpRemoteStatus, nil, &st); err != nil {
			t.Fatal(err)
		}
		if st.Listening {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("didn't follow config.toml")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := call("remote.nope", nil, nil); err == nil || !strings.Contains(err.Error(), "unknown op") {
		t.Errorf("unknown op: %v", err)
	}
	if err := call(ipc.OpRemoteDeny, "x", nil); err == nil || !strings.Contains(err.Error(), "bad request") {
		t.Errorf("bad data: %v", err)
	}
}
