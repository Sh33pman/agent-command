package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
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

// focusExternal focuses an external session's own pane.
func focusExternal(ctx context.Context, loc core.PaneLoc) error { return focusPane(ctx, loc) }

func newSidebarCmd() *cobra.Command {
	var viewID, launchDir string
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
			call := func(op string, in, out any) error { return vc.call(ctx, op, in, out) }
			act := &sidebarActions{vc: vc, cmd: cmd, view: v,
				panes: &viewPanes{v: v, call: call, own: own, width: sidebarWidth()}}
			model := sidebar.New(ctx, act, viewID, homeDir())
			if launchDir == "" {
				launchDir, _ = os.Getwd()
			}
			model.SetLaunchDir(launchDir)
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
	cmd.Flags().StringVar(&launchDir, "launch-dir", "", "the folder acmd was run from (offered first for new sessions)")
	return cmd
}

// sidebarActions implements sidebar.Actions.
type sidebarActions struct {
	vc    *viewClient
	cmd   *cobra.Command
	view  *core.View
	panes *viewPanes
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
	if s.NeedsResume() {
		if _, err := a.vc.Resume(ctx, s.ID); err != nil {
			return err
		}
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
func (a *sidebarActions) ToggleTerm(ctx context.Context) error {
	snap, err := a.vc.Snapshot(ctx)
	if err != nil {
		return err
	}
	for _, v := range snap.Views {
		if v.ID == a.view.ID {
			call := func(op string, in, out any) error { return a.vc.call(ctx, op, in, out) }
			return toggleTerm(ctx, call, snap, v)
		}
	}
	return errors.New("this view is gone")
}
func (a *sidebarActions) FitWidth(ctx context.Context) error { return a.panes.FitWidth(ctx) }

func (a *sidebarActions) Open(ctx context.Context, s *core.Session) error {
	return openSession(ctx, s, a.view, openOptions{})
}

func (a *sidebarActions) Adopt(ctx context.Context, s *core.Session) error {
	call := func(op string, in, out any) error { return a.vc.call(ctx, op, in, out) }
	r, left, err := adoptSession(ctx, call, s, nil)
	if err != nil {
		return err
	}
	err = a.Show(ctx, r)
	closePane(ctx, left)
	return err
}

func (a *sidebarActions) SplitOut(ctx context.Context, s *core.Session, window, withSidebar bool) error {
	return splitOut(ctx, a.view, s, window, withSidebar)
}

func (a *sidebarActions) Terminal() string { return a.view.Pane.Terminal }

func (a *sidebarActions) ViewFocused(ctx context.Context) bool { return viewFocused(ctx, a.view) }

func (a *sidebarActions) Help(width int, groups bool) []string {
	bs := keymap.Filter(bindings(a.view.Pane.Terminal), "", "", groups)
	return keymap.Lines(bs, keymap.Ordered(keymap.Sidebar), width)
}

// newFromShortcut starts a session for the sidebar or a keybinding (no
// terminal of its own). Claude's trust prompt, for an untrusted folder,
// shows up in the session itself.
func newFromShortcut(ctx context.Context, cmd *cobra.Command, v *core.View, groupID, dir string) error {
	_, err := startSession(ctx, cmd, newOptions{groupID: groupID, cwd: dir, showIn: v.ID})
	return err
}

// ---- global shortcuts (SPEC §8.3) ----

// shortcutView is the view a shortcut acts on: the one whose pane (or
// sidebar) is Kitty window win, else the one this runs in, else the most
// recently focused one.
func shortcutView(snap ipc.Snapshot, win int) (*core.View, error) {
	if win > 0 {
		for _, v := range snap.Views {
			sb := v.Sidebar
			if v.Pane.KittyWindow == win || sb.KittyWindow == win || sb.HeaderWindow == win || sb.BarWindow == win || sb.TermWindow == win {
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
	call := func(op string, in, out any) error { return c.conn.Call(op, in, out) }
	alive, err := sidebarAlive(ctx, v)
	if err != nil {
		return err
	}
	if !alive {
		_, err := addSidebar(ctx, call, v, "")
		return err
	}
	_ = c.mutate(ipc.Mutation{Kind: ipc.MutViewTouch, ID: v.ID}, nil)
	return flipSidebar(ctx, call, v)
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
				hidden, err := sidebarHidden(cmd.Context(), v)
				if err != nil {
					return err
				}
				alive, err := sidebarAlive(cmd.Context(), v)
				if err != nil {
					return err
				}
				if hidden || !alive {
					if err := toggleSidebar(cmd.Context(), c, v); err != nil {
						return err
					}
					if snap, err = c.snapshot(); err != nil {
						return err
					}
					for _, x := range snap.Views {
						if x.ID == v.ID {
							v = x
						}
					}
				}
				_ = c.mutate(ipc.Mutation{Kind: ipc.MutViewTouch, ID: v.ID}, nil)
				return focusPane(cmd.Context(), v.Sidebar)
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
	if s.NeedsResume() {
		if err := c.conn.Call(ipc.OpResume, ipc.Mutation{ID: s.ID}, nil); err != nil {
			return err
		}
	}
	if err := c.mutate(ipc.Mutation{Kind: ipc.MutViewSelect, ID: v.ID, Text: s.ID}, nil); err != nil {
		return err
	}
	if v.Pane.Terminal == "kitty" || v.Pane.Terminal == "ghostty" {
		return focusPane(ctx, v.Pane)
	}
	return nil
}

func kittyWindowFlag(cmd *cobra.Command, win *int) {
	cmd.Flags().IntVar(win, "kitty-window", 0, "the Kitty window a shortcut came from (set by the keybinding)")
	_ = cmd.Flags().MarkHidden("kitty-window")
}
