//go:build linux

package term

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestForegroundFollowsWhatRunsInFrontLinux(t *testing.T) {
	dir, err := os.MkdirTemp("", "acmdterm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "t.sock")
	ready := make(chan struct{})
	// -i: job control, so a command gets its own process group, as in a login shell.
	s := &Server{
		Socket: sock,
		Shell:  []string{"/bin/sh", "-i"},
		Dir:    dir,
		Env:    []string{"PS1=$ ", "PATH=/usr/bin:/bin"},
		Ready:  func() { close(ready) },
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Error(err)
		}
	})
	<-ready

	is := func(want Front) func() bool {
		return func() bool { f, ok := Foreground(sock); return ok && f == want }
	}
	waitFor(t, "the shell in front", is(Front{Shell: true}))
	if err := Send(sock, []byte("sleep 30\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "sleep in front", is(Front{Name: "sleep"}))
	if err := Send(sock, []byte{3}); err != nil { // Ctrl-C
		t.Fatal(err)
	}
	waitFor(t, "the shell back in front", is(Front{Shell: true}))
}
