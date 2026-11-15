//go:build darwin || linux

package peer

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pair is a connected unix socket, as the server sees it.
func pair(t *testing.T) net.Conn {
	t.Helper()
	// Not t.TempDir(): its path can be too long for a unix socket.
	dir, err := os.MkdirTemp("", "peer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	ln, err := net.Listen("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := ln.Accept(); err == nil {
			accepted <- c
		}
	}()
	client, err := net.Dial("unix", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	server := <-accepted
	t.Cleanup(func() { _ = server.Close() })
	return server
}

func TestCheckAcceptsTheSameUser(t *testing.T) {
	if err := Check(pair(t)); err != nil {
		t.Fatalf("same user: %v", err)
	}
}

func TestCheckRejectsAnotherUser(t *testing.T) {
	// The peer is this user, so asking for any other uid must fail.
	err := check(pair(t), os.Getuid()+1)
	if err == nil || !strings.Contains(err.Error(), "peer uid") {
		t.Fatalf("another user: %v", err)
	}
}

func TestCheckRejectsWhatIsNotAUnixSocket(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if err := Check(a); err == nil {
		t.Fatal("a pipe passed")
	}
}
