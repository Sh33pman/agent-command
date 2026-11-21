package remote

import (
	"strconv"
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// TestPortFallback: with the port taken and the fallback on, Hosting listens
// on another port, and the same one again after a restart (state.db keeps
// it). Clients reach it there.
func TestPortFallback(t *testing.T) {
	taken := hold(t)
	host := newMachine(t, "host", hostConfig(taken, true))
	host.start()
	st := host.listening()
	if st.Port == taken || st.Port == 0 || st.ConfiguredPort != taken {
		t.Fatalf("status = %+v, with %d taken", st, taken)
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
