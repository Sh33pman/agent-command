package remote

import (
	"strings"
	"testing"

	"github.com/brettmostert/agent-command/internal/ipc"
)

// TestPairing: an unknown client's request waits on the host. Approved, the
// client connects; denied, it stays out.
func TestPairing(t *testing.T) {
	host := newMachine(t, "host", hostConfig(freePort(t), true))
	host.start()
	laptop := newMachine(t, "laptop", clientConfig)
	laptop.start()
	desktop := newMachine(t, "desktop", clientConfig)
	desktop.start()

	request := func(c *machine, fp string) ipc.RemoteRequest {
		t.Helper()
		var found ipc.RemoteRequest
		host.waitFor("a request from "+c.name, func() bool {
			var reqs []ipc.RemoteRequest
			host.call(ipc.OpRemoteRequests, nil, &reqs)
			for _, r := range reqs {
				if r.Fingerprint == fp {
					found = r
					return true
				}
			}
			return false
		})
		return found
	}

	// Approved.
	rh := laptop.addHost("studio", host)
	if rh.OurFingerprint == "" {
		t.Fatalf("added = %+v", rh)
	}
	pending := laptop.waitHost("studio", ipc.HostPending)
	if !strings.Contains(pending.Error, "approve") {
		t.Errorf("pending error = %q", pending.Error)
	}
	req := request(laptop, rh.OurFingerprint)
	var approved ipc.RemoteClient
	host.call(ipc.OpRemoteApprove, ipc.RemoteRef{ID: req.ID, Name: "laptop"}, &approved)
	if approved.Fingerprint != rh.OurFingerprint {
		t.Errorf("approved = %+v", approved)
	}
	laptop.call(ipc.OpRemoteHostRetry, ipc.RemoteHostRef{Host: "studio"}, nil)
	connected := laptop.waitHost("studio", ipc.HostConnected)
	if connected.HostVersion != binVersion {
		t.Errorf("connected = %+v", connected)
	}

	// Denied.
	rd := desktop.addHost("studio", host)
	desktop.waitHost("studio", ipc.HostPending)
	req = request(desktop, rd.OurFingerprint)
	host.call(ipc.OpRemoteDeny, ipc.RemoteRef{ID: req.ID}, nil)
	// It asks again, and is only waiting again: never let in.
	desktop.call(ipc.OpRemoteHostRetry, ipc.RemoteHostRef{Host: "studio"}, nil)
	if again := request(desktop, rd.OurFingerprint); again.ID == req.ID {
		t.Errorf("the denied request is still there: %+v", again)
	}
	desktop.waitHost("studio", ipc.HostPending)
	var clients []ipc.RemoteClient
	host.call(ipc.OpRemoteClients, nil, &clients)
	if len(clients) != 1 || clients[0].Fingerprint != rh.OurFingerprint || len(clients[0].Connections) != 1 {
		t.Errorf("clients = %+v", clients)
	}
	if err := desktop.try(ipc.OpRemoteBrowse, ipc.RemoteHostRef{Host: "studio"}, nil); err == nil {
		t.Error("a denied client browsed the host")
	}
}

// TestAuthorizeByFingerprint: a client authorized ahead of time connects at
// once, and no request is left on the host.
func TestAuthorizeByFingerprint(t *testing.T) {
	host := newMachine(t, "host", hostConfig(freePort(t), true))
	host.start()
	laptop := newMachine(t, "laptop", clientConfig)
	laptop.start()
	laptop.pair("studio", host)
	var reqs []ipc.RemoteRequest
	host.call(ipc.OpRemoteRequests, nil, &reqs)
	if len(reqs) != 0 {
		t.Errorf("requests = %+v", reqs)
	}
	var browse ipc.RemoteBrowse
	laptop.call(ipc.OpRemoteBrowse, ipc.RemoteHostRef{Host: "studio"}, &browse)
	if browse.Host != "studio" || len(browse.Sessions) != 0 {
		t.Errorf("browse = %+v", browse)
	}
}
