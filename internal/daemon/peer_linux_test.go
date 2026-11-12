//go:build linux

package daemon

import (
	"net"
	"path/filepath"
	"testing"
)

func TestCheckPeerLinux(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "peer.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()

	client, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	server := <-accepted
	defer server.Close()

	if err := checkPeer(server); err != nil {
		t.Fatalf("checkPeer same user: %v", err)
	}
}
