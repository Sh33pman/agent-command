package cli

import (
	"context"
	"testing"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/kitty"
	"github.com/brettmostert/agent-command/internal/kitty/kittytest"
)

func TestUserInAcmd(t *testing.T) {
	sim := kittytest.New() // windows 1 and 2; 1 focused
	origK, origF := kittyAt, frontmostApp
	kittyAt = func(string) *kitty.Kitty { return sim.Kitty() }
	t.Cleanup(func() { kittyAt, frontmostApp = origK, origF })
	frontmostApp = func() string { return "net.kovidgoyal.kitty" }
	ctx := context.Background()
	sock := "unix:/tmp/k"
	snap := ipc.Snapshot{
		Views: []*core.View{{ID: "v", Selected: "shown", Pane: core.PaneLoc{Terminal: "kitty", KittySocket: sock, KittyWindow: 1}}},
		Sessions: []*core.Session{
			{ID: "mine", Kind: core.KindExternal, Status: core.StatusDone, External: &core.PaneLoc{Terminal: "kitty", KittySocket: sock, KittyWindow: 2}},
		},
	}
	// The view's pane has focus: in acmd, looking at what it shows.
	if in, id := userInAcmd(ctx, snap); !in || id != "shown" {
		t.Errorf("view focused: %v %q", in, id)
	}
	// The user's own pane with a session has focus: not in acmd, but
	// looking at that session (no popup for it).
	sim.Focused = 2
	if in, id := userInAcmd(ctx, snap); in || id != "mine" {
		t.Errorf("own pane focused: %v %q", in, id)
	}
	// Another app in front: neither.
	frontmostApp = func() string { return "com.google.Chrome" }
	if in, id := userInAcmd(ctx, snap); in || id != "" {
		t.Errorf("other app: %v %q", in, id)
	}
}
