package view

import (
	"bytes"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

// runRelay relays a stand-in for `claude attach` that reads one line, then
// either shows Claude's agent view (sets the title) or exits.
func runRelay(t *testing.T, script, typed string) (relayed, string, error) {
	t.Helper()
	user, term, err := pty.Open() // user types into user; relay reads term
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { user.Close(); term.Close() })
	out := &syncBuf{}
	cmd := exec.Command("/bin/sh", "-c", script)
	type res struct {
		r   relayed
		err error
	}
	done := make(chan res, 1)
	go func() {
		r, err := relay(cmd, term, out, make(chan struct{}))
		done <- res{r, err}
	}()
	time.Sleep(300 * time.Millisecond)
	if _, err := user.Write([]byte(typed)); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		out.mu.Lock()
		defer out.mu.Unlock()
		return r.r, out.b.String(), r.err
	case <-time.After(10 * time.Second):
		t.Fatal("relay didn't end")
	}
	return relayed{}, "", nil
}

const agentView = `read line; echo "got:$line"; printf '\033]0;2 awaiting input \302\267 claude agents\007'; sleep 30`

func TestRelayExitFinishes(t *testing.T) {
	r, out, err := runRelay(t, agentView, "/exit\r")
	if err != nil || !r.agentView || !r.exitTyped {
		t.Errorf("/exit: %+v %v\n%q", r, err, out)
	}
	if !bytes.Contains([]byte(out), []byte("got:/exit")) {
		t.Errorf("input didn't reach the program: %q", out)
	}
}

func TestRelayLeftArrowOnlyLeaves(t *testing.T) {
	r, _, err := runRelay(t, agentView, "\x1b[D\r")
	if err != nil || !r.agentView || r.exitTyped {
		t.Errorf("←: %+v %v", r, err)
	}
	// Something else typed, then ←: not an exit either.
	r, _, _ = runRelay(t, agentView, "/ex is not it, hello\r")
	if r.exitTyped {
		t.Errorf("a prompt counted as /exit: %+v", r)
	}
}

func TestRelayProgramExits(t *testing.T) {
	r, _, err := runRelay(t, `read line; exit 3`, "bye\r")
	if r.agentView || err == nil {
		t.Errorf("exit: %+v %v", r, err)
	}
}

func TestTypingAndTitle(t *testing.T) {
	var ty typing
	ty.add([]byte("/ex\r")) // Claude completes it
	ty.add([]byte("\r"))
	if !ty.exitRecently(time.Now()) {
		t.Error("/ex + Enter + Enter isn't /exit")
	}
	if ty.exitRecently(time.Now().Add(time.Minute)) {
		t.Error("an old /exit still counts")
	}
	ty = typing{}
	ty.add([]byte("/q\r"))
	if ty.exitRecently(time.Now()) {
		t.Error("/q (too short) counted")
	}
	ty.add([]byte("/quiz\x7ft\r"))
	if !ty.exitRecently(time.Now()) {
		t.Error("/quit with backspaces not seen")
	}
	if !hasAgentTitle([]byte("x\x1b]0;claude agents\x07y")) || hasAgentTitle([]byte("\x1b]0;my claude agents notes ok\x07")) ||
		hasAgentTitle([]byte("\x1b]0;claude agents")) {
		t.Error("title detection")
	}
}
