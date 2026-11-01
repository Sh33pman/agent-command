package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	xterm "github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ghostty"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/kitty"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/procenv"
	"github.com/brettmostert/agent-command/internal/setup"
	"github.com/brettmostert/agent-command/internal/term"
)

// Session terminals (SPEC §8.7): a shell per session, kept alive by a
// terminal server (`acmd term serve`), shown in a pane under the view's
// Claude pane (`acmd term pane`) while that session is shown. The
// shortcut creates it, or shows/hides it; it never ends one. exit/Ctrl-D
// in the shell, or closing its pane, ends it.

func newTermCmd() *cobra.Command {
	var win int
	cmd := &cobra.Command{
		Use:   "term",
		Short: "Show or hide the terminal of the session shown in this view (creates it the first time)",
		Long: `Each session can have its own terminal: a shell in the session's folder,
in a pane under the Claude pane. It keeps running while other sessions
are shown, and comes back with its session. This command creates it the
first time, then shows or hides it; it never ends it. Type exit (or
Ctrl-D) in it, or close its pane, to end it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				v, err := shortcutView(snap, win)
				if err != nil {
					return err
				}
				call := func(op string, in, out any) error { return c.conn.Call(op, in, out) }
				return toggleTerm(cmd.Context(), call, snap, v)
			})
		},
	}
	kittyWindowFlag(cmd, &win)
	cmd.AddCommand(newTermServeCmd(), newTermPaneCmd())
	return cmd
}

// toggleTerm creates the terminal of the session v shows, or shows/hides
// it.
func toggleTerm(ctx context.Context, call caller, snap ipc.Snapshot, v *core.View) error {
	var s *core.Session
	for _, x := range snap.Sessions {
		if x.ID == v.Selected {
			s = x
		}
	}
	if s == nil {
		return errors.New("no session is shown in this view")
	}
	t, ok := snap.Terminals[s.ID]
	switch {
	case !ok:
		if err := startTerm(ctx, call, s); err != nil {
			return err
		}
	case t.Shown:
		// Its pane closes by itself; the shell keeps running.
		if err := call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutTermShown, ID: s.ID, Flag: false}, nil); err != nil {
			return err
		}
		return focusPane(ctx, v.Pane)
	default:
		if err := call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutTermShown, ID: s.ID, Flag: true}, nil); err != nil {
			return err
		}
	}
	return ensureTermPane(ctx, call, v, true)
}

// termStart is how long acmd waits for a new terminal server.
var termStart = 5 * time.Second

// startTerm starts session s's terminal server (detached, in its own
// session, so it outlives panes, views and the terminal app) and waits
// until it reports itself to the daemon.
func startTerm(ctx context.Context, call caller, s *core.Session) error {
	p, err := paths.Default()
	if err != nil {
		return err
	}
	if !term.Running(p.TermSocket(s.ID)) {
		if err := spawnTerm(p, s.ID); err != nil {
			return err
		}
	}
	for deadline := time.Now().Add(termStart); time.Now().Before(deadline) && ctx.Err() == nil; time.Sleep(50 * time.Millisecond) {
		var snap ipc.Snapshot
		if err := call(ipc.OpSnapshot, nil, &snap); err != nil {
			return err
		}
		if _, ok := snap.Terminals[s.ID]; ok {
			return nil
		}
	}
	return fmt.Errorf("the terminal didn't start (see %s)", p.DaemonLog())
}

// spawnTerm starts `acmd term serve` for session id, detached (swappable
// in tests).
var spawnTerm = func(p paths.Paths, id string) error {
	exe, err := executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Clean(p.DaemonLog()), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	c := exec.Command(exe, "term", "serve", "--session", id) // #nosec G204 -- our own binary
	c.Env = procenv.Scrub(os.Environ())
	c.Stdout, c.Stderr = logf, logf
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}

// ensureTermPane makes sure v has its terminal pane under the view pane,
// adding it if it isn't there (it closes by itself when not needed).
// focus: give it focus; else focus stays on the view pane.
func ensureTermPane(ctx context.Context, call caller, v *core.View, focus bool) error {
	argv := []string{acmdPath(), "term", "pane", "--view", v.ID}
	loc := v.Sidebar
	switch v.Pane.Terminal {
	case "kitty":
		k := kittyAt(v.Pane.KittySocket)
		ws, err := k.LS(ctx)
		if err != nil {
			return err
		}
		if loc.TermWindow != 0 {
			if _, _, ok := kitty.Find(ws, loc.TermWindow); ok {
				if focus {
					return k.Focus(ctx, loc.TermWindow)
				}
				return nil
			}
		}
		lines := 12
		if w, _, ok := kitty.Find(ws, v.Pane.KittyWindow); ok {
			lines = max(8, w.Lines/3)
		}
		id, err := k.AddPaneBelow(ctx, kitty.BarSpec{View: v.Pane.KittyWindow, ViewID: v.ID, Lines: lines,
			Command: argv, Env: acmdEnv()}, "term")
		if err != nil {
			return err
		}
		loc.TermWindow = id
		if focus {
			_ = k.Focus(ctx, id)
		} else {
			_ = k.Focus(ctx, v.Pane.KittyWindow)
		}
	case "ghostty":
		g := ghosttyApp()
		if loc.TermTerm != "" {
			if ok, _ := g.Exists(ctx, loc.TermTerm); ok {
				if focus {
					return g.Focus(ctx, loc.TermTerm)
				}
				return nil
			}
		}
		id, err := g.Split(ctx, v.Pane.GhosttyTerm, "down", ghostty.Quote(argv), homeDir(), acmdEnv())
		if err != nil {
			return err
		}
		loc.TermTerm = id
		if focus {
			_ = g.Focus(ctx, id)
		}
	default:
		return errNoTerminal
	}
	if loc.Terminal == "" {
		loc.Terminal = v.Pane.Terminal
	}
	return call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutViewSidebar, ID: v.ID, Pane: &loc}, nil)
}

// termShowing brings back the terminal pane when the view shows a
// session whose terminal is shown (view.Loop.Showing).
func termShowing(vc *viewClient, viewID string) func(ctx context.Context, id string) {
	return func(ctx context.Context, id string) {
		snap, err := vc.Snapshot(ctx)
		if err != nil || !snap.Terminals[id].Shown {
			return
		}
		for _, v := range snap.Views {
			if v.ID == viewID {
				call := func(op string, in, out any) error { return vc.call(ctx, op, in, out) }
				_ = ensureTermPane(ctx, call, v, false)
			}
		}
	}
}

// termEnv is a session terminal's shell and environment: the login
// environment captured at init when there is one (SPEC §9.4).
func termEnv(p paths.Paths, s *core.Session) ([]string, []string) {
	env := os.Environ()
	if b, err := os.ReadFile(p.Env()); err == nil {
		var saved setup.LoginEnv
		if json.Unmarshal(b, &saved) == nil && saved["PATH"] != "" {
			env = saved.Environ()
		}
	}
	var out []string
	shell := ""
	for _, kv := range procenv.Scrub(env) {
		name, val, _ := splitEnv(kv)
		switch name {
		case "SHELL":
			shell = val
		case "TERM", "KITTY_WINDOW_ID", "KITTY_LISTEN_ON", "ACMD_VIEW_ID", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "PWD", "OLDPWD":
			continue // belong to whatever started acmd, not to this shell
		}
		out = append(out, kv)
	}
	if shell == "" {
		shell = "/bin/zsh"
	}
	out = append(out, "SHELL="+shell, "TERM=xterm-256color", "ACMD_SESSION_ID="+s.ID)
	return []string{shell, "-l"}, out
}

func splitEnv(kv string) (string, string, bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	return kv, "", false
}

func newTermServeCmd() *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:    "serve --session <id>",
		Short:  "Run a session's terminal (started by `acmd term`)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
			defer stop()
			vc := &viewClient{paths: p}
			defer vc.close()
			snap, err := vc.Snapshot(ctx)
			if err != nil {
				return err
			}
			var s *core.Session
			for _, x := range snap.Sessions {
				if x.ID == id {
					s = x
				}
			}
			if s == nil {
				return fmt.Errorf("no session %s", id)
			}
			dir := s.Cwd
			if st, err := os.Stat(dir); err != nil || !st.IsDir() {
				dir = homeDir()
			}
			shell, env := termEnv(p, s)
			report := func(running bool) {
				cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = vc.call(cctx, ipc.OpMutate, ipc.Mutation{Kind: ipc.MutTerm, ID: id, Flag: running}, nil)
			}
			if err := os.WriteFile(p.TermIDFile(id), []byte(id+"\n"), 0o600); err != nil {
				return err
			}
			defer os.Remove(p.TermIDFile(id))
			srv := &term.Server{Socket: p.TermSocket(id), Shell: shell, Dir: dir, Env: env,
				Ready: func() { report(true) }, Ended: func() { report(false) }}
			return srv.Serve(ctx)
		},
	}
	cmd.Flags().StringVar(&id, "session", "", "the session")
	return cmd
}

func newTermPaneCmd() *cobra.Command {
	var viewID string
	cmd := &cobra.Command{
		Use:    "pane --view <id>",
		Short:  "Run a view's terminal pane (started by `acmd term`)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			return runTermPane(cmd.Context(), p, viewID)
		},
	}
	cmd.Flags().StringVar(&viewID, "view", "", "the view this pane belongs to")
	return cmd
}

// runTermPane shows the terminal of the session the view shows, following
// the view; it returns (and its pane closes) when that session has no
// shown terminal or the view closes. Closing the pane (SIGHUP) ends the
// terminal shown in it, unless the whole view is going away.
func runTermPane(parent context.Context, p paths.Paths, viewID string) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	vc := &viewClient{paths: p}
	defer vc.close()

	fd := int(os.Stdin.Fd())
	if state, err := xterm.MakeRaw(uintptr(fd)); err == nil {
		defer func() { _ = xterm.Restore(uintptr(fd), state) }()
	}
	in := make(chan []byte, 16)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				in <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()
	sizes := make(chan [2]int, 1)
	size := func() (int, int) {
		cols, rows, err := xterm.GetSize(os.Stdout.Fd())
		if err != nil {
			return 24, 80
		}
		return rows, cols
	}
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGWINCH, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTSTP)
	defer signal.Stop(sigs)

	var mu sync.Mutex
	current := "" // the session shown in the pane
	go func() {
		for sig := range sigs {
			switch sig {
			case syscall.SIGWINCH:
				r, c := size()
				select {
				case sizes <- [2]int{r, c}:
				default:
				}
			case syscall.SIGHUP:
				// The pane was closed. If the view is going away too (the
				// tab or the app closed), only detach; else end the
				// terminal that was shown here.
				mu.Lock()
				id := current
				mu.Unlock()
				time.Sleep(time.Second)
				wctx, wcancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
				w, err := vc.Watch(wctx, ipc.WatchRequest{ViewID: viewID, Passive: true})
				wcancel()
				if id != "" && err == nil && !w.Gone {
					_ = term.Kill(p.TermSocket(id))
				}
				cancel()
				return
			case syscall.SIGTERM:
				cancel()
				return
			}
			// SIGINT/SIGQUIT/SIGTSTP: raw mode passes Ctrl+C/\/Z to the
			// shell as input; ignore any that arrive as signals.
		}
	}()

	var rev uint64
	for ctx.Err() == nil {
		w, err := vc.Watch(ctx, ipc.WatchRequest{ViewID: viewID, Passive: true})
		if err != nil || w.Gone {
			return nil
		}
		snap, err := vc.Snapshot(ctx)
		if err != nil {
			return nil
		}
		rev = w.Rev
		sel := w.View.Selected
		if sel == "" || !snap.Terminals[sel].Shown {
			return nil // nothing to show: the pane closes
		}
		mu.Lock()
		current = sel
		mu.Unlock()
		actx, stop := context.WithCancel(ctx)
		go func() { // follow the view: a new session, or this one hidden or gone
			defer stop()
			r := rev
			for {
				w, err := vc.Watch(actx, ipc.WatchRequest{ViewID: viewID, Rev: r, WaitMS: 25000, Passive: true})
				if err != nil || w.Gone || w.View.Selected != sel {
					return
				}
				if w.Rev != r {
					snap, err := vc.Snapshot(actx)
					if err != nil || !snap.Terminals[sel].Shown {
						return
					}
				}
				r = w.Rev
			}
		}()
		rows, cols := size()
		err = term.Attach(actx, p.TermSocket(sel), in, sizes, rows, cols, os.Stdout)
		stop()
		if errors.Is(err, term.ErrEnded) {
			// The shell exited; give its server a moment to tell the
			// daemon before looking again.
			select {
			case <-ctx.Done():
			case <-time.After(300 * time.Millisecond):
			}
		}
	}
	return nil
}
