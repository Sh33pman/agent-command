package daemon

import (
	"context"
	"testing"

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
	if h := helloOf([]byte(`{"proto":1,"client":{"id":"x","kind":"tui"}}`)); h == nil || h.ID != "x" || h.Kind != ipc.KindTUI {
		t.Errorf("hello = %+v", h)
	}
}
