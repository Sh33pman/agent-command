package remote

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/store"
)

// ui is a UI on m, registered as the app registers, using it as presence
// says: it stays connected until the test ends.
func (m *machine) ui(id string) *ipc.Conn {
	m.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := ipc.Dial(ctx, m.p.Socket(), ipc.Hello{Proto: ipc.Proto, Version: binVersion,
		Client: &ipc.ClientHello{ID: id, Kind: ipc.KindMac, Device: m.name, Caps: []string{ipc.CapNotify}}})
	if err != nil {
		m.t.Fatal(err)
	}
	m.t.Cleanup(func() { _ = c.Close() })
	return c
}

func use(t *testing.T, c *ipc.Conn, presence string) {
	t.Helper()
	if err := c.Call(ipc.OpClientPresence, ipc.ClientPresence{Presence: presence}, nil); err != nil {
		t.Fatal(err)
	}
}

// waits has the agent in a session on host wait for a permission, as Claude
// Code does: the host's notifier sends a "waiting" notice.
func waits(t *testing.T, host *machine, name string) *ipc.Session {
	t.Helper()
	s := host.newSession(name, false)
	payload := filepath.Join(host.home, name+".json")
	write(t, payload, `{"session_id":"c-`+name+`","hook_event_name":"PermissionRequest","tool_name":"Bash",`+
		`"tool_input":{"command":"make `+name+`"},"cwd":"`+s.Cwd+`"}`)
	agent := filepath.Join(host.home, name+".sh")
	write(t, agent, fmt.Sprintf("%q hook PermissionRequest < %q\nexec sleep 600\n", bin, payload))
	host.typeIn(s.ID, "sh "+agent+"\n")
	return s
}

// shown says whether m shows a notice about one of host's sessions.
func shown(m *machine, session string) bool {
	for _, n := range m.snapshot().Notices {
		if n.Host == "studio" && n.SessionID == session && n.Kind == "waiting" {
			return true
		}
	}
	return false
}

// A host routes its notices to the machine you're using, as to its own UIs
// (design 6.9, 6.12): each paired machine is one client there, with its UIs'
// presence, and passes the notice on to its UI. With route = all, every
// machine shows it. And a change made from a UI on a machine is recorded on
// the host as that UI's.
func TestNoticesGoToTheMachineYouAreUsing(t *testing.T) {
	cfg := strings.Replace(hostConfig(freePort(t), true), "enabled = false", "enabled = true\ncooldown = \"0s\"", 1)
	host := newMachine(t, "host", cfg)
	host.start()
	laptop := newMachine(t, "laptop", "[notify]\nenabled = true\n")
	laptop.start()
	desk := newMachine(t, "desk", "[notify]\nenabled = true\n")
	desk.start()
	laptop.pair("studio", host)
	desk.pair("studio", host)
	atLaptop, atDesk := laptop.ui("ui-laptop"), desk.ui("ui-desk")

	// You're at the laptop: the notice shows there, once.
	use(t, atDesk, ipc.PresenceIdle)
	use(t, atLaptop, ipc.PresenceActive)
	host.waitFor("both machines' presence", func() bool {
		var active, idle bool
		var list []ipc.Client
		host.call(ipc.OpClients, nil, &list)
		for _, c := range list {
			active = active || (c.Kind == ipc.KindMachine && c.Device == "laptop" && c.Presence == ipc.PresenceActive)
			idle = idle || (c.Kind == ipc.KindMachine && c.Device == "desk" && c.Presence == ipc.PresenceIdle)
		}
		return active && idle
	})
	first := waits(t, host, "first")
	laptop.waitFor("the notice on the laptop", func() bool { return shown(laptop, first.ID) })
	time.Sleep(300 * time.Millisecond)
	if shown(desk, first.ID) {
		t.Error("the idle desk showed it too")
	}

	// You move to the desk: the next goes there.
	use(t, atLaptop, ipc.PresenceIdle)
	use(t, atDesk, ipc.PresenceActive)
	host.waitFor("the desk active", func() bool {
		var list []ipc.Client
		host.call(ipc.OpClients, nil, &list)
		for _, c := range list {
			if c.Device == "desk" && c.Presence == ipc.PresenceActive {
				return true
			}
		}
		return false
	})
	second := waits(t, host, "second")
	desk.waitFor("the notice on the desk", func() bool { return shown(desk, second.ID) })
	time.Sleep(300 * time.Millisecond)
	if shown(laptop, second.ID) {
		t.Error("the idle laptop showed it too")
	}

	// With route = all, both.
	host.call(ipc.OpConfigSet, ipc.ConfigSet{Values: map[string]any{"notify.route": "all"}}, nil)
	third := waits(t, host, "third")
	laptop.waitFor("the third on the laptop", func() bool { return shown(laptop, third.ID) })
	desk.waitFor("the third on the desk", func() bool { return shown(desk, third.ID) })

	// A rename from the laptop's UI is recorded on the host as that UI's.
	if err := atLaptop.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutRename, Targets: ipc.On("studio", first.ID), Text: "from the laptop"}, nil); err != nil {
		t.Fatal(err)
	}
	host.waitFor("the rename", func() bool { s := host.session(first.ID); return s != nil && s.Name == "from the laptop" })
	st, err := store.Open(context.Background(), host.p.DB())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	evs, err := st.RecentEvents(context.Background(), "", 50)
	if err != nil {
		t.Fatal(err)
	}
	found := ""
	for _, e := range evs {
		if e.Detail == ipc.MutRename {
			found = e.Caller
		}
	}
	if !strings.Contains(found, "remote=") || !strings.Contains(found, "client=ui-laptop") || !strings.Contains(found, "device=laptop mac") {
		t.Errorf("the host recorded the rename as %q", found)
	}
}
