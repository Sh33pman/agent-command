//go:build darwin || linux

package term

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestForegroundFollowsWhatRunsInFront(t *testing.T) {
	dir, err := os.MkdirTemp("", "acmdterm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "t.sock")
	ready := make(chan struct{})
	// -i: job control, so a command gets its own process group, as in a login shell.
	s := &Server{Socket: sock, Shell: []string{"/bin/sh", "-i"}, Dir: dir, Env: []string{"PS1=$ ", "PATH=/usr/bin:/bin"},
		Ready: func() { close(ready) }}
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

// Asking what is in front reads the terminal's descriptor, and the server
// closes the terminal as it ends. The two must not overlap: run with -race.
func TestForegroundWhileTheServerEnds(t *testing.T) {
	for i := 0; i < 15; i++ {
		dir, err := os.MkdirTemp("", "acmdterm")
		if err != nil {
			t.Fatal(err)
		}
		sock := filepath.Join(dir, "t.sock")
		ready := make(chan struct{})
		s := &Server{Socket: sock, Shell: []string{"/bin/sh"}, Dir: dir, Env: []string{"PATH=/usr/bin:/bin"},
			Ready: func() { close(ready) }}
		ctx, cancel := context.WithCancel(context.Background())
		served := make(chan error, 1)
		go func() { served <- s.Serve(ctx) }()
		<-ready

		var asking sync.WaitGroup
		for j := 0; j < 8; j++ {
			asking.Add(1)
			go func() {
				defer asking.Done()
				for k := 0; k < 25; k++ {
					Foreground(sock)
				}
			}()
		}
		cancel()
		if err := <-served; err != nil {
			t.Error(err)
		}
		asking.Wait()
		_ = os.RemoveAll(dir)
	}
}

func TestProgramName(t *testing.T) {
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"/Users/me/.local/bin/claude", "--resume"}, "claude"},
		{[]string{"claude"}, "claude"},
		{[]string{"/opt/homebrew/bin/node", "--no-warnings", "/opt/homebrew/bin/gemini"}, "gemini"},
		{[]string{"node"}, "node"},
		{[]string{"vim", "x.go"}, "vim"},
	} {
		if got := programName(c.argv); got != c.want {
			t.Errorf("programName(%q) = %q, want %q", c.argv, got, c.want)
		}
	}
}

func TestForegroundOfNoServer(t *testing.T) {
	if _, ok := Foreground(filepath.Join(t.TempDir(), "none.sock")); ok {
		t.Fatal("ok without a server")
	}
}
