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
)

func TestTypeViaAttach(t *testing.T) {
	e := viewEnvFor(t)
	origSettle, origDraw, origTake := replySettle, replyDraw, replyTake
	replySettle, replyDraw, replyTake = 100*time.Millisecond, 2*time.Second, time.Second
	t.Cleanup(func() { replySettle, replyDraw, replyTake = origSettle, origDraw, origTake })
	typed := filepath.Join(e.dir, "typed.txt")
	api := e.folder(t, "api", false)
	mustRun(t, "new", "--cwd", api, "--name", "auth")
	s := e.snapshot(t).Sessions[0]
	env := append(os.Environ(), "FAKECLAUDE_TYPED="+typed)
	fake := buildFakeClaude(t)
	got := func() string { b, _ := os.ReadFile(typed); return string(b) }

	// Text is pasted (bracketed) and sent with Enter.
	err := typeViaAttach(context.Background(), fake, env, s, replyText("hello there").keys(), true,
		func() bool { return strings.Contains(got(), "hello there") })
	if err != nil || !strings.Contains(got(), "\x1b[200~hello there\x1b[201~") {
		t.Errorf("text: %v %q", err, got())
	}
	// An option is its number key; nothing reacting is an error.
	err = typeViaAttach(context.Background(), fake, env, s, replyInput{Option: 1}.keys(), false, func() bool { return false })
	if err == nil || !strings.Contains(err.Error(), "didn't take the answer") {
		t.Errorf("no reaction: %v", err)
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
