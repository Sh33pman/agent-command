package term

import (
	"context"
	"os"
	"path/filepath"
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
