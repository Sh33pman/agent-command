package daemon

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// A client names itself in its hello; the daemon lists it, takes its
// presence, and notices name it.
func TestClientsOverTheSocket(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	hello := ipc.Hello{Proto: ipc.Proto, Version: testVersion,
		Client: &ipc.ClientHello{ID: "mac-1", Kind: ipc.KindMac, Device: "studio", Caps: []string{ipc.CapNotify}, OnHost: true}}
	c, err := ipc.Dial(context.Background(), p.Socket(), hello)
	must(t, err)
	defer c.Close()
	must(t, c.Call(ipc.OpClientPresence, ipc.ClientPresence{Presence: ipc.PresenceActive, Focus: "s1"}, nil))
	var list []ipc.Client
	must(t, c.Call(ipc.OpClients, nil, &list))
	if len(list) != 1 || list[0].ID != "mac-1" || !list[0].Connected || list[0].Presence != ipc.PresenceActive || !list[0].OnHost ||
		list[0].LastActive.IsZero() {
		t.Fatalf("clients = %+v", list)
	}
	anon := dial(t, p)
	if err := anon.Call(ipc.OpClientPresence, ipc.ClientPresence{Presence: ipc.PresenceActive}, nil); err == nil {
		t.Error("presence from a connection that didn't say who it is")
	}
	if err := anon.Call(ipc.OpNoticeGet, ipc.NoticeRef{ID: 7}, nil); err == nil {
		t.Error("a notice that isn't there")
	}
	for _, bad := range []string{ipc.OpClientPresence, ipc.OpNoticeGet} {
		if err := c.Call(bad, "x", nil); err == nil {
			t.Errorf("%s took a request that isn't one", bad)
		}
	}
	c.Close()
	waitFor(t, "the client to go", func() bool {
		must(t, anon.Call(ipc.OpClients, nil, &list))
		return len(list) == 1 && list[0].Presence == ipc.PresenceDisconnected
	})
}

func TestHelloOfReadsTheClient(t *testing.T) {
	if helloOf([]byte(`nope`)) != nil || helloOf([]byte(`{"proto":1}`)) != nil {
		t.Error("a hello without a client")
	}
	if h := helloOf([]byte(`{"proto":1,"client":{"id":"x","kind":"terminal"}}`)); h == nil || h.ID != "x" || h.Kind != ipc.KindTerminal {
		t.Errorf("hello = %+v", h)
	}
}

// A client's profile says which notices it shows: routing skips one that
// doesn't want a kind. The popup's trigger is kept for the client.
func TestProfilesShapeRouting(t *testing.T) {
	d, _, _ := notifyDaemon(t, config.Default().Notify)
	ctx := context.Background()
	must(t, d.loadClients(ctx))
	mac, _ := connect(t, d, "mac-1", ipc.KindMac, ipc.CapNotify)
	connect(t, d, "terminal-1", ipc.KindTerminal, ipc.CapNotify)
	use(t, d, mac, ipc.PresenceActive)
	macCtx := context.WithValue(ctx, callerKey{}, mac)
	p, err := d.setProfile(macCtx, ipc.ClientProfile{Notify: []string{"waiting", "error"}, PopupOpens: ipc.PopupNeedsYou})
	must(t, err)
	if got, _ := d.profile(macCtx); !slices.Equal(got.Notify, p.Notify) || got.PopupOpens != ipc.PopupNeedsYou {
		t.Errorf("profile = %+v", got)
	}
	if got := d.route(config.RouteOne, "waiting"); !slices.Equal(got, []string{"mac-1"}) {
		t.Errorf("waiting: %v", got)
	}
	if got := d.route(config.RouteOne, "done"); !slices.Equal(got, []string{"terminal-1"}) {
		t.Errorf("done goes to the one that wants it: %v", got)
	}
	if got := d.route(config.RouteAll, "withdraw"); len(got) != 2 {
		t.Errorf("a withdrawal goes to all: %v", got)
	}
	for _, bad := range []ipc.ClientProfile{{Notify: []string{"news"}}, {PopupOpens: "sometimes"}} {
		if _, err := d.setProfile(macCtx, bad); err == nil {
			t.Errorf("took %+v", bad)
		}
	}
	if _, err := d.setProfile(ctx, ipc.ClientProfile{}); err == nil {
		t.Error("a profile for no one")
	}
	if _, err := d.profile(ctx); err == nil {
		t.Error("read a profile for no one")
	}
	// Kept: a reload reads it back.
	must(t, d.loadClients(ctx))
	if got, _ := d.profile(macCtx); got.PopupOpens != ipc.PopupNeedsYou {
		t.Errorf("reloaded profile = %+v", got)
	}
}

// Clients register their keymaps; acmd keys reads the desktop app's.
func TestKeymapsAreRegistered(t *testing.T) {
	d, _, _ := notifyDaemon(t, config.Default().Notify)
	ctx := context.Background()
	must(t, d.loadClients(ctx))
	if _, err := d.keymap(ctx, ipc.KeysRequest{}); resultOf("", err).Code != ipc.CodeNotFound {
		t.Errorf("none yet: %v", err)
	}
	mac, _ := connect(t, d, "mac-1", ipc.KindMac)
	phone, _ := connect(t, d, "phone", ipc.KindAndroid)
	must(t, d.setKeymap(context.WithValue(ctx, callerKey{}, mac), json.RawMessage(`{"sections":[]}`)))
	must(t, d.setKeymap(context.WithValue(ctx, callerKey{}, phone), json.RawMessage(`{"gestures":[]}`)))
	if m, err := d.keymap(ctx, ipc.KeysRequest{}); err != nil || m.ClientID != "mac-1" || string(m.Map) != `{"sections":[]}` || m.Kind != ipc.KindMac {
		t.Errorf("the desktop app's: %+v %v", m, err)
	}
	if m, err := d.keymap(ctx, ipc.KeysRequest{ClientID: "phone"}); err != nil || m.ClientID != "phone" {
		t.Errorf("by id: %+v %v", m, err)
	}
	if _, err := d.keymap(ctx, ipc.KeysRequest{ClientID: "nope"}); err == nil {
		t.Error("no such client")
	}
	if err := d.setKeymap(context.WithValue(ctx, callerKey{}, mac), json.RawMessage(`[1]`)); err == nil {
		t.Error("a keymap that isn't an object")
	}
	if err := d.setKeymap(ctx, json.RawMessage(`{}`)); err == nil {
		t.Error("a keymap from no one")
	}
}

// The per-client ops over the socket, as the calling client.
func TestClientStateOverTheSocket(t *testing.T) {
	p := testPaths(t)
	start(t, p)
	c, err := ipc.Dial(context.Background(), p.Socket(), ipc.Hello{Proto: ipc.Proto, Version: testVersion,
		Client: &ipc.ClientHello{ID: "mac-1", Kind: ipc.KindMac, Caps: []string{ipc.CapNotify}}})
	must(t, err)
	defer c.Close()
	var prof ipc.ClientProfile
	must(t, c.Call(ipc.OpClientProfileSet, ipc.ClientProfile{PopupOpens: ipc.PopupNever}, &prof))
	must(t, c.Call(ipc.OpClientProfile, nil, &prof))
	if prof.PopupOpens != ipc.PopupNever {
		t.Errorf("profile = %+v", prof)
	}
	must(t, c.Call(ipc.OpKeysSet, map[string]any{"sections": []any{}}, nil))
	var m ipc.Keymap
	must(t, c.Call(ipc.OpKeys, nil, &m))
	if m.ClientID != "mac-1" {
		t.Errorf("keys = %+v", m)
	}
	// Dismissing without naming a client is the caller's.
	var ids []string
	must(t, c.Call(ipc.OpHealthDismiss, ipc.HealthRef{ID: "hooks"}, &ids))
	var snap ipc.Snapshot
	must(t, c.Call(ipc.OpSnapshot, nil, &snap))
	if !slices.Equal(snap.Dismissed, []string{"hooks"}) {
		t.Errorf("dismissed = %v", snap.Dismissed)
	}
	for _, op := range []string{ipc.OpClientProfileSet, ipc.OpKeys} {
		if err := c.Call(op, "x", nil); err == nil {
			t.Errorf("%s took a request that isn't one", op)
		}
	}
}
