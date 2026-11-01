package cli

import (
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

// adoptSession moves external session s into acmd: when its pane is idle
// (done or waiting) acmd types /bg there, and Claude continues the
// conversation as a background session, which the daemon links back to s.
// A session whose process ended is resumed in the background instead.
// The caller has confirmed with the user.
func adoptSession(ctx context.Context, call caller, s *core.Session) (*core.Session, error) {
	if s.Kind != core.KindExternal {
		return s, errors.New("that session is already in acmd")
	}
	if s.Status == core.StatusPaused || s.External == nil {
		var r core.Session
		if err := call(ipc.OpResume, ipc.Mutation{ID: s.ID}, &r); err != nil {
			return nil, err
		}
		return &r, nil
	}
	if s.Status != core.StatusDone && s.Status != core.StatusWaiting {
		return nil, errors.New("Claude is working in that pane: move it when it's done or waiting for you") //nolint:staticcheck // ST1005: product name
	}
	if err := call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutAdopt, ID: s.ID}, nil); err != nil {
		return nil, err
	}
	if err := typeInto(ctx, *s.External, "/bg"); err != nil {
		return nil, err
	}
	// The conversation continues under a new Claude id (M0 C4); wait for
	// the daemon to link it.
	deadline := time.Now().Add(adoptWait)
	for time.Now().Before(deadline) {
		var snap ipc.Snapshot
		if err := call(ipc.OpSnapshot, nil, &snap); err != nil {
			return nil, err
		}
		for _, x := range snap.Sessions {
			if x.ID == s.ID && x.Kind == core.KindBackground && x.BgID != "" {
				return x, nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil, errors.New("Claude didn't move the session to the background (is /bg available in your Claude Code?)") //nolint:staticcheck // ST1005: product name
}

// adoptWait is how long acmd waits for Claude's /bg (tests shorten it).
var adoptWait = 15 * time.Second

// typeInto types text + Enter into a pane (Kitty send-text, Ghostty input
// text + enter).
func typeInto(ctx context.Context, loc core.PaneLoc, text string) error {
	switch {
	case loc.Terminal == "kitty" && loc.KittySocket != "" && loc.KittyWindow != 0:
		return kittyAt(loc.KittySocket).SendText(ctx, loc.KittyWindow, text+"\r")
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
	cmd := &cobra.Command{
		Use:   "adopt <N|id|name-prefix>",
		Short: "Move a session from one of your own panes into acmd (it continues in the background)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(cmd.Context(), func(c *client, snap ipc.Snapshot) error {
				s, err := resolveSession(snap, args[0])
				if err != nil {
					return err
				}
				if s.Kind != core.KindExternal {
					return errors.New("that session is already in acmd")
				}
				q := fmt.Sprintf("Move %s into acmd? Claude continues it in the background and your pane goes back to the shell.", nameOf(s))
				if err := confirm(cmd.InOrStdin(), cmd.OutOrStdout(), yes, q); err != nil {
					return err
				}
				call := func(op string, in, out any) error { return c.conn.Call(op, in, out) }
				r, err := adoptSession(cmd.Context(), call, s)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Moved %s into acmd (claude attach %s).\n", nameOf(r), r.BgID)
				if v, err := targetView(snap, os.Getenv("ACMD_VIEW_ID")); err == nil {
					return show(cmd.Context(), c, v, r)
				}
				return nil
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}

// ---- split out (SPEC §6.6) ----

// splitOut opens s in a new tab or window next to view v: session only
// (`acmd view --pinned`) or a full view with its own sidebar.
func splitOut(ctx context.Context, v *core.View, s *core.Session, window, withSidebar bool) error {
	if s.Kind != core.KindBackground || s.BgID == "" {
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
