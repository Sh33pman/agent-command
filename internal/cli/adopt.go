package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ghostty"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// M5a: moving a session from your own pane into acmd (SPEC §6.3 step 5)
// and splitting a session out into its own tab or window (§6.6).

// caller is one daemon call (client or view client).
type caller func(op string, in, out any) error

// adoptSession moves external session s into acmd: once Claude is idle
// in its pane (done: not working and not waiting on a question, where
// typed text would be taken as the answer) acmd types /exit there, and
// when the session has ended resumes it in a host of its own
// (`claude --resume`, SPEC §4.5). A session whose process already ended is
// just resumed. The caller has confirmed with the user; waiting reports
// each status it waits on (nil: quiet). left is the pane Claude left
// (nil when it had already ended): the caller closes it with closePane
// once it's done with it.
func adoptSession(ctx context.Context, call caller, s *core.Session, waiting func(core.Status)) (r *core.Session, left *core.PaneLoc, err error) {
	if s.Kind != core.KindExternal {
		return s, nil, errors.New("that session is already in acmd")
	}
	s, err = waitIdle(ctx, call, s, waiting)
	if err != nil {
		return nil, nil, err
	}
	var pane *core.PaneLoc
	if !s.Stopped() && s.External != nil {
		p := *s.External
		if err := typeInto(ctx, p, "/exit"); err != nil {
			return nil, nil, err
		}
		pane = &p
		if err := waitEnded(ctx, call, s.ID); err != nil {
			return nil, nil, err
		}
	}
	var res core.Session
	if err := call(ipc.OpResume, ipc.Mutation{ID: s.ID}, &res); err != nil {
		return nil, nil, err
	}
	return &res, pane, nil
}

// waitEnded waits (up to adoptWait) until session id has ended.
func waitEnded(ctx context.Context, call caller, id string) error {
	deadline := time.Now().Add(adoptWait)
	for time.Now().Before(deadline) {
		var snap ipc.Snapshot
		if err := call(ipc.OpSnapshot, nil, &snap); err != nil {
			return err
		}
		for _, x := range snap.Sessions {
			if x.ID == id && x.Stopped() {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return errors.New("Claude didn't exit in that pane (did /exit get typed into something else?)") //nolint:staticcheck // ST1005: product name
}

// closePane closes the user's pane a moved session left: after /exit only
// a shell is left there, and the session now lives in acmd. Errors are
// ignored (the user may have closed it already).
func closePane(ctx context.Context, loc *core.PaneLoc) {
	if loc == nil {
		return
	}
	switch {
	case loc.Terminal == "kitty" && loc.KittySocket != "" && loc.KittyWindow != 0:
		_ = kittyAt(loc.KittySocket).Close(ctx, loc.KittyWindow)
	case loc.Terminal == "ghostty":
		term := loc.GhosttyTerm
		if term == "" && loc.TTY != "" {
			term, _ = ghosttyApp().TermForTTY(ctx, loc.TTY)
		}
		if term != "" {
			_ = ghosttyApp().Close(ctx, term)
		}
	}
}

// adoptIdle are the statuses in which /exit can be typed into the pane.
func adoptIdle(st core.Status) bool {
	return st == core.StatusDone || st == core.StatusError || st == core.StatusPaused
}

// waitIdle returns s once it's idle (adoptIdle), polling the daemon; it
// gives up when ctx ends, or s is archived or gone.
func waitIdle(ctx context.Context, call caller, s *core.Session, waiting func(core.Status)) (*core.Session, error) {
	last := core.Status("")
	for {
		if adoptIdle(s.Status) {
			return s, nil
		}
		if s.Status == core.StatusArchived {
			return nil, errors.New("that session was archived")
		}
		if waiting != nil && s.Status != last {
			waiting(s.Status)
		}
		last = s.Status
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(adoptPoll):
		}
		var snap ipc.Snapshot
		if err := call(ipc.OpSnapshot, nil, &snap); err != nil {
			return nil, err
		}
		var found *core.Session
		for _, x := range snap.Sessions {
			if x.ID == s.ID {
				found = x
			}
		}
		if found == nil {
			return nil, errors.New("that session no longer exists")
		}
		s = found
	}
}

// adoptPoll is how often acmd checks whether a session became idle.
var adoptPoll = 500 * time.Millisecond

// adoptWait is how long acmd waits for Claude to exit after /exit (tests shorten it).
var adoptWait = 15 * time.Second

// typeInto types text + Enter into a pane (Kitty send-text, Ghostty input
// text + enter).
// typeEnterDelay separates typed text from its Enter (tests shorten it).
var typeEnterDelay = 300 * time.Millisecond

func typeInto(ctx context.Context, loc core.PaneLoc, text string) error {
	switch {
	case loc.Terminal == "kitty" && loc.KittySocket != "" && loc.KittyWindow != 0:
		// Text and Enter in one burst read as a paste to Claude: the Enter
		// becomes a new line instead of sending. Press it on its own.
		k := kittyAt(loc.KittySocket)
		if err := k.SendText(ctx, loc.KittyWindow, text); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(typeEnterDelay):
		}
		return k.SendText(ctx, loc.KittyWindow, "\r")
	case loc.Terminal == "ghostty":
		term := loc.GhosttyTerm
		if term == "" && loc.TTY != "" {
			id, err := ghosttyApp().TermForTTY(ctx, loc.TTY)
			if err != nil {
				return err
			}
			term = id
		}
		if term != "" {
			return ghosttyApp().Type(ctx, term, text)
		}
	}
	return errors.New("don't know which pane that session is in; it can be moved once it has ended")
}

func newAdoptCmd() *cobra.Command {
	var yes bool
	var win int
	cmd := &cobra.Command{
		Use:   "adopt [N|id|name-prefix]",
		Short: "Move a session from one of your own panes into acmd (it continues, hosted by acmd)",
		Long: `Move a Claude session running in one of your own panes into acmd, where it
continues hosted by acmd. acmd asks first, then waits until Claude is idle
in that pane (not working, not waiting on a question), types /exit there and
resumes the conversation in acmd. From the shortcut (Kitty cmd+shift+m) it
moves the session in the focused pane.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			err := withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				var s *core.Session
				var err error
				switch {
				case len(args) == 1:
					s, err = resolveSession(snap, args[0])
				case win != 0:
					s, err = sessionInPane(snap, os.Getenv("KITTY_LISTEN_ON"), win)
				default:
					err = errors.New("which session? Pass its number, id or name (see `acmd list`)")
				}
				if err != nil {
					return err
				}
				if s.Kind != core.KindExternal {
					return errors.New("that session is already in acmd")
				}
				q := fmt.Sprintf("Move %s into acmd? It continues hosted by acmd and your pane closes.", nameOf(s))
				if err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(), yes, q); err != nil {
					return err
				}
				call := func(op string, in, out any) error { return c.conn.Call(op, in, out) }
				out := cmd.OutOrStdout()
				r, left, err := adoptSession(cmd.Context(), call, s, func(st core.Status) {
					fmt.Fprintf(out, "Waiting for Claude to finish in that pane (%s)… Ctrl+C cancels.\n", waitingWord(st))
				})
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "Moved %s into acmd (%s).\n", nameOf(r), attachNote(r))
				// Close the pane last: the shortcut's overlay runs in it.
				defer closePane(context.WithoutCancel(cmd.Context()), left)
				if v, err := targetView(snap, os.Getenv("ACMD_VIEW_ID")); err == nil {
					return show(cmd.Context(), c, v, r)
				}
				return nil
			})
			if err != nil && win != 0 {
				// From the shortcut's overlay: keep the message readable.
				fmt.Fprintf(cmd.ErrOrStderr(), "%v\nPress Enter to close.", err)
				_, _ = bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
			}
			return err
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	kittyWindowFlag(cmd, &win)
	return cmd
}

// waitingWord describes a status adopt waits on.
func waitingWord(st core.Status) string {
	switch st {
	case core.StatusWaiting:
		return "it's waiting for you: answer it first"
	case core.StatusStarting:
		return "starting"
	default:
		return "working"
	}
}

// sessionInPane is the session running in Kitty window win: the most
// recent non-archived external session there.
func sessionInPane(snap ipc.Snapshot, socket string, win int) (*core.Session, error) {
	var best *core.Session
	for _, s := range snap.Sessions {
		x := s.External
		if s.Kind != core.KindExternal || s.Status == core.StatusArchived || x == nil || x.Terminal != "kitty" || x.KittyWindow != win {
			continue
		}
		if socket != "" && x.KittySocket != "" && x.KittySocket != socket {
			continue
		}
		if best == nil || s.LastEventAt.After(best.LastEventAt) {
			best = s
		}
	}
	if best == nil {
		return nil, errors.New("no Claude session acmd knows of runs in this pane")
	}
	return best, nil
}

// ---- split out (SPEC §6.6) ----

// splitOut opens s in a new tab or window next to view v: session only
// (`acmd view --pinned`) or a full view with its own sidebar.
func splitOut(ctx context.Context, v *core.View, s *core.Session, window, withSidebar bool) error {
	if !s.Managed() {
		return errors.New("only sessions in acmd can be split out (move it into acmd first)")
	}
	argv := []string{acmdPath(), "view", "--pinned", s.ID}
	if withSidebar {
		argv = []string{acmdPath(), "view", "--start", s.ID}
	}
	dir := s.Cwd
	if dir == "" {
		dir = homeDir()
	}
	switch v.Pane.Terminal {
	case "kitty":
		k := kittyAt(v.Pane.KittySocket)
		if window {
			return k.LaunchOSWindow(ctx, v.Pane.KittyWindow, dir, argv, acmdEnv())
		}
		return k.LaunchTab(ctx, v.Pane.KittyWindow, dir, argv, acmdEnv())
	case "ghostty":
		g := ghosttyApp()
		if window {
			return g.NewWindow(ctx, ghostty.Quote(argv), dir, acmdEnv())
		}
		return g.NewTab(ctx, v.Pane.GhosttyTerm, ghostty.Quote(argv), dir, acmdEnv())
	}
	return errNoTerminal
}

func newSplitOutCmd() *cobra.Command {
	var tab, window, plain, withSidebar bool
	cmd := &cobra.Command{
		Use:   "split-out <N|id|name-prefix> [--tab|--window] [--plain|--sidebar]",
		Short: "Open a session in its own tab or window (closing it leaves the session running)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if tab && window || plain && withSidebar {
				return errors.New("choose one of --tab/--window and one of --plain/--sidebar")
			}
			return withClient(cmd.Context(), func(_ *client, snap ipc.Snapshot) error {
				s, err := resolveSession(snap, args[0])
				if err != nil {
					return err
				}
				v, err := targetView(snap, os.Getenv("ACMD_VIEW_ID"))
				if err != nil {
					return err
				}
				if !tab && !window {
					tab = true // the default without a choice
				}
				return splitOut(cmd.Context(), v, s, window, withSidebar)
			})
		},
	}
	f := cmd.Flags()
	f.BoolVar(&tab, "tab", false, "a new tab (default)")
	f.BoolVar(&window, "window", false, "a new window")
	f.BoolVar(&plain, "plain", false, "just the session (default)")
	f.BoolVar(&withSidebar, "sidebar", false, "a full view with its own sidebar")
	return cmd
}
