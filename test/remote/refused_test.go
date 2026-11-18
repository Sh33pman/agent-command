package remote

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/remote/wire"
)

// TestHostFingerprintMismatch: a host that shows another fingerprint than the
// pinned one is refused, with an error that says so and names both.
func TestHostFingerprintMismatch(t *testing.T) {
	host := newMachine(t, "host", hostConfig(freePort(t), true))
	host.start()
	laptop := newMachine(t, "laptop", clientConfig)
	laptop.start()
	host.call(ipc.OpRemoteAuthorize, ipc.RemoteRef{Fingerprint: laptop.identity().Fingerprint(), Name: "laptop"}, nil)

	other, err := identity.Ensure(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	laptop.call(ipc.OpRemoteHostAdd, ipc.RemoteHostEdit{Name: "studio", Address: host.address(), Fingerprint: other.Fingerprint()}, nil)
	_ = laptop.try(ipc.OpRemoteBrowse, ipc.RemoteHostRef{Host: "studio"}, nil)
	h := laptop.waitHost("studio", ipc.HostFingerprint)
	actual := host.status().Fingerprint
	if h.Seen != actual || !strings.Contains(h.Error, actual) || !strings.Contains(h.Error, other.Fingerprint()) ||
		!strings.Contains(h.Error, "fingerprint") {
		t.Errorf("host = %+v", h)
	}
	if err := laptop.try(ipc.OpRemoteBrowse, ipc.RemoteHostRef{Host: "studio"}, nil); err == nil || !strings.Contains(errText(err), "fingerprint") {
		t.Errorf("browse: %v", err)
	}
	var clients []ipc.RemoteClient
	host.call(ipc.OpRemoteClients, nil, &clients)
	if len(clients) != 1 || len(clients[0].Connections) != 0 {
		t.Errorf("clients = %+v", clients)
	}

	// Trusting the real fingerprint lets it in.
	laptop.call(ipc.OpRemoteHostEdit, ipc.RemoteHostEdit{Host: "studio", Fingerprint: actual}, nil)
	_ = laptop.try(ipc.OpRemoteBrowse, ipc.RemoteHostRef{Host: "studio"}, nil)
	laptop.waitHost("studio", ipc.HostConnected)
}

// Both sides of a version mismatch, against a peer of a newer protocol. wire's
// Proto is a constant both real daemons share, so the other side is a fake
// that speaks the hello by hand: the least change to acmd that tests the
// real daemons' refusals.
const newerProto = wire.Proto + 1

// TestVersionMismatch: a client refuses a host of another protocol, and a
// host refuses a client of one, even an authorized one.
func TestVersionMismatch(t *testing.T) {
	t.Run("the client refuses the host", func(t *testing.T) {
		id, err := identity.Ensure(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		go fakeHost(ln, id)

		laptop := newMachine(t, "laptop", clientConfig)
		laptop.start()
		laptop.call(ipc.OpRemoteHostAdd, ipc.RemoteHostEdit{Name: "newer", Address: ln.Addr().String(), Fingerprint: id.Fingerprint()}, nil)
		_ = laptop.try(ipc.OpRemoteBrowse, ipc.RemoteHostRef{Host: "newer"}, nil)
		h := laptop.waitHost("newer", ipc.HostVersion)
		if !strings.Contains(h.Error, "protocol") || !strings.Contains(h.Error, "9.9.9") {
			t.Errorf("host = %+v", h)
		}
	})

	t.Run("the host refuses the client", func(t *testing.T) {
		host := newMachine(t, "host", hostConfig(freePort(t), true))
		host.start()
		id, err := identity.Ensure(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		host.call(ipc.OpRemoteAuthorize, ipc.RemoteRef{Fingerprint: id.Fingerprint(), Name: "newer"}, nil)
		st := host.listening()

		raw, err := net.DialTimeout("tcp", host.address(), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
		tc := tls.Client(raw, wire.ClientTLS(id, st.Fingerprint))
		if err := tc.HandshakeContext(context.Background()); err != nil {
			t.Fatal(err)
		}
		sendLine(t, tc, wire.Hello{Proto: newerProto, Version: "9.9.9", Name: "newer"})
		var w wire.Welcome
		r := bufio.NewReader(tc)
		// The whole line: its newline can come in a TLS record of its own.
		line, err := r.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(line, &w); err != nil {
			t.Fatal(err)
		}
		if w.Status != wire.StatusVersion || w.Proto != wire.Proto || !strings.Contains(w.Message, "9.9.9") {
			t.Errorf("welcome = %+v", w)
		}
		// And it hung up.
		if _, err := r.ReadByte(); err == nil {
			t.Error("the host kept the connection open")
		}
		var clients []ipc.RemoteClient
		host.call(ipc.OpRemoteClients, nil, &clients)
		if len(clients) != 1 || len(clients[0].Connections) != 0 {
			t.Errorf("clients = %+v", clients)
		}
	})
}

// fakeHost answers every hello as a host of a newer protocol does.
func fakeHost(ln net.Listener, id *identity.Identity) {
	for {
		raw, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer raw.Close()
			_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
			tc := tls.Server(raw, wire.ServerTLS(id))
			if tc.Handshake() != nil {
				return
			}
			var hello wire.Hello
			if json.NewDecoder(bufio.NewReader(tc)).Decode(&hello) != nil {
				return
			}
			w := wire.Welcome{Hello: wire.Hello{Proto: newerProto, Version: "9.9.9", Name: "newer"}, Status: wire.StatusVersion,
				Message: "this host speaks a newer protocol"}
			b, _ := json.Marshal(w)
			_, _ = tc.Write(append(b, '\n'))
		}()
	}
}

func sendLine(t *testing.T, c net.Conn, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}
