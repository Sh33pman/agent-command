package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/term"
)

func TestTypeViaHost(t *testing.T) {
	dir, err := os.MkdirTemp("", "acmdreply")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	origEnter, origTake := replyEnterDelay, replyTake
	replyEnterDelay, replyTake = 50*time.Millisecond, time.Second
	t.Cleanup(func() { replyEnterDelay, replyTake = origEnter, origTake })
	typed := filepath.Join(dir, "typed.txt")
	sock := filepath.Join(dir, "h.sock")
	// A stand-in session: it records what it is typed.
	srv := &term.Server{Socket: sock, Shell: []string{"/bin/sh", "-c", "cat > " + typed}, Dir: dir, Env: []string{"PATH=/usr/bin:/bin"}}
	go func() { _ = srv.Serve(context.Background()) }()
	waitUntil(t, "host", func() bool { return term.Running(sock) })
	got := func() string { b, _ := os.ReadFile(typed); return string(b) }

	// Text is pasted (bracketed) and sent with Enter.
	err = typeViaHost(context.Background(), sock, replyText("hello there").keys(), true,
		func() bool { return strings.Contains(got(), "hello there\x1b[201~\n") })
	if err != nil || !strings.Contains(got(), "\x1b[200~hello there\x1b[201~") {
		t.Errorf("text: %v %q", err, got())
	}
	// Nothing reacting is an error.
	err = typeViaHost(context.Background(), sock, replyInput{Option: 1}.keys(), false, func() bool { return false })
	if err == nil || !strings.Contains(err.Error(), "didn't take the answer") {
		t.Errorf("no reaction: %v", err)
	}
	// No host: a clear error.
	if err := typeViaHost(context.Background(), filepath.Join(dir, "none.sock"), []byte("x"), false, func() bool { return true }); err == nil ||
		!strings.Contains(err.Error(), "host isn't running") {
		t.Errorf("no host: %v", err)
	}
}

func TestPopupBindings(t *testing.T) {
	bs := popupBindings(config.Default().Popup.Keys)
	want := map[string]string{"prev": "left cmd,ctrl", "next": "right cmd,ctrl", "play": "p cmd,ctrl", "skip": "k cmd,ctrl", "close": "escape "}
	for _, b := range bs {
		if got := b.Key + " " + strings.Join(b.Mods, ","); got != want[b.Action] {
			t.Errorf("%s: %q, want %q", b.Action, got, want[b.Action])
		}
	}
	if len(bs) != 5 {
		t.Errorf("%d bindings", len(bs))
	}
	if b := popupBindings(config.PopupKeys{Close: "esc", Play: "Command+Option+P"}); b[0].Key != "p" || strings.Join(b[0].Mods, ",") != "cmd,alt" || b[1].Key != "escape" {
		t.Errorf("aliases: %+v", b)
	}
	if _, err := optionIndex(&core.InboxItem{Options: []string{"a"}}, 3); err == nil {
		t.Error("option out of range accepted")
	}
}
