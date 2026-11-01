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
	"golang.org/x/sys/unix"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ghostty"
	"github.com/brettmostert/agent-command/internal/header"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/kitty"
	"github.com/brettmostert/agent-command/internal/paths"
)

// The header bar (SPEC §8.4): one line above the view pane only, showing
// the shown session's status, name, description and folder. Added with
// the sidebar.

// addHeader adds the header above v's view pane and records it in loc.
// Errors are not fatal: the view works without a header.
func addHeader(ctx context.Context, v *core.View, loc *core.PaneLoc) error {
	argv := []string{acmdPath(), "header", "--view", v.ID}
	switch v.Pane.Terminal {
	case "kitty":
		id, err := kittyAt(v.Pane.KittySocket).AddPaneAbove(ctx, kitty.BarSpec{View: v.Pane.KittyWindow, ViewID: v.ID,
			Lines: 1, Command: argv, Env: acmdEnv()}, "header")
		if err != nil {
			return err
		}
		loc.HeaderWindow = id
	case "ghostty":
		id, err := ghosttyApp().Split(ctx, v.Pane.GhosttyTerm, "up", ghostty.Quote(argv), homeDir(), acmdEnv())
		if err != nil {
			return err
		}
		loc.HeaderTerm = id
	}
	return nil
}

// headerActions implements header.Actions.
type headerActions struct {
	vc   *viewClient
	view *core.View
	own  int // the header's own Kitty window (0 in Ghostty)
	fit  fitGate

	// Ghostty height loop (resize_split is in pixels): the header's own
	// terminal, pixels per row (calibrated), the last resize, tries.
	term     string
	pxRow    float64
	lastRows int
	lastPx   int
	tries    int
}

func (a *headerActions) Watch(ctx context.Context, rev uint64, wait bool) (ipc.WatchResponse, error) {
	req := ipc.WatchRequest{ViewID: a.view.ID, Rev: rev, Passive: true}
	if wait {
		req.WaitMS = 25000
	}
	return a.vc.Watch(ctx, req)
}

func (a *headerActions) Snapshot(ctx context.Context) (ipc.Snapshot, error) {
	return a.vc.Snapshot(ctx)
}

func (a *headerActions) FitHeight(ctx context.Context, lines int) error {
	switch {
	case a.own != 0:
		return a.fit.Run(ctx, func(ctx context.Context) error {
			return kittyAt(a.view.Pane.KittySocket).SetHeight(ctx, a.own, lines)
		})
	case a.view.Pane.Terminal == "ghostty":
		return a.fit.Run(ctx, func(ctx context.Context) error { return a.fitGhostty(ctx, lines) })
	}
	return nil
}

// fitGhostty moves the divider under the header until it is lines tall:
// resize, read the new height, calibrate pixels per row (G5), at most 4
// tries.
func (a *headerActions) fitGhostty(ctx context.Context, lines int) error {
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return nil //nolint:nilerr // no terminal: nothing to fit
	}
	rows := int(ws.Row)
	if a.lastPx != 0 && a.lastRows != 0 && rows != a.lastRows {
		a.pxRow = float64(a.lastPx) / float64(abs(rows-a.lastRows))
	}
	if rows == lines || a.tries >= 4 {
		a.lastPx = 0
		return nil
	}
	if a.pxRow == 0 {
		a.pxRow = 17 // ~17 px per row at a typical font size
	}
	if a.term == "" {
		if a.term, err = ghosttyApp().TermForTTY(ctx, ttyName()); err != nil {
			return err
		}
	}
	dir, d := "up", rows-lines // the header is the top pane: up shrinks it
	if d < 0 {
		dir, d = "down", -d
	}
	a.tries++
	a.lastRows, a.lastPx = rows, int(float64(d)*a.pxRow+0.5)
	return ghosttyApp().Action(ctx, a.term, fmt.Sprintf("resize_split:%s,%d", dir, a.lastPx))
}

func (a *headerActions) FocusView(ctx context.Context) error { return focusPane(ctx, a.view.Pane) }

func newHeaderCmd() *cobra.Command {
	var viewID string
	cmd := &cobra.Command{
		Use:    "header --view <id>",
		Short:  "Run a view's header bar (started by `acmd`)",
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
			model := header.New(ctx, &headerActions{vc: vc, view: v, own: own}, viewID, homeDir())
			if _, err := tea.NewProgram(model, tea.WithContext(ctx)).Run(); err != nil && ctx.Err() == nil && model.Err() == nil {
				return err
			}
			switch err := model.Err(); {
			case err == nil, errors.Is(err, header.ErrClosed), ctx.Err() != nil:
				return nil
			case errors.Is(err, errNewerDaemon):
				return reexec("header", "--view", viewID) // keeps the pane
			default:
				return err
			}
		},
	}
	cmd.Flags().StringVar(&viewID, "view", "", "the view this header belongs to")
	return cmd
}
