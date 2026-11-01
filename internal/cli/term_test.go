package cli

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/kitty"
	"github.com/brettmostert/agent-command/internal/kitty/kittytest"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/term"
)

func TestSessionTerminal(t *testing.T) {
	e := viewEnvFor(t)
	sim := kittytest.New()
	origK, origSpawn := kittyAt, spawnTerm
	kittyAt = func(string) *kitty.Kitty { return sim.Kitty() }
	t.Setenv("SHELL", "/bin/sh")
	// The server runs in this process (the test binary isn't acmd).
	served := make(chan error, 4)
	spawnTerm = func(_ paths.Paths, id string) error {
		go func() {
			_, err := acmd(t, "term", "serve", "--session", id)
			served <- err
		}()
		return nil
	}
	t.Cleanup(func() { kittyAt, spawnTerm = origK, origSpawn })
	ctx := context.Background()

	var v core.View
	must(t, e.conn.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutViewOpen, Pane: &core.PaneLoc{Terminal: "kitty", KittySocket: "unix:/tmp/k", KittyWindow: 1}}, &v))
	_, err := addSidebar(ctx, e.conn.Call, &v, "")
	must(t, err)

	// No session shown: refused.
	if _, err := acmd(t, "term", "--kitty-window", "1"); err == nil || !strings.Contains(err.Error(), "no session is shown") {
		t.Errorf("without a session: %v", err)
	}
	api := e.folder(t, "api", false)
	mustRun(t, "new", "--cwd", api, "--name", "auth")
	s := e.snapshot(t).Sessions[0]
	mustRun(t, "go", "auth")

	// First press: the terminal starts and its pane opens under the view.
	mustRun(t, "term", "--kitty-window", "1")
	snap := e.snapshot(t)
	if tm, ok := snap.Terminals[s.ID]; !ok || !tm.Shown {
		t.Fatalf("terminal %+v %v", tm, ok)
	}
	sb := snap.Views[0].Sidebar
	ws := sim.Tabs[0].Windows
	found := false
	for i, w := range ws {
		if w.ID == sb.TermWindow {
			found = w.UserVars["acmd_role"] == "term" && i > 0 && ws[i-1].ID == 1 && w.Lines >= 8
		}
	}
	if !found || sim.Focused != sb.TermWindow {
		t.Errorf("term pane %d (focused %d): %+v", sb.TermWindow, sim.Focused, ws)
	}
	p, _ := paths.Default()
	if !term.Running(p.TermSocket(s.ID)) {
		t.Error("terminal server not running")
	}

	// Pressing it in the terminal pane hides it (the shell keeps running);
	// again shows it, without a second pane.
	mustRun(t, "term", "--kitty-window", itoa(sb.TermWindow))
	if snap := e.snapshot(t); snap.Terminals[s.ID].Shown || !term.Running(p.TermSocket(s.ID)) {
		t.Errorf("hide: %+v", snap.Terminals)
	}
	n := len(sim.Tabs[0].Windows)
	mustRun(t, "term", "--kitty-window", "1")
	if snap := e.snapshot(t); !snap.Terminals[s.ID].Shown || len(sim.Tabs[0].Windows) != n {
		t.Errorf("show: %+v, %d windows (was %d)", snap.Terminals, len(sim.Tabs[0].Windows), n)
	}

	// Showing the session again brings a closed pane back (view hook).
	must(t, sim.Kitty().Close(ctx, sb.TermWindow))
	vc := &viewClient{paths: p}
	termShowing(vc, v.ID)(ctx, s.ID)
	vc.close()
	if nb := e.snapshot(t).Views[0].Sidebar.TermWindow; nb == 0 || nb == sb.TermWindow || sim.Focused == nb {
		t.Errorf("pane not brought back (or it took focus): %d", nb)
	}

	// Ending it (exit, or closing its pane): it's gone for the daemon.
	must(t, term.Kill(p.TermSocket(s.ID)))
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("term serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("terminal server didn't end")
	}
	eventually(t, "terminal gone", func() bool {
		_, ok := e.snapshot(t).Terminals[s.ID]
		return !ok
	})
	if _, err := os.Stat(p.TermIDFile(s.ID)); !os.IsNotExist(err) {
		t.Errorf("id file left: %v", err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
