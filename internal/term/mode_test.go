package term

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/errcode"
)

// An attach asks for a mode: stream works as attaching always has, and any
// other is refused with not_supported before anything attaches, so a client
// can fall back.
func TestAttachModes(t *testing.T) {
	sock, _ := startServer(t)
	if !HasMode("") || !HasMode(ModeStream) || HasMode(ModeRendered) {
		t.Error("the modes")
	}
	if err := CheckMode(sock, ModeStream); err != nil {
		t.Errorf("stream: %v", err)
	}
	err := CheckMode(sock, ModeRendered)
	var me *ModeError
	if !errors.As(err, &me) || me.Code() != errcode.NotSupported || me.Mode != ModeRendered ||
		!strings.Contains(err.Error(), `"rendered"`) {
		t.Errorf("rendered: %v", err)
	}
	// The server says so itself.
	if b := ask(t, sock, frameMode, []byte(ModeStream)); string(b) != `{"ok":true}` {
		t.Errorf("mode frame for stream = %s", b)
	}
	if b := ask(t, sock, frameMode, []byte("rendered")); string(b) != `{"ok":false}` {
		t.Errorf("mode frame for rendered = %s", b)
	}

	// AttachMode refuses before it attaches.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := AttachMode(ctx, sock, ModeRendered, nil, nil, 24, 80, io.Discard); !errors.As(err, &me) {
		t.Errorf("attach rendered: %v", err)
	}
	// A client that skips the check: the server closes, with nothing sent.
	if b := ask(t, sock, frameAttach, attachPayload(24, 80, "rendered")); len(b) != 0 {
		t.Errorf("an attach in a mode it doesn't have got %q", b)
	}
	// Stream, by name, attaches as Attach does.
	out := &buf{}
	in := make(chan []byte, 1)
	done := make(chan error, 1)
	go func() { done <- AttachMode(ctx, sock, ModeStream, in, nil, 24, 80, out) }()
	in <- []byte("echo mode-$((3+3))\n")
	waitFor(t, "output", func() bool { return strings.Contains(out.String(), "mode-6") })
	cancel()
	if err := <-done; err != nil {
		t.Errorf("stream: %v", err)
	}
	if attachMode(attachPayload(1, 2, "")) != ModeStream || attachMode(nil) != ModeStream {
		t.Error("no mode is stream")
	}
}

// A server from before modes closes on a frame it doesn't know: that's
// not_supported too. With no server, the terminal has ended.
func TestCheckModeOnAnOlderServer(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "old.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _, _ = readFrame(c)
			_ = c.Close()
		}
	}()
	var me *ModeError
	if err := CheckMode(sock, ModeRendered); !errors.As(err, &me) {
		t.Errorf("an older server: %v", err)
	}
	if err := CheckMode(filepath.Join(t.TempDir(), "none.sock"), ModeRendered); !errors.Is(err, ErrEnded) {
		t.Errorf("no server: %v", err)
	}
	if err := CheckMode(filepath.Join(t.TempDir(), "none.sock"), ModeStream); err != nil {
		t.Errorf("stream isn't asked about: %v", err)
	}
}

// ask sends one frame and reads what the server writes until it closes.
func ask(t *testing.T, sock string, typ byte, payload []byte) []byte {
	t.Helper()
	c, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := writeFrame(c, typ, payload); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	b, _ := io.ReadAll(c)
	return b
}
