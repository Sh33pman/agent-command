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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/keymap"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/procenv"
	"github.com/brettmostert/agent-command/internal/setup"
	"github.com/brettmostert/agent-command/internal/version"
	"github.com/brettmostert/agent-command/internal/view"
)

// Views (SPEC §6.4): `acmd` turns the pane it runs in into a view. The
// sidebar split arrives with the Kitty adapter (M2b); until then the
// view's placeholder lists sessions.

// runView registers this pane as a view and runs the view loop in it.
func runView(cmd *cobra.Command, viewID string) error {
	p, err := paths.Default()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGHUP, syscall.SIGTERM)
	defer stop()
	vc := &viewClient{paths: p}
	defer vc.close()

	var v core.View
	if viewID == "" {
		pane := currentPane(os.Getenv)
		if pane.Terminal == "ghostty" {
			pane.TTY = ttyName()
			if pane.TTY != "" {
				if id, err := ghosttyApp().TermForTTY(ctx, pane.TTY); err == nil {
					pane.GhosttyTerm = id
				} else {
					fmt.Fprintf(cmd.ErrOrStderr(), "acmd: couldn't find this Ghostty pane (%v); the view works without a sidebar\n", err)
				}
			}
		}
		if err := vc.call(ctx, ipc.OpMutate, ipc.Mutation{Kind: ipc.MutViewOpen, ID: startSessionID, Pane: &pane}, &v); err != nil {
			return err
		}
		viewID = v.ID
		if (pane.Terminal == "kitty" && pane.KittySocket != "" && pane.KittyWindow != 0) || (pane.Terminal == "ghostty" && pane.GhosttyTerm != "") {
			call := func(op string, in, out any) error { return vc.call(ctx, op, in, out) }
			launchDir, _ := os.Getwd()
			if v.Sidebar, err = addSidebar(ctx, call, &v, launchDir); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "acmd: couldn't add the sidebar (%v); the view works without it\n", err)
			}
		}
	} else {
		// Re-executed after an upgrade (or started by acmd): the view and
		// its sidebar exist already.
		var snap ipc.Snapshot
		if err := vc.call(ctx, ipc.OpSnapshot, nil, &snap); err != nil {
			return err
		}
		for _, x := range snap.Views {
			if x.ID == viewID {
				v = *x
			}
		}
	}
	reexecing := false
	defer func() {
		if reexecing {
			return
		}
		cctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		removeSidebar(cctx, &v)
	}()
	env := append(procenv.Scrub(os.Environ()), "ACMD_VIEW_ID="+viewID)
	cwd, _ := os.Getwd()
	loop := &view.Loop{
		ViewID: viewID,
		Client: vc,
		Out:    cmd.OutOrStdout(),
		Keys:   &view.TermKeys{In: os.Stdin},
		Attach: func(s *core.Session) *exec.Cmd {
			exe, _ := executable()
			c := exec.Command(exe, "host", "attach", "--session", s.ID) // #nosec G204 -- our own binary
			c.Stdin, c.Stdout, c.Stderr, c.Env = os.Stdin, os.Stdout, os.Stderr, env
			return c
		},
		New: func(ctx context.Context) error {
			_, err := startSession(ctx, cmd, newOptions{cwd: cwd, showIn: viewID})
			return err
		},
		Showing: termShowing(vc, viewID),
		Hints:   placeholderHints(p),
		Term:    terminalIn(),
	}
	err = loop.Run(ctx)
	switch {
	case errors.Is(err, view.ErrClosed):
		return nil
	case errors.Is(err, errNewerDaemon):
		reexecing = true
		return reexec("view", "--view", viewID)
	case ctx.Err() != nil:
		// The pane closed (SIGHUP) or we were asked to stop: the view goes,
		// its session keeps running (SPEC §6.4).
		cctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = vc.Close(cctx, viewID)
		return nil
	}
	return err
}

// currentPane describes the pane acmd runs in.
func currentPane(getenv func(string) string) core.PaneLoc {
	loc := core.PaneLoc{Terminal: "unknown"}
	switch {
	case getenv("KITTY_WINDOW_ID") != "":
		loc.Terminal, loc.KittySocket = "kitty", getenv("KITTY_LISTEN_ON")
		_, _ = fmt.Sscan(getenv("KITTY_WINDOW_ID"), &loc.KittyWindow)
	case getenv("TERM_PROGRAM") == "ghostty":
		loc.Terminal = "ghostty"
	}
	return loc
}

func newViewCmd() *cobra.Command {
	var viewID, pinned, start string
	cmd := &cobra.Command{
		Use:    "view",
		Short:  "Run the view loop in this pane (started by `acmd`)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if pinned != "" {
				return runPinned(cmd, pinned)
			}
			startSessionID = start
			return runView(cmd, viewID)
		},
	}
	cmd.Flags().StringVar(&viewID, "view", "", "the view to run (default: register this pane)")
	cmd.Flags().StringVar(&pinned, "pinned", "", "just show this session; detaching closes the pane (split out)")
	cmd.Flags().StringVar(&start, "start", "", "start the new view on this session (split out with sidebar)")
	return cmd
}

// startSessionID is the session a new view starts on ("" = most recent).
var startSessionID string

// runPinned shows one session in this pane (split out, session only,
// SPEC §6.6): attach until the user detaches, then the pane closes.
func runPinned(cmd *cobra.Command, id string) error {
	p, err := paths.Default()
	if err != nil {
		return err
	}
	vc := &viewClient{paths: p}
	defer vc.close()
	snap, err := vc.Snapshot(cmd.Context())
	if err != nil {
		return err
	}
	var s *core.Session
	for _, x := range snap.Sessions {
		if x.ID == id {
			s = x
		}
	}
	if s == nil || !s.Managed() {
		return errors.New("that session can't be shown here")
	}
	if s.Stopped() || s.NeedsResume() {
		if s, err = vc.Resume(cmd.Context(), id); err != nil {
			return err
		}
	}
	// Ctrl+C/Ctrl+\/Ctrl+Z are for claude attach. Caught (not ignored)
	// signals revert to the default in the child; ignored ones wouldn't.
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTSTP)
	defer signal.Stop(sigs)
	go func() {
		for range sigs {
		}
	}()
	return attachHost(cmd.Context(), p.HostSocket(s.ID))
}

// errNewerDaemon means the daemon runs another acmd version: this view
// loop re-executes the installed binary (SPEC §4.3, version skew).
var errNewerDaemon = errors.New("daemon runs another acmd version")

// viewClient implements view.Client. Watch gets its own connection per
// call (it blocks, and runs alongside other calls).
type viewClient struct {
	paths paths.Paths
	mu    sync.Mutex
	conn  *ipc.Conn
}

func (v *viewClient) dial(ctx context.Context) (*ipc.Conn, error) {
	me := ipc.Hello{Proto: ipc.Proto, Version: version.Get().Version}
	conn, err := ipc.Dial(ctx, v.paths.Socket(), me)
	var mismatch *ipc.ErrVersionMismatch
	if errors.As(err, &mismatch) {
		_ = conn.Close()
		if installedVersion() != me.Version {
			return nil, errNewerDaemon
		}
		// We are the installed version: replace the old daemon.
	}
	if err == nil {
		return conn, nil
	}
	c, err := connect(ctx)
	if err != nil {
		return nil, err
	}
	return c.conn, nil
}

// installedVersion is the version of the acmd at the stable path ("" if
// it can't be run).
var installedVersion = func() string {
	exe, _ := executable()
	out, err := exec.Command(setup.StableBinary(invokedAs, exe, exec.LookPath), "version", "--json").Output() // #nosec G204 -- our own binary
	if err != nil {
		return ""
	}
	var info version.Info
	if json.Unmarshal(out, &info) != nil {
		return ""
	}
	return info.Version
}

// reexec replaces this process with the installed acmd, running args.
var reexec = func(args ...string) error {
	path := acmdPath()
	return syscall.Exec(path, append([]string{path}, args...), os.Environ()) // #nosec G204 -- our own binary
}

func (v *viewClient) call(ctx context.Context, op string, in, out any) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	for attempt := 0; ; attempt++ {
		if v.conn == nil {
			conn, err := v.dial(ctx)
			if err != nil {
				return err
			}
			v.conn = conn
		}
		err := v.conn.Call(op, in, out)
		var remote *ipc.RemoteError
		if err == nil || errors.As(err, &remote) || attempt > 0 {
			return err
		}
		// The connection broke (daemon restarted): dial again once.
		_ = v.conn.Close()
		v.conn = nil
	}
}

func (v *viewClient) close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.conn != nil {
		_ = v.conn.Close()
		v.conn = nil
	}
}

func (v *viewClient) Watch(ctx context.Context, req ipc.WatchRequest) (ipc.WatchResponse, error) {
	var resp ipc.WatchResponse
	conn, err := v.dial(ctx)
	if err != nil {
		return resp, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer conn.Close()
	err = conn.Call(ipc.OpWatch, req, &resp)
	if ctx.Err() != nil {
		return resp, ctx.Err()
	}
	return resp, err
}

func (v *viewClient) Snapshot(ctx context.Context) (ipc.Snapshot, error) {
	var s ipc.Snapshot
	err := v.call(ctx, ipc.OpSnapshot, nil, &s)
	return s, err
}

func (v *viewClient) Select(ctx context.Context, viewID, sessionID string) error {
	return v.call(ctx, ipc.OpMutate, ipc.Mutation{Kind: ipc.MutViewSelect, ID: viewID, Text: sessionID}, nil)
}

func (v *viewClient) Close(ctx context.Context, viewID string) error {
	return v.call(ctx, ipc.OpMutate, ipc.Mutation{Kind: ipc.MutViewClose, ID: viewID}, nil)
}

func (v *viewClient) Finish(ctx context.Context, id string) error {
	return v.call(ctx, ipc.OpMutate, ipc.Mutation{Kind: ipc.MutFinish, ID: id}, nil)
}

// terminalIn is stdin when it's a terminal (the view relays attach then).
func terminalIn() *os.File {
	if st, err := os.Stdin.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
		return os.Stdin
	}
	return nil
}

func (v *viewClient) Resume(ctx context.Context, id string) (*core.Session, error) {
	var s core.Session
	err := v.call(ctx, ipc.OpResume, ipc.Mutation{ID: id}, &s)
	return &s, err
}

// ---- acmd new (SPEC §6.1) ----

type newOptions struct {
	groupID     string // a known group (sidebar); else group is resolved by name
	group       string
	noCreate    bool
	cwd         string
	name        string
	description string
	prompt      string
	showIn      string // view to switch ("" = none)
}

func newNewCmd() *cobra.Command {
	var o newOptions
	var noFollow bool
	var win int
	var viewID string
	cmd := &cobra.Command{
		Use:   "new [-- <first prompt>]",
		Short: "Start a new Claude session hosted by acmd",
		Long: "Starts an interactive `claude` in a folder (default: this one), hosted by acmd, and lists it. Run inside a view, the view\n" +
			"switches to it. A folder Claude hasn't trusted yet shows Claude's trust prompt in the session.",
		RunE: func(cmd *cobra.Command, args []string) error {
			o.prompt = strings.TrimSpace(strings.Join(args, " "))
			if win > 0 {
				// From a shortcut: follow into the view it came from; start
				// in the folder of what it shows (SPEC §6.1, decision 24).
				return withClient(cmd.Context(), func(_ *client, snap ipc.Snapshot) error {
					v, err := shortcutView(snap, win)
					if err != nil {
						return err
					}
					dir := o.cwd
					if dir == "" {
						dir = newFolder(snap, v)
					}
					return newFromShortcut(cmd.Context(), cmd, v, "", dir)
				})
			}
			if o.cwd == "" {
				o.cwd, _ = os.Getwd()
			}
			switch {
			case viewID != "":
				o.showIn = viewID
			case !noFollow:
				o.showIn = os.Getenv("ACMD_VIEW_ID")
			}
			s, err := startSession(cmd.Context(), cmd, o)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Started %s in %s (%s).\n", nameOf(s), setup.Tilde(s.Cwd, homeDir()), attachNote(s))
			if o.showIn == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "Show it in a view: acmd go "+s.Ref())
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.group, "group", "", "group to put it in (created if missing)")
	f.BoolVar(&o.noCreate, "no-create", false, "fail instead of creating a missing --group")
	f.StringVar(&o.cwd, "cwd", "", "folder to start in (default: the current folder)")
	f.StringVar(&o.name, "name", "", "session name (default: Claude's)")
	f.StringVar(&o.description, "description", "", "session description")
	f.BoolVar(&noFollow, "no-follow", false, "don't switch the current view to it")
	f.StringVar(&viewID, "view", "", "the view to show it in")
	_ = f.MarkHidden("view")
	f.StringVar(&o.groupID, "group-id", "", "the group's id (used by the sidebar)")
	_ = f.MarkHidden("group-id")
	kittyWindowFlag(cmd, &win)
	return cmd
}

// newFolder is where a shortcut starts a session (SPEC §6.1 step 2).
func newFolder(snap ipc.Snapshot, v *core.View) string {
	return view.NewFolder(snap, v, homeDir())
}

// startSession resolves the group and starts the hosted session. Claude's
// trust prompt, for a folder it hasn't trusted, shows up in the session.
func startSession(ctx context.Context, _ *cobra.Command, o newOptions) (*core.Session, error) {
	p, err := paths.Default()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(p.Config())
	if err != nil {
		return nil, err
	}
	dir, err := filepath.Abs(o.cwd)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%s isn't a folder", dir)
	}
	var s core.Session
	err = withClient(ctx, func(c *client, snap ipc.Snapshot) error {
		req := ipc.NewRequest{GroupID: o.groupID, Cwd: dir, Name: o.name, Description: o.description, Args: cfg.ClaudeArgs, Prompt: o.prompt, ShowIn: o.showIn}
		if o.group != "" {
			g, err := resolveGroup(snap, o.group)
			if err != nil && o.noCreate {
				return err
			}
			if err != nil {
				var created core.Group
				if err := c.mutate(ipc.Mutation{Kind: ipc.MutGroupCreate, Text: o.group}, &created); err != nil {
					return err
				}
				g = &created
			}
			req.GroupID = g.ID
		}
		return c.conn.Call(ipc.OpNew, req, &s)
	})
	return &s, err
}

// ---- acmd go / resume ----

func newGoCmd() *cobra.Command {
	var win int
	cmd := &cobra.Command{
		Use:   "go <N|id|name-prefix>",
		Short: "Show a session in the current (or most recently used) view; your own panes are focused",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				s, err := resolveSession(snap, args[0])
				if err != nil {
					return err
				}
				if s.Kind == core.KindExternal {
					return show(cmd.Context(), c, nil, s)
				}
				target, err := shortcutView(snap, win)
				if err != nil {
					return err
				}
				return show(cmd.Context(), c, target, s)
			})
		},
	}
	kittyWindowFlag(cmd, &win)
	return cmd
}

// targetView is the view a command acts on: the one it runs in, else the
// most recently focused one (SPEC §8.3).
func targetView(snap ipc.Snapshot, current string) (*core.View, error) {
	var best *core.View
	for _, v := range snap.Views {
		if v.ID == current {
			return v, nil
		}
		if best == nil || v.LastFocusedAt.After(best.LastFocusedAt) {
			best = v
		}
	}
	if best == nil {
		return nil, errors.New("no view is open: run `acmd` in a pane to open one")
	}
	return best, nil
}

func newResumeCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "resume <N|id|name-prefix> | --all",
		Short: "Resume paused background sessions (they keep running in the background)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if all == (len(args) == 1) || len(args) > 1 {
				return errors.New("give one session, or --all")
			}
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				var targets []*core.Session
				if all {
					for _, s := range snap.Sessions {
						if s.Status == core.StatusPaused && s.ClaudeSessionID != "" { // --all: not finished ones
							targets = append(targets, s)
						}
					}
				} else {
					s, err := resolveSession(snap, args[0])
					if err != nil {
						return err
					}
					targets = append(targets, s)
				}
				var errs []error
				for _, s := range targets {
					if !s.Stopped() {
						fmt.Fprintf(cmd.OutOrStdout(), "%s is already running.\n", nameOf(s))
						continue
					}
					var r core.Session
					if err := c.conn.Call(ipc.OpResume, ipc.Mutation{ID: s.ID}, &r); err != nil {
						errs = append(errs, fmt.Errorf("%s: %w", nameOf(s), err))
						continue
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Resumed %s (%s).\n", nameOf(&r), attachNote(&r))
				}
				if all && len(targets) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "No paused sessions.")
				}
				return errors.Join(errs...)
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "resume every paused session")
	return cmd
}

func nameOf(s *core.Session) string {
	if s.Name != "" {
		return fmt.Sprintf("%q", s.Name)
	}
	return "session " + s.Ref()
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// placeholderHints are the empty view's shortcut lines, with the user's keys.
func placeholderHints(p paths.Paths) []string {
	keys := config.Default().Keys
	if cfg, err := config.Load(p.Config()); err == nil {
		keys = cfg.Keys
	}
	var out []string
	add := func(key, what string) {
		if key != "" {
			out = append(out, fmt.Sprintf("%-6s %s", keymap.Pretty(key), what))
		}
	}
	add(keys.Focus, "focus the sidebar")
	add(keys.Picker, "find a session by name")
	if keys.GotoPrefix != "" {
		out = append(out, fmt.Sprintf("%-6s show the sidebar's session N (here: 1–9)", keymap.Pretty(keys.GotoPrefix)+"N"))
	}
	add(keys.Cheatsheet, "all shortcuts")
	return out
}

// attachNote says how a started or resumed session is shown, for the
// confirmation line.
func attachNote(s *core.Session) string {
	if s.Kind == core.KindHosted {
		return "hosted " + s.Ref()
	}
	return "claude attach " + s.BgID
}
