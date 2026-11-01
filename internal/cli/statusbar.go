package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/kitty"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/statusbar"
	"github.com/brettmostert/agent-command/internal/version"
)

// The full-width status bar (SPEC §8.4): a thin Kitty pane along the
// bottom of a view's tab, added with the sidebar.

// addBar adds the bar under the view's sidebar and view pane and returns
// its window id. Errors are not fatal: without a bar the sidebar shows
// messages itself.
func addBar(ctx context.Context, v *core.View) (int, error) {
	if v.Pane.Terminal != "kitty" {
		return 0, nil // Ghostty can't split under an existing split
	}
	return kittyAt(v.Pane.KittySocket).AddBottomBar(ctx, kitty.BarSpec{View: v.Pane.KittyWindow, ViewID: v.ID,
		Lines: 1, Command: []string{acmdPath(), "statusbar", "--view", v.ID}, Env: acmdEnv()})
}

// barActions implements statusbar.Actions.
type barActions struct {
	vc   *viewClient
	view *core.View
	own  int // the bar's own Kitty window
	fit  fitGate
}

func (a *barActions) Watch(ctx context.Context, rev uint64, wait bool) (ipc.WatchResponse, error) {
	req := ipc.WatchRequest{ViewID: a.view.ID, Rev: rev, Passive: true}
	if wait {
		req.WaitMS = 25000
	}
	return a.vc.Watch(ctx, req)
}

func (a *barActions) Snapshot(ctx context.Context) (ipc.Snapshot, error) { return a.vc.Snapshot(ctx) }

func (a *barActions) Mutate(ctx context.Context, m ipc.Mutation, out any) error {
	return a.vc.call(ctx, ipc.OpMutate, m, out)
}

func (a *barActions) FitHeight(ctx context.Context, lines int) error {
	if a.own == 0 {
		return nil
	}
	return a.fit.Run(ctx, func(ctx context.Context) error {
		return kittyAt(a.view.Pane.KittySocket).SetHeight(ctx, a.own, lines)
	})
}

func (a *barActions) FocusView(ctx context.Context) error { return focusPane(ctx, a.view.Pane) }

func newStatusbarCmd() *cobra.Command {
	var viewID string
	cmd := &cobra.Command{
		Use:    "statusbar --view <id>",
		Short:  "Run a view's full-width status bar (started by `acmd`)",
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
			model := statusbar.New(ctx, &barActions{vc: vc, view: v, own: own}, viewID)
			model.Version = version.Get().Version
			if _, err := tea.NewProgram(model, tea.WithContext(ctx)).Run(); err != nil && ctx.Err() == nil && model.Err() == nil {
				return err
			}
			switch err := model.Err(); {
			case err == nil, errors.Is(err, statusbar.ErrClosed), ctx.Err() != nil:
				return nil
			case errors.Is(err, errNewerDaemon):
				return reexec("statusbar", "--view", viewID) // keeps the pane
			default:
				return err
			}
		},
	}
	cmd.Flags().StringVar(&viewID, "view", "", "the view this bar belongs to")
	return cmd
}
