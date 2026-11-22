package remote

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/api"
	"github.com/brettmostert/agent-command/internal/remote/wire"
)

// relayTo serves a unix socket whose connections go to a session's attach
// stream on the host, as a client with no daemon of its own would: term's
// protocol, byte for byte.
func relayTo(t *testing.T, c *wire.Conn, session string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		pane, err := ln.Accept()
		if err != nil {
			return
		}
		s, err := c.Open(context.Background(), api.OpSessionAttach, api.SessionRef{ID: session}, nil)
		if err != nil {
			_ = pane.Close()
			return
		}
		go func() { _, _ = io.Copy(s, pane); _ = s.Close() }()
		_, _ = io.Copy(pane, s)
		_ = pane.Close()
	}()
	return sock
}

// TestDirectConnect: a client with no local daemon connects straight to a
// host, sees its groups and sets as the host has them, follows its changes,
// and attaches to any of its sessions, Remote or not.
func TestDirectConnect(t *testing.T) {
	host := newMachine(t, "host", hostConfig(freePort(t), true))
	host.start()
	var g ipc.Group
	host.call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupCreate, Text: "Backend"}, &g)
	a := host.newSession("api", false)
	dir := filepath.Join(host.home, "api")
	var b ipc.Session
	remote := false
	host.call(ipc.OpNew, ipc.NewRequest{Cwd: dir, Harness: core.HarnessTerminal, Name: "tests", SetWith: a.ID, Remote: &remote}, &b)
	host.call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutMove, IDs: []string{a.ID, b.ID}, GroupID: g.ID}, nil)

	c := dialHost(t, host)
	var info wire.Info
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Call(ctx, wire.OpInfo, nil, &info); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{api.OpCall, api.OpSubscribe, api.OpSessionAttach} {
		found := false
		for _, have := range info.Ops {
			found = found || have == op
		}
		if !found {
			t.Errorf("the host doesn't list %s: %v", op, info.Ops)
		}
	}

	// The host's tree, as the host has it.
	data, _ := json.Marshal(nil)
	var snap ipc.Snapshot
	if err := c.Call(ctx, api.OpCall, api.CallRequest{Op: ipc.OpSnapshot, Data: data}, &snap); err != nil {
		t.Fatal(err)
	}
	byID := map[string]*ipc.Session{}
	for _, s := range snap.Sessions {
		byID[s.ID] = s
	}
	if byID[a.ID] == nil || byID[b.ID] == nil || byID[a.ID].GroupID != g.ID || len(snap.Sets) != 1 ||
		byID[a.ID].Parent != snap.Sets[0].ID || byID[b.ID].Order != 2 || snap.Sets[0].Parent != g.ID {
		t.Fatalf("tree: sessions %+v %+v, sets %+v", byID[a.ID], byID[b.ID], snap.Sets)
	}
	found := false
	for _, x := range snap.Groups {
		found = found || x.ID == g.ID && x.Name == "Backend"
	}
	if !found {
		t.Errorf("groups = %+v", snap.Groups)
	}

	// It follows the host's changes.
	s, err := c.Open(ctx, api.OpSubscribe, ipc.SubscribeRequest{Types: []string{ipc.EntityGroup}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var ev ipc.Event
	if err := s.Receive(&ev); err != nil || ev.Type != ipc.EventSnapshot {
		t.Fatalf("first event = %+v %v", ev, err)
	}
	host.call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutGroupRename, GroupID: g.ID, Text: "API"}, nil)
	for {
		if err := s.Receive(&ev); err != nil {
			t.Fatal(err)
		}
		if ev.Type == ipc.EventUpdated && ev.ID == g.ID {
			break
		}
	}

	// It attaches to a session that isn't Remote.
	p := attach(t, relayTo(t, c, a.ID), 24, 80)
	p.in <- []byte("echo direct-$((6*7))\n")
	p.sees(t, "direct-42")
}
