package remote

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// TestPortFallback: with the port taken and the fallback on, Hosting listens
// on another port, and the same one again after a restart. Clients reach it
// there.
func TestPortFallback(t *testing.T) {
	taken := hold(t)
	host := newMachine(t, "host", hostConfig(taken, true))
	host.start()
	st := host.listening()
	if st.Port == taken || st.Port == 0 || st.ConfiguredPort != taken {
		t.Fatalf("status = %+v, with %d taken", st, taken)
	}
	var saved struct {
		Port struct{ Configured, Port int }
	}
	b, err := os.ReadFile(host.p.RemoteHostState())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &saved); err != nil || saved.Port.Port != st.Port || saved.Port.Configured != taken {
		t.Errorf("remote-host.json = %s (%v)", b, err)
	}

	laptop := newMachine(t, "laptop", clientConfig)
	laptop.start()
	laptop.pair("studio", host)

	host.stop()
	host.start()
	if again := host.listening(); again.Port != st.Port {
		t.Errorf("after a restart it listens on %d, not %d as before", again.Port, st.Port)
	}
	laptop.waitHost("studio", ipc.HostConnected)
}

// TestOwnPortTaken: with the fallback off, a port that's taken is an error
// that names it, and Hosting doesn't listen.
func TestOwnPortTaken(t *testing.T) {
	taken := hold(t)
	host := newMachine(t, "host", hostConfig(taken, false))
	host.start()
	var st ipc.RemoteStatus
	host.waitFor("Hosting's error", func() bool {
		st = host.status()
		return st.Error != ""
	})
	if st.Listening || st.Port != 0 || !strings.Contains(st.Error, strconv.Itoa(taken)) || !st.Enabled {
		t.Errorf("status = %+v", st)
	}
}
