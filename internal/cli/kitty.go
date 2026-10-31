package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/keymap"
	"github.com/brettmostert/agent-command/internal/kitty"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/setup"
	"github.com/brettmostert/agent-command/internal/sidebar"
)

// Kitty integration (SPEC §7.2, M2b): the view's sidebar split and the
// global shortcuts.

// kittyAt returns the adapter for a Kitty socket; swappable in tests.
var kittyAt = func(socket string) *kitty.Kitty {
	return kitty.New(kitty.FindKitten(func(n string) (string, error) {
		if p, err := setup.LookPathIn(n, loginPath()); err == nil {
			return p, nil
		}
		return exec.LookPath(n)
	}), socket)
}

func acmdPath() string {
	exe, _ := executable()
	return setup.StableBinary(invokedAs, exe, exec.LookPath)
}

func sidebarWidth() int {
	if p, err := paths.Default(); err == nil {
		if cfg, err := config.Load(p.Config()); err == nil && cfg.SidebarWidth >= 16 {
			return cfg.SidebarWidth
		}
	}
	return config.Default().SidebarWidth
}

// addSidebar adds the view's sidebar split (left of the view pane) and
// records it with the daemon.
func addSidebar(ctx context.Context, call func(op string, in, out any) error, v *core.View) (core.PaneLoc, error) {
	k := kittyAt(v.Pane.KittySocket)
	var env []string
	for _, kv := range os.Environ() { // test and dev overrides follow the view
		if strings.HasPrefix(kv, "ACMD_") && !strings.HasPrefix(kv, "ACMD_VIEW_ID=") {
			env = append(env, kv)
		}
	}
	id, prev, err := k.AddSidebarLeft(ctx, kitty.SidebarSpec{View: v.Pane.KittyWindow, ViewID: v.ID, Width: sidebarWidth(),
		Command: []string{acmdPath(), "sidebar", "--view", v.ID}, Env: env})
	if err != nil {
		return core.PaneLoc{}, err
	}
	if old := v.Sidebar.RestoreLayout; old != "" {
		prev = old // re-added: the tab's layout from before acmd's first sidebar
	}
	loc := core.PaneLoc{Terminal: "kitty", KittySocket: v.Pane.KittySocket, KittyWindow: id, RestoreLayout: prev}
	return loc, call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutViewSidebar, ID: v.ID, Pane: &loc}, nil)
}

// removeSidebar closes a view's sidebar and gives its tab back its layout.
func removeSidebar(ctx context.Context, v *core.View) {
	if v.Sidebar.KittyWindow == 0 {
		return
	}
	k := kittyAt(v.Sidebar.KittySocket)
	// Through the sidebar's window: the view pane may be closed already.
	if l := v.Sidebar.RestoreLayout; l != "" && l != "splits" {
		_ = k.SetLayout(ctx, v.Sidebar.KittyWindow, l)
	}
	_ = k.Close(ctx, v.Sidebar.KittyWindow)
}

// kittyPanes implements view.Panes for a sidebar in Kitty.
type kittyPanes struct {
	k             *kitty.Kitty
	view, sidebar int
	width         int
}

func (p *kittyPanes) FocusView(ctx context.Context) error { return p.k.Focus(ctx, p.view) }
func (p *kittyPanes) ToggleSidebar(ctx context.Context) error {
	return p.k.ToggleSidebar(ctx, p.view)
}
func (p *kittyPanes) FitWidth(ctx context.Context) error {
	return p.k.SetWidth(ctx, p.sidebar, p.width)
}
func (p *kittyPanes) FocusPane(ctx context.Context, loc core.PaneLoc) error {
	return focusExternal(ctx, loc)
}

func focusExternal(ctx context.Context, loc core.PaneLoc) error {
	if loc.Terminal != "kitty" || loc.KittySocket == "" || loc.KittyWindow == 0 {
		return errors.New("can't focus that session's pane (not a Kitty pane acmd knows)")
	}
	return kittyAt(loc.KittySocket).Focus(ctx, loc.KittyWindow)
}

func newSidebarCmd() *cobra.Command {
	var viewID string
	cmd := &cobra.Command{
		Use:    "sidebar --view <id>",
		Short:  "Run a view's sidebar (started by `acmd`)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGHUP, syscall.SIGTERM)
			defer stop()
			vc := &viewClient{paths: p}
			defer vc.close()
			var snap ipc.Snapshot
			if err := vc.call(ctx, ipc.OpSnapshot, nil, &snap); err != nil {
				return err
			}
			var v *core.View
			for _, x := range snap.Views {
				if x.ID == viewID {
					v = x
				}
			}
			if v == nil {
				return fmt.Errorf("no view %s", viewID)
			}
			own := 0
			_, _ = fmt.Sscan(os.Getenv("KITTY_WINDOW_ID"), &own)
			act := &sidebarActions{vc: vc, cmd: cmd, view: v,
				panes: &kittyPanes{k: kittyAt(v.Pane.KittySocket), view: v.Pane.KittyWindow, sidebar: own, width: sidebarWidth()}}
			model := sidebar.New(ctx, act, viewID, homeDir())
			if _, err := tea.NewProgram(model, tea.WithContext(ctx)).Run(); err != nil && ctx.Err() == nil && model.Err() == nil {
				return err
			}
			switch err := model.Err(); {
			case err == nil, errors.Is(err, sidebar.ErrClosed), ctx.Err() != nil:
				return nil
			case errors.Is(err, errNewerDaemon):
				return reexec("sidebar", "--view", viewID) // keeps the pane
			default:
				return err
			}
		},
	}
	cmd.Flags().StringVar(&viewID, "view", "", "the view this sidebar belongs to")
	return cmd
}

// sidebarActions implements sidebar.Actions.
type sidebarActions struct {
	vc    *viewClient
	cmd   *cobra.Command
	view  *core.View
	panes *kittyPanes
}

func (a *sidebarActions) Watch(ctx context.Context, rev uint64, wait bool) (ipc.WatchResponse, error) {
	req := ipc.WatchRequest{ViewID: a.view.ID, Rev: rev, Passive: true}
	if wait {
		req.WaitMS = 25000
	}
	return a.vc.Watch(ctx, req)
}

func (a *sidebarActions) Snapshot(ctx context.Context) (ipc.Snapshot, error) {
	return a.vc.Snapshot(ctx)
}

func (a *sidebarActions) Mutate(ctx context.Context, m ipc.Mutation, out any) error {
	return a.vc.call(ctx, ipc.OpMutate, m, out)
}

func (a *sidebarActions) Show(ctx context.Context, s *core.Session) error {
	if s.Kind == core.KindExternal {
		if s.External == nil {
			return errors.New("don't know which pane that session is in")
		}
		return focusExternal(ctx, *s.External)
	}
	if err := a.vc.Select(ctx, a.view.ID, s.ID); err != nil {
		return err
	}
	return a.panes.FocusView(ctx)
}

func (a *sidebarActions) New(ctx context.Context, groupID, dir string) error {
	return newFromShortcut(ctx, a.cmd, a.view, groupID, dir)
}

func (a *sidebarActions) FocusView(ctx context.Context) error     { return a.panes.FocusView(ctx) }
func (a *sidebarActions) ToggleSidebar(ctx context.Context) error { return a.panes.ToggleSidebar(ctx) }
func (a *sidebarActions) FitWidth(ctx context.Context) error      { return a.panes.FitWidth(ctx) }

func (a *sidebarActions) Open(ctx context.Context, s *core.Session) error {
	return openSession(ctx, s, a.view, openOptions{})
}

func (a *sidebarActions) Help(width int, groups bool) []string {
	bs := keymap.Filter(bindings(a.view.Pane.Terminal), "", "", groups)
	return keymap.Lines(bs, keymap.Ordered(keymap.Sidebar), width)
}

// newFromShortcut starts a session for the sidebar or a keybinding (no
// terminal of its own). An untrusted folder opens Claude's trust prompt
// in an overlay over the view pane, which then starts the session.
func newFromShortcut(ctx context.Context, cmd *cobra.Command, v *core.View, groupID, dir string) error {
	_, err := startSession(ctx, cmd, newOptions{groupID: groupID, cwd: dir, showIn: v.ID})
	var untrusted *errUntrusted
	if !errors.As(err, &untrusted) || v.Pane.Terminal != "kitty" {
		return err
	}
	argv := []string{acmdPath(), "new", "--cwd", dir, "--view", v.ID}
	if groupID != "" {
		argv = append(argv, "--group-id", groupID)
	}
	return kittyAt(v.Pane.KittySocket).Overlay(ctx, v.Pane.KittyWindow, argv, nil)
}

// ---- global shortcuts (SPEC §8.3) ----

// shortcutView is the view a shortcut acts on: the one whose pane (or
// sidebar) is Kitty window win, else the one this runs in, else the most
// recently focused one.
func shortcutView(snap ipc.Snapshot, win int) (*core.View, error) {
	if win > 0 {
		for _, v := range snap.Views {
			if v.Pane.KittyWindow == win || v.Sidebar.KittyWindow == win {
				return v, nil
			}
		}
	}
	return targetView(snap, os.Getenv("ACMD_VIEW_ID"))
}

func newToggleCmd() *cobra.Command {
	var all bool
	var win int
	cmd := &cobra.Command{
		Use:   "toggle [--all]",
		Short: "Hide or show the sidebar of the current view (or of every view)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				views := snap.Views
				if !all {
					v, err := shortcutView(snap, win)
					if err != nil {
						return err
					}
					views = []*core.View{v}
				}
				var errs []error
				for _, v := range views {
					errs = append(errs, toggleSidebar(cmd.Context(), c, v))
				}
				return errors.Join(errs...)
			})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "every view")
	kittyWindowFlag(cmd, &win)
	return cmd
}

// toggleSidebar hides/shows v's sidebar, adding it again if it was closed.
func toggleSidebar(ctx context.Context, c *client, v *core.View) error {
	if v.Pane.Terminal != "kitty" {
		return errors.New("hiding the sidebar works in Kitty views only (for now)")
	}
	k := kittyAt(v.Pane.KittySocket)
	if v.Sidebar.KittyWindow != 0 {
		ws, err := k.LS(ctx)
		if err != nil {
			return err
		}
		if _, _, ok := kitty.Find(ws, v.Sidebar.KittyWindow); ok {
			_ = c.mutate(ipc.Mutation{Kind: ipc.MutViewTouch, ID: v.ID}, nil)
			return k.ToggleSidebar(ctx, v.Pane.KittyWindow)
		}
	}
	_, err := addSidebar(ctx, func(op string, in, out any) error { return c.conn.Call(op, in, out) }, v)
	return err
}

func newFocusCmd() *cobra.Command {
	var win int
	cmd := &cobra.Command{
		Use:   "focus",
		Short: "Focus the current view's sidebar",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				v, err := shortcutView(snap, win)
				if err != nil {
					return err
				}
				if v.Pane.Terminal != "kitty" {
					return errors.New("focusing the sidebar works in Kitty views only (for now)")
				}
				k := kittyAt(v.Pane.KittySocket)
				hidden, err := k.Hidden(cmd.Context(), v.Pane.KittyWindow)
				if err != nil {
					return err
				}
				if hidden || v.Sidebar.KittyWindow == 0 {
					if err := toggleSidebar(cmd.Context(), c, v); err != nil {
						return err
					}
					if snap, err = c.snapshot(); err != nil {
						return err
					}
					if v, err = shortcutView(snap, v.Pane.KittyWindow); err != nil {
						return err
					}
				}
				_ = c.mutate(ipc.Mutation{Kind: ipc.MutViewTouch, ID: v.ID}, nil)
				return k.Focus(cmd.Context(), v.Sidebar.KittyWindow)
			})
		},
	}
	kittyWindowFlag(cmd, &win)
	return cmd
}

func newNextAttentionCmd() *cobra.Command {
	var win int
	cmd := &cobra.Command{
		Use:   "next-attention",
		Short: "Show the next session that needs you (waiting, then error, then unseen done)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				v, err := shortcutView(snap, win)
				if err != nil {
					return err
				}
				s := nextAttention(snap, v.Selected)
				if s == nil {
					return errors.New("no session needs you")
				}
				return show(cmd.Context(), c, v, s)
			})
		},
	}
	kittyWindowFlag(cmd, &win)
	return cmd
}

// nextAttention is core.NextAttention over a snapshot.
func nextAttention(snap ipc.Snapshot, shown string) *core.Session {
	return core.NextAttention(snap.Sessions, snap.Groups, snap.ViewState, shown)
}

// show makes view v show s (external sessions: focus their own pane).
func show(ctx context.Context, c *client, v *core.View, s *core.Session) error {
	if s.Kind == core.KindExternal {
		if s.External == nil {
			return errors.New("don't know which pane that session is in")
		}
		return focusExternal(ctx, *s.External)
	}
	if err := c.mutate(ipc.Mutation{Kind: ipc.MutViewSelect, ID: v.ID, Text: s.ID}, nil); err != nil {
		return err
	}
	if v.Pane.Terminal == "kitty" && v.Pane.KittyWindow != 0 {
		return kittyAt(v.Pane.KittySocket).Focus(ctx, v.Pane.KittyWindow)
	}
	return nil
}

func kittyWindowFlag(cmd *cobra.Command, win *int) {
	cmd.Flags().IntVar(win, "kitty-window", 0, "the Kitty window a shortcut came from (set by the keybinding)")
	_ = cmd.Flags().MarkHidden("kitty-window")
}
