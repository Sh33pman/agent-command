//go:build linux

package linuxui

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

func startMockDaemon(t *testing.T, handler func(conn net.Conn, r *bufio.Reader)) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "daemon.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer func() { _ = conn.Close() }()
				r := bufio.NewReader(conn)
				handler(conn, r)
			}(c)
		}
	}()
	return sock
}

func readReq(t *testing.T, r *bufio.Reader) ipc.Request {
	t.Helper()
	var req ipc.Request
	if err := ipc.ReadJSON(r, &req); err != nil {
		t.Fatalf("read request: %v", err)
	}
	return req
}

func writeOK(t *testing.T, w net.Conn, data any) {
	t.Helper()
	var raw json.RawMessage
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			t.Fatalf("marshal reply: %v", err)
		}
		raw = b
	}
	if err := ipc.WriteJSON(w, ipc.Response{OK: true, Data: raw}); err != nil {
		t.Fatalf("write reply: %v", err)
	}
}

func TestClientCallAndPresence(t *testing.T) {
	var seenHello ipc.Hello
	var seenPresence ipc.ClientPresence

	sock := startMockDaemon(t, func(conn net.Conn, r *bufio.Reader) {
		for {
			var req ipc.Request
			if err := ipc.ReadJSON(r, &req); err != nil {
				return
			}
			switch req.Op {
			case ipc.OpHello:
				_ = json.Unmarshal(req.Data, &seenHello)
				writeOK(t, conn, ipc.Hello{
					Proto:    ipc.Proto,
					ProtoMin: ipc.ProtoMin,
					ProtoMax: ipc.Proto,
					Version:  "0.0.0",
					Ops:      []string{ipc.OpSnapshot, ipc.OpClientPresence, ipc.OpSubscribe},
				})
			case ipc.OpClientPresence:
				_ = json.Unmarshal(req.Data, &seenPresence)
				writeOK(t, conn, nil)
			case ipc.OpSnapshot:
				writeOK(t, conn, ipc.Snapshot{
					Sessions: []*ipc.Session{{ID: "s1", Name: "alpha", Status: core.StatusRunning}},
				})
			}
		}
	})

	c, err := NewClient(Options{Socket: sock, ClientID: "linux-test", Device: "testbox", Version: "0.0.0"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	if c.ID() != "linux-test" {
		t.Fatalf("ID() = %q, want linux-test", c.ID())
	}

	ctx := context.Background()
	if err := c.SetPresence(ctx, ipc.PresenceActive, "s1"); err != nil {
		t.Fatalf("SetPresence: %v", err)
	}
	if seenHello.Client == nil || seenHello.Client.Kind != ipc.KindLinux || !seenHello.Client.OnHost {
		t.Fatalf("unexpected ClientHello: %+v", seenHello.Client)
	}
	if seenPresence.Presence != ipc.PresenceActive || seenPresence.Focus != "s1" {
		t.Fatalf("unexpected presence: %+v", seenPresence)
	}

	var snap ipc.Snapshot
	if err := c.Call(ctx, ipc.OpSnapshot, nil, &snap); err != nil {
		t.Fatalf("Call snapshot: %v", err)
	}
	if len(snap.Sessions) != 1 || snap.Sessions[0].ID != "s1" {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
}

func TestClientSubscribeAndStore(t *testing.T) {
	sock := startMockDaemon(t, func(conn net.Conn, r *bufio.Reader) {
		req := readReq(t, r)
		if req.Op != ipc.OpHello {
			return
		}
		writeOK(t, conn, ipc.Hello{Proto: ipc.Proto, ProtoMin: ipc.ProtoMin, ProtoMax: ipc.Proto, Version: "0.0.0"})

		req = readReq(t, r)
		if req.Op != ipc.OpSubscribe {
			return
		}
		writeOK(t, conn, nil)

		_ = ipc.WriteJSON(conn, ipc.Event{
			Rev:  1,
			Type: ipc.EventSnapshot,
			Snapshot: &ipc.Snapshot{
				Sessions:  []*ipc.Session{{ID: "s1", Name: "one", Status: core.StatusRunning}},
				Harnesses: []ipc.Harness{{ID: "h1", Name: "Work", Kind: core.HarnessClaude, Title: "Claude", Installed: true}},
			},
		})
		s2, _ := json.Marshal(ipc.Session{ID: "s2", Name: "two", Status: core.StatusWaiting})
		_ = ipc.WriteJSON(conn, ipc.Event{
			Rev:    2,
			Type:   ipc.EventAdded,
			Entity: ipc.EntitySession,
			ID:     "s2",
			Data:   s2,
		})
		_ = ipc.WriteJSON(conn, ipc.Event{
			Rev:    3,
			Type:   ipc.EventRemoved,
			Entity: ipc.EntitySession,
			ID:     "s1",
		})
	})

	c, err := NewClient(Options{Socket: sock, ClientID: "linux-test", Version: "0.0.0"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	store := NewStore()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	events := 0
	go c.RunStream(ctx, store, func(ev ipc.Event) {
		events++
		if ev.Rev == 3 {
			cancel()
		}
	}, nil)

	<-ctx.Done()
	snap, rev, ok := store.Snapshot()
	if !ok {
		t.Fatal("expected snapshot to be loaded")
	}
	if rev != 3 {
		t.Fatalf("rev = %d, want 3", rev)
	}
	if len(snap.Sessions) != 1 || snap.Sessions[0].ID != "s2" {
		t.Fatalf("unexpected sessions after add+remove: %+v", snap.Sessions)
	}
	if len(snap.Harnesses) != 1 || snap.Harnesses[0].ID != "h1" {
		t.Fatalf("unexpected harnesses: %+v", snap.Harnesses)
	}
	if events < 3 {
		t.Fatalf("expected at least 3 events, got %d", events)
	}
}

func TestStoreApplyEntities(t *testing.T) {
	store := NewStore()
	changed, err := store.Apply(ipc.Event{
		Rev:  10,
		Type: ipc.EventSnapshot,
		Snapshot: &ipc.Snapshot{
			Groups: []*ipc.Group{{ID: "g1", Name: "Default", IsDefault: true}},
		},
	})
	if err != nil || !changed {
		t.Fatalf("snapshot apply: changed=%v err=%v", changed, err)
	}

	hData, _ := json.Marshal(ipc.Harness{ID: "h1", Name: "Personal", Kind: core.HarnessClaude, Installed: true})
	if changed, err := store.Apply(ipc.Event{Rev: 11, Type: ipc.EventAdded, Entity: ipc.EntityHarness, ID: "h1", Data: hData}); err != nil || !changed {
		t.Fatalf("harness add: changed=%v err=%v", changed, err)
	}

	nData, _ := json.Marshal(ipc.Notice{Seq: 5, SessionID: "s1", Kind: "waiting", Title: "Needs input", To: []string{"linux-test"}})
	if changed, err := store.Apply(ipc.Event{Rev: 12, Type: ipc.EventAdded, Entity: ipc.EntityNotice, Data: nData}); err != nil || !changed {
		t.Fatalf("notice add: changed=%v err=%v", changed, err)
	}

	if changed, err := store.Apply(ipc.Event{Rev: 13, Type: ipc.EventRemoved, Entity: ipc.EntityHarness, ID: "h1"}); err != nil || !changed {
		t.Fatalf("harness remove: changed=%v err=%v", changed, err)
	}

	gData, _ := json.Marshal(ipc.Group{ID: "g2", Name: "Side"})
	if _, err := store.Apply(ipc.Event{Rev: 14, Type: ipc.EventAdded, Entity: ipc.EntityGroup, ID: "g2", Data: gData}); err != nil {
		t.Fatalf("group add: %v", err)
	}
	gUpdated, _ := json.Marshal(ipc.Group{ID: "g2", Name: "Side Updated"})
	if _, err := store.Apply(ipc.Event{Rev: 15, Type: ipc.EventUpdated, Entity: ipc.EntityGroup, ID: "g2", Data: gUpdated}); err != nil {
		t.Fatalf("group update: %v", err)
	}
	setData, _ := json.Marshal(ipc.Set{ID: "set1"})
	if _, err := store.Apply(ipc.Event{Rev: 16, Type: ipc.EventAdded, Entity: ipc.EntitySet, ID: "set1", Data: setData}); err != nil {
		t.Fatalf("set add: %v", err)
	}
	inData, _ := json.Marshal(core.InboxItem{ID: 42, SessionID: "s1"})
	if _, err := store.Apply(ipc.Event{Rev: 17, Type: ipc.EventAdded, Entity: ipc.EntityInbox, ID: "42", Data: inData}); err != nil {
		t.Fatalf("inbox add: %v", err)
	}
	vsData, _ := json.Marshal(ipc.ViewState{Mode: "list", Sort: "name"})
	if _, err := store.Apply(ipc.Event{Rev: 18, Type: ipc.EventUpdated, Entity: ipc.EntityView, Data: vsData}); err != nil {
		t.Fatalf("view update: %v", err)
	}
	settsData, _ := json.Marshal(map[string]any{"theme": "Tokyo Night"})
	if _, err := store.Apply(ipc.Event{Rev: 19, Type: ipc.EventUpdated, Entity: ipc.EntitySettings, Data: settsData}); err != nil {
		t.Fatalf("settings update: %v", err)
	}
	hlData, _ := json.Marshal([]ipc.Health{{ID: "ok"}})
	if _, err := store.Apply(ipc.Event{Rev: 20, Type: ipc.EventUpdated, Entity: ipc.EntityHealth, Data: hlData}); err != nil {
		t.Fatalf("health update: %v", err)
	}
	if _, err := store.Apply(ipc.Event{Rev: 21, Type: ipc.EventRemoved, Entity: ipc.EntityInbox, ID: "42"}); err != nil {
		t.Fatalf("inbox remove: %v", err)
	}
	if _, err := store.Apply(ipc.Event{Rev: 22, Type: ipc.EventRemoved, Entity: ipc.EntitySet, ID: "set1"}); err != nil {
		t.Fatalf("set remove: %v", err)
	}
	if _, err := store.Apply(ipc.Event{Rev: 23, Type: ipc.EventRemoved, Entity: ipc.EntityGroup, ID: "g2"}); err != nil {
		t.Fatalf("group remove: %v", err)
	}

	snap, rev, ok := store.Snapshot()
	if !ok || rev != 23 {
		t.Fatalf("snapshot state: ok=%v rev=%d", ok, rev)
	}
	if len(snap.Harnesses) != 0 || len(snap.Inbox) != 0 || len(snap.Sets) != 0 || len(snap.Groups) != 1 {
		t.Fatalf("unexpected entity counts: harnesses=%d inbox=%d sets=%d groups=%d",
			len(snap.Harnesses), len(snap.Inbox), len(snap.Sets), len(snap.Groups))
	}
	if snap.ViewState.Mode != "list" || snap.Settings["theme"] != "Tokyo Night" || len(snap.Health) != 1 {
		t.Fatalf("unexpected view/settings/health: %+v %+v %+v", snap.ViewState, snap.Settings, snap.Health)
	}
	if len(snap.Notices) != 1 || snap.Notices[0].Seq != 5 {
		t.Fatalf("unexpected notices: %+v", snap.Notices)
	}
}

func TestNewClientDefaults(t *testing.T) {
	c, err := NewClient(Options{})
	if err != nil {
		t.Fatalf("NewClient default: %v", err)
	}
	defer c.Close()
	if c.ID() == "" {
		t.Fatal("expected non-empty default ClientID")
	}
}

func TestClientAllowsVersionMismatch(t *testing.T) {
	sock := startMockDaemon(t, func(conn net.Conn, r *bufio.Reader) {
		for {
			var req ipc.Request
			if err := ipc.ReadJSON(r, &req); err != nil {
				return
			}
			switch req.Op {
			case ipc.OpHello:
				writeOK(t, conn, ipc.Hello{
					Proto:    ipc.Proto,
					ProtoMin: ipc.ProtoMin,
					ProtoMax: ipc.Proto,
					Version:  "0.92.0",
				})
			case ipc.OpSnapshot:
				writeOK(t, conn, ipc.Snapshot{})
			}
		}
	})

	c, err := NewClient(Options{Socket: sock, ClientID: "linux-test", Version: "0.0.0-dev"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	var snap ipc.Snapshot
	if err := c.Call(context.Background(), ipc.OpSnapshot, nil, &snap); err != nil {
		t.Fatalf("Call with version mismatch: %v", err)
	}
}

func TestClientRetriesStalePooledConn(t *testing.T) {
	var calls atomic.Int32
	sock := startMockDaemon(t, func(conn net.Conn, r *bufio.Reader) {
		for {
			var req ipc.Request
			if err := ipc.ReadJSON(r, &req); err != nil {
				return
			}
			switch req.Op {
			case ipc.OpHello:
				writeOK(t, conn, ipc.Hello{Proto: ipc.Proto, ProtoMin: ipc.ProtoMin, ProtoMax: ipc.Proto, Version: "0.0.0"})
			case ipc.OpSnapshot:
				if calls.Add(1) == 2 {
					// Simulate stale pooled connection closing on the second call.
					_ = conn.Close()
					return
				}
				writeOK(t, conn, ipc.Snapshot{})
			}
		}
	})

	c, err := NewClient(Options{Socket: sock, ClientID: "linux-test", Version: "0.0.0"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	var snap ipc.Snapshot
	if err := c.Call(context.Background(), ipc.OpSnapshot, nil, &snap); err != nil {
		t.Fatalf("first Call: %v", err)
	}
	if err := c.Call(context.Background(), ipc.OpSnapshot, nil, &snap); err != nil {
		t.Fatalf("second Call (stale retry): %v", err)
	}
}
