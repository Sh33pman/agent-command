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

// runRelay relays a stand-in for the attach client that reads one line,
// then runs the rest of script.
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

// The CLI exits after /exit is typed: the attach ends on its own.
func TestRelayExitFinishes(t *testing.T) {
	r, out, err := runRelay(t, `read line; echo "got:$line"`, "/exit\r")
	if err != nil || !r.exitTyped {
		t.Errorf("/exit: %+v %v\n%q", r, err, out)
	}
	if !bytes.Contains([]byte(out), []byte("got:/exit")) {
		t.Errorf("input didn't reach the program: %q", out)
	}
}

func TestRelayOtherInputIsNotExit(t *testing.T) {
	r, _, err := runRelay(t, `read line`, "hello\r")
	if err != nil || r.exitTyped {
		t.Errorf("a prompt: %+v %v", r, err)
	}
	r, _, _ = runRelay(t, `read line`, "/ex is not it, hello\r")
	if r.exitTyped {
		t.Errorf("a prompt counted as /exit: %+v", r)
	}
}

func TestRelayProgramFails(t *testing.T) {
	r, _, err := runRelay(t, `read line; exit 3`, "bye\r")
	if r.exitTyped || err == nil {
		t.Errorf("exit: %+v %v", r, err)
	}
}

func TestTyping(t *testing.T) {
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
}
