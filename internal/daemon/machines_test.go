package daemon

import (
	"context"
	"io"
	"log"
	"testing"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// A paired machine is one client here while it's connected, with its UIs'
// presence; it's routed to as the host's own UIs are. This machine's own
// presence, for its hosts, is its UIs': active while any is.
func TestPairedMachinesAreClients(t *testing.T) {
	d, _, _ := notifyDaemon(t, config.Default().Notify)
	d.cfg.Log = log.New(io.Discard, "", 0)
	ctx := context.Background()
	must(t, d.loadClients(ctx))

	if d.machinePresence() != ipc.PresenceBackground {
		t.Error("no UIs: background")
	}
	closed := d.machineConnected("SHA256:laptop", "laptop")
	desk := d.machineConnected("SHA256:desk", "desk")
	var list []ipc.Client
	for _, c := range d.clientList() {
		if c.Kind == ipc.KindMachine {
			list = append(list, c)
		}
	}
	if len(list) != 2 || list[0].Device == "" || !list[0].Connected {
		t.Fatalf("machines = %+v", list)
	}
	// Presence from a paired machine is the machine's, whatever UI asked.
	from := func(fp, ui string) context.Context {
		return context.WithValue(ctx, callerKey{}, &caller{remote: "x", machine: fp, clientID: ui})
	}
	must(t, d.presence(from("SHA256:desk", "ui-desk"), ipc.ClientPresence{Presence: ipc.PresenceIdle}))
	must(t, d.presence(from("SHA256:laptop", "ui-laptop"), ipc.ClientPresence{Presence: ipc.PresenceActive}))
	if got := d.route(config.RouteOne, "waiting"); len(got) != 1 || got[0] != "SHA256:laptop" {
		t.Errorf("route one = %v, want the active machine", got)
	}
	if got := d.route(config.RouteAll, "waiting"); len(got) != 2 {
		t.Errorf("route all = %v", got)
	}
	// Gone: the other one.
	closed()
	d.reg.mu.Lock()
	d.reg.clients["SHA256:laptop"].gone = d.cfg.Now().Add(-clientGap - 1)
	d.reg.mu.Unlock()
	if got := d.route(config.RouteOne, "waiting"); len(got) != 1 || got[0] != "SHA256:desk" {
		t.Errorf("after the laptop left: %v", got)
	}
	desk()

	// This machine's UIs: its presence for its hosts, and who asked.
	ui := &caller{}
	done := d.hello(ctx, &ipc.ClientHello{ID: "mac-1", Kind: ipc.KindMac, Device: "MacBook", Caps: []string{ipc.CapNotify}}, ui)
	defer done()
	uiCtx := context.WithValue(ctx, callerKey{}, ui)
	must(t, d.presence(uiCtx, ipc.ClientPresence{Presence: ipc.PresenceIdle}))
	if d.machinePresence() != ipc.PresenceIdle {
		t.Errorf("an idle UI: %s", d.machinePresence())
	}
	must(t, d.presence(uiCtx, ipc.ClientPresence{Presence: ipc.PresenceActive}))
	if d.machinePresence() != ipc.PresenceActive {
		t.Errorf("an active UI: %s", d.machinePresence())
	}
	if got := d.uiOf(uiCtx); got == nil || got.ID != "mac-1" || got.Kind != ipc.KindMac || got.Device != "MacBook" {
		t.Errorf("uiOf = %+v", got)
	}
	if d.uiOf(ctx) != nil {
		t.Error("a caller that didn't say who it is")
	}
	if got := d.uiOf(context.WithValue(ctx, callerKey{}, &caller{clientID: "unknown"})); got == nil || got.ID != "unknown" {
		t.Errorf("an unregistered UI: %+v", got)
	}
	if got := d.describe(&caller{remote: "laptop", machine: "SHA256:l", clientID: "ui-1", device: "laptop mac"}); got != "remote=laptop client=ui-1 device=laptop mac" {
		t.Errorf("describe = %q", got)
	}
	// No registry, no fingerprint: nothing to register.
	var none Daemon
	none.machineConnected("x", "y")()
	d.machineConnected("", "nameless")()
	d.tellHosts() // no client: nothing to tell
}
