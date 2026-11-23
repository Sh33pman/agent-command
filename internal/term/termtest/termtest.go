// Package termtest is a stand-in for a session's terminal in tests, where
// there's no pty to run a real one: Windows, which has no ConPTY host and
// never hosts sessions. It speaks the terminal's frames (internal/term) and
// shows what's typed, so a client's half (its socket, the connection to a
// host, the host's relay) can be tested there for real.
package termtest

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
)

// Echo listens on sock until the test ends, as a terminal whose shell shows
// what's typed: input ('i') and typed-in ('w') frames come back as output.
func Echo(t testing.TB, sock string) {
	t.Helper()
	ln, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
}

// Listen is Echo for code that isn't a test's own (a fake host started by the
// code under test): it serves until the listener is closed.
func Listen(sock string) (net.Listener, error) {
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serve(c)
		}
	}()
	return ln, nil
}

// serve answers one connection: frames of [type][length uint32 BE][payload].
func serve(c net.Conn) {
	defer c.Close()
	for {
		var hdr [5]byte
		if _, err := io.ReadFull(c, hdr[:]); err != nil {
			return
		}
		p := make([]byte, binary.BigEndian.Uint32(hdr[1:]))
		if _, err := io.ReadFull(c, p); err != nil {
			return
		}
		switch hdr[0] {
		case 'i':
			if _, err := c.Write(p); err != nil {
				return
			}
		case 'w', 'k':
			return
		}
	}
}
