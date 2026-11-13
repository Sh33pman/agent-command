//go:build libghostty

package term

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A client attaching later gets the exact state, however much output there was.
func TestReattachRestoresState(t *testing.T) {
	sock, _ := startServer(t)
	if err := Send(sock, []byte(`printf '\033[?1000h\033[?2004h'; seq 1 20000; echo marker-$((3+4))`+"\n")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "output", func() bool {
		s, err := ScreenText(sock)
		return err == nil && strings.Contains(s, "marker-7")
	})
	out := &buf{}
	attachTo(t, sock, 24, 80, out)
	waitFor(t, "restored state", func() bool {
		o := out.String()
		return strings.HasPrefix(o, "\x1bc") && strings.Contains(o, "marker-7") &&
			strings.Contains(o, "\x1b[?1000h") && strings.Contains(o, "\x1b[?2004h")
	})
	if !strings.Contains(out.String(), "1\r\n2\r\n3\r\n") {
		t.Errorf("the start of the history was not restored")
	}
}

const askCursor = `stty -icanon -echo; printf '\033[6n'; dd bs=1 count=4 of=reply 2>/dev/null; echo done-$((5+5))` + "\n"

// With nobody attached, the host answers the program's terminal queries.
func TestQueriesAnsweredWhileDetached(t *testing.T) {
	sock, dir := startServer(t)
	if err := Send(sock, []byte(askCursor)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "answer", func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "reply"))
		return err == nil && strings.HasPrefix(string(b), "\x1b[")
	})
}

// While attached the client's terminal answers: a second answer from the host
// would be typed into the program.
func TestQueriesLeftToAttachedClient(t *testing.T) {
	sock, dir := startServer(t)
	out := &buf{}
	in := attachTo(t, sock, 24, 80, out)
	in <- []byte(askCursor)
	waitFor(t, "query reaches the client", func() bool { return strings.Contains(out.String(), "\x1b[6n") })
	time.Sleep(200 * time.Millisecond)
	if b, _ := os.ReadFile(filepath.Join(dir, "reply")); len(b) != 0 {
		t.Errorf("the host answered while attached: %q", b)
	}
	in <- []byte("\x1b[1;1R")
	waitFor(t, "client's answer", func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "reply"))
		return err == nil && string(b) == "\x1b[1;"
	})
}
