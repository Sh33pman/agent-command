package term

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// buf is a goroutine-safe output sink.
type buf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *buf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *buf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestServerAttachDetachReplay(t *testing.T) {
	dir, err := os.MkdirTemp("", "acmdterm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "t.sock")
	ready, ended := make(chan struct{}), make(chan struct{})
	s := &Server{Socket: sock, Shell: []string{"/bin/sh"}, Dir: dir, Env: []string{"PS1=$ ", "PATH=/usr/bin:/bin"},
		Ready: func() { close(ready) }, Ended: func() { close(ended) }}
	served := make(chan error, 1)
	go func() { served <- s.Serve(context.Background()) }()
	<-ready

	attach := func(out *buf) (chan<- []byte, context.CancelFunc, <-chan error) {
		in := make(chan []byte, 8)
		ctx, cancel := context.WithCancel(context.Background())
		res := make(chan error, 1)
		go func() { res <- Attach(ctx, sock, in, make(chan [2]int), 24, 80, out) }()
		return in, cancel, res
	}

	// Input reaches the shell; its output comes back.
	out1 := &buf{}
	in1, detach1, res1 := attach(out1)
	in1 <- []byte("echo one-$((1+1))\n")
	waitFor(t, "first output", func() bool { return strings.Contains(out1.String(), "one-2") })

	// A probe doesn't take the client over.
	if !Running(sock) {
		t.Fatal("not running")
	}
	in1 <- []byte("echo still-$((2+1))\n")
	waitFor(t, "output after a probe", func() bool { return strings.Contains(out1.String(), "still-3") })

	// Detach; output while nobody is attached is kept and replayed.
	detach1()
	if err := <-res1; err != nil {
		t.Fatalf("detach: %v", err)
	}
	out2 := &buf{}
	in2, detach2, res2 := attach(out2)
	in2 <- []byte("sleep 0.2; echo later-$((3+1))\n")
	waitFor(t, "live output", func() bool { return strings.Contains(out2.String(), "later-4") })
	detach2()
	if err := <-res2; err != nil {
		t.Fatalf("detach: %v", err)
	}
	out3 := &buf{}
	_, detach3, res3 := attach(out3)
	waitFor(t, "replay", func() bool {
		o := out3.String()
		return (strings.HasPrefix(o, "\x1b[H\x1b[2J") || strings.HasPrefix(o, "\x1bc")) && strings.Contains(o, "one-2") && strings.Contains(o, "later-4")
	})
	detach3()
	if err := <-res3; err != nil {
		t.Fatalf("detach: %v", err)
	}

	// exit ends it: the attached client gets ErrEnded, the socket goes.
	out4 := &buf{}
	in4, _, res4 := attach(out4)
	in4 <- []byte("exit\n")
	if err := <-res4; !errors.Is(err, ErrEnded) {
		t.Errorf("after exit: %v", err)
	}
	<-ended
	if err := <-served; err != nil {
		t.Errorf("serve: %v", err)
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) || Running(sock) {
		t.Errorf("socket left: %v", err)
	}
}

func TestKill(t *testing.T) {
	dir, err := os.MkdirTemp("", "acmdterm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "t.sock")
	ready := make(chan struct{})
	s := &Server{Socket: sock, Shell: []string{"/bin/sh"}, Dir: dir, Env: []string{"PATH=/usr/bin:/bin"}, Ready: func() { close(ready) }}
	served := make(chan error, 1)
	go func() { served <- s.Serve(context.Background()) }()
	<-ready
	if err := Kill(sock); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("kill didn't end the terminal")
	}
}

func TestSendTypesWithoutAttaching(t *testing.T) {
	dir, err := os.MkdirTemp("", "acmdsend")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock, typed := filepath.Join(dir, "t.sock"), filepath.Join(dir, "typed")
	s := &Server{Socket: sock, Shell: []string{"/bin/sh", "-c", "cat > " + typed}, Dir: dir, Env: []string{"PATH=/usr/bin:/bin"}}
	go func() { _ = s.Serve(context.Background()) }()
	waitFor(t, "server", func() bool { return Running(sock) })
	if err := Send(sock, []byte("hi there\n")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "typed text", func() bool { b, _ := os.ReadFile(typed); return strings.Contains(string(b), "hi there") })
	if err := Send(filepath.Join(dir, "none.sock"), []byte("x")); !errors.Is(err, ErrEnded) {
		t.Errorf("send without a server: %v", err)
	}
}
