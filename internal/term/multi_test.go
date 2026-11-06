package term

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// startServer runs a shell whose folder the test can use for files.
func startServer(t *testing.T) (sock, dir string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "acmdterm")
	if err != nil {
		t.Fatal(err)
	}
	sock = filepath.Join(dir, "t.sock")
	ready := make(chan struct{})
	s := &Server{Socket: sock, Shell: []string{"/bin/sh"}, Dir: dir,
		Env: []string{"PS1=$ ", "PATH=/usr/bin:/bin"}, Ready: func() { close(ready) }}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx) }()
	<-ready
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("serve: %v", err)
		}
		os.RemoveAll(dir)
	})
	return sock, dir
}

func attachTo(t *testing.T, sock string, rows, cols int, out *buf) chan<- []byte {
	t.Helper()
	in := make(chan []byte, 8)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Attach(ctx, sock, in, make(chan [2]int), rows, cols, out) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, ErrEnded) {
			t.Errorf("attach: %v", err)
		}
	})
	return in
}

func TestSeveralClients(t *testing.T) {
	sock, _ := startServer(t)
	a, b := &buf{}, &buf{}
	inA := attachTo(t, sock, 24, 80, a)
	attachTo(t, sock, 24, 80, b)
	inA <- []byte("echo both-$((1+1))\n")
	waitFor(t, "first client", func() bool { return strings.Contains(a.String(), "both-2") })
	waitFor(t, "second client", func() bool { return strings.Contains(b.String(), "both-2") })
}

func TestScreenText(t *testing.T) {
	sock, _ := startServer(t)
	if err := Send(sock, []byte("echo readable-$((2+2))\n")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "screen text", func() bool {
		s, err := ScreenText(sock)
		return err == nil && strings.Contains(s, "readable-4")
	})
}

func TestAttachSetsSize(t *testing.T) {
	sock, _ := startServer(t)
	out := &buf{}
	in := attachTo(t, sock, 31, 97, out)
	in <- []byte("stty size\n")
	waitFor(t, "size", func() bool { return strings.Contains(out.String(), "31 97") })
}

func TestEmptyAttachPayload(t *testing.T) {
	if got := attachSize(nil, 2); got != 0 {
		t.Errorf("attachSize(nil) = %d", got)
	}
	p := attachPayload(24, 80)
	if attachSize(p, 2) != 24 || attachSize(p, 4) != 80 {
		t.Errorf("attachPayload = %v", p)
	}
}

func TestSendAfterEnd(t *testing.T) {
	if err := Send(filepath.Join(t.TempDir(), "none.sock"), nil); !errors.Is(err, ErrEnded) {
		t.Errorf("Send = %v", err)
	}
	if _, err := ScreenText(filepath.Join(t.TempDir(), "none.sock")); !errors.Is(err, ErrEnded) {
		t.Errorf("ScreenText = %v", err)
	}
}
