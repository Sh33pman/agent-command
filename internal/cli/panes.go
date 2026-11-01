package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ghostty"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/kitty"
	"github.com/brettmostert/agent-command/internal/setup"
)

// Terminal dispatch (SPEC §7.1): the few pane operations acmd needs, for
// Kitty (kitten @) and Ghostty (AppleScript).

var errNoTerminal = errors.New("this needs a Kitty or Ghostty view")

// focusPane focuses a pane (a view, a sidebar, an external session's).
func focusPane(ctx context.Context, loc core.PaneLoc) error {
	switch {
	case loc.Terminal == "kitty" && loc.KittySocket != "" && loc.KittyWindow != 0:
		return kittyAt(loc.KittySocket).Focus(ctx, loc.KittyWindow)
	case loc.Terminal == "ghostty" && loc.GhosttyTerm != "":
		return ghosttyApp().Focus(ctx, loc.GhosttyTerm)
	case loc.Terminal == "ghostty" && loc.TTY != "":
		// An external session: its terminal is found by tty (G1).
		id, err := ghosttyApp().TermForTTY(ctx, loc.TTY)
		if err != nil {
			return err
		}
		return ghosttyApp().Focus(ctx, id)
	}
	return errors.New("can't focus that pane (not a Kitty or Ghostty pane acmd knows)")
}

// promptIn runs argv where the user sees it, for a view that has no
// terminal of its own to ask in: a Kitty overlay over the view pane, or a
// Ghostty tab (Ghostty has no overlays).
func promptIn(ctx context.Context, v *core.View, argv []string) error {
	switch v.Pane.Terminal {
	case "kitty":
		return kittyAt(v.Pane.KittySocket).Overlay(ctx, v.Pane.KittyWindow, argv, nil)
	case "ghostty":
		return ghosttyApp().NewTab(ctx, v.Pane.GhosttyTerm, ghostty.Quote(argv), homeDir(), acmdEnv())
	}
	return errNoTerminal
}

// tabIn runs argv in a new tab next to the view (terminal editors).
func tabIn(ctx context.Context, v *core.View, dir string, argv []string) error {
	switch v.Pane.Terminal {
	case "kitty":
		return kittyAt(v.Pane.KittySocket).LaunchTab(ctx, v.Pane.KittyWindow, dir, argv, nil)
	case "ghostty":
		return ghosttyApp().NewTab(ctx, v.Pane.GhosttyTerm, ghostty.Quote(argv), dir, nil)
	}
	return errNoTerminal
}

// acmdEnv is the ACMD_* overrides (dev and tests) for processes acmd
// starts in panes.
func acmdEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "ACMD_") && !strings.HasPrefix(kv, "ACMD_VIEW_ID=") {
			env = append(env, kv)
		}
	}
	return env
}

// addSidebar adds the view's sidebar split on the left of the view pane
// and records it with the daemon.
func addSidebar(ctx context.Context, call func(op string, in, out any) error, v *core.View) (core.PaneLoc, error) {
	argv := []string{acmdPath(), "sidebar", "--view", v.ID}
	var loc core.PaneLoc
	switch v.Pane.Terminal {
	case "kitty":
		id, prev, err := kittyAt(v.Pane.KittySocket).AddSidebarLeft(ctx, kitty.SidebarSpec{View: v.Pane.KittyWindow, ViewID: v.ID,
			Width: sidebarWidth(), Command: argv, Env: acmdEnv()})
		if err != nil {
			return core.PaneLoc{}, err
		}
		if old := v.Sidebar.RestoreLayout; old != "" {
			prev = old // re-added: the tab's layout from before acmd's first sidebar
		}
		loc = core.PaneLoc{Terminal: "kitty", KittySocket: v.Pane.KittySocket, KittyWindow: id, RestoreLayout: prev}
	case "ghostty":
		id, err := ghosttyApp().AddSidebarLeft(ctx, v.Pane.GhosttyTerm, ghostty.Quote(argv), homeDir(), acmdEnv())
		if err != nil {
			return core.PaneLoc{}, err
		}
		loc = core.PaneLoc{Terminal: "ghostty", GhosttyTerm: id}
	default:
		return core.PaneLoc{}, errNoTerminal
	}
	return loc, call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutViewSidebar, ID: v.ID, Pane: &loc}, nil)
}

// removeSidebar closes a view's sidebar (Kitty: and gives the tab its
// layout back, through the sidebar's window: the view pane may be gone).
func removeSidebar(ctx context.Context, v *core.View) {
	switch v.Sidebar.Terminal {
	case "kitty":
		if v.Sidebar.KittyWindow == 0 {
			return
		}
		k := kittyAt(v.Sidebar.KittySocket)
		if l := v.Sidebar.RestoreLayout; l != "" && l != "splits" {
			_ = k.SetLayout(ctx, v.Sidebar.KittyWindow, l)
		}
		_ = k.Close(ctx, v.Sidebar.KittyWindow)
	case "ghostty":
		if v.Sidebar.GhosttyTerm != "" {
			_ = ghosttyApp().Close(ctx, v.Sidebar.GhosttyTerm)
		}
	}
}

// sidebarAlive reports whether the view's sidebar pane still exists.
func sidebarAlive(ctx context.Context, v *core.View) (bool, error) {
	switch v.Sidebar.Terminal {
	case "kitty":
		if v.Sidebar.KittyWindow == 0 {
			return false, nil
		}
		ws, err := kittyAt(v.Pane.KittySocket).LS(ctx)
		if err != nil {
			return false, err
		}
		_, _, ok := kitty.Find(ws, v.Sidebar.KittyWindow)
		return ok, nil
	case "ghostty":
		return ghosttyApp().Exists(ctx, v.Sidebar.GhosttyTerm)
	}
	return false, nil
}

// sidebarHidden reports whether the view's sidebar is hidden. Kitty can
// tell (stack layout); Ghostty can't report zoom (G7), so the daemon's
// record is used.
func sidebarHidden(ctx context.Context, v *core.View) (bool, error) {
	if v.Pane.Terminal == "kitty" {
		return kittyAt(v.Pane.KittySocket).Hidden(ctx, v.Pane.KittyWindow)
	}
	return v.SidebarHidden, nil
}

// flipSidebar hides/shows an existing sidebar.
func flipSidebar(ctx context.Context, call func(op string, in, out any) error, v *core.View) error {
	switch v.Pane.Terminal {
	case "kitty":
		return kittyAt(v.Pane.KittySocket).ToggleSidebar(ctx, v.Pane.KittyWindow)
	case "ghostty":
		if err := ghosttyApp().Action(ctx, v.Pane.GhosttyTerm, "toggle_split_zoom"); err != nil {
			return err
		}
		return call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutViewHidden, ID: v.ID, Flag: !v.SidebarHidden}, nil)
	}
	return errNoTerminal
}

// ttyName is this process's terminal (/dev/ttysNNN), for finding its
// Ghostty pane (G1: Ghostty reports full paths).
var ttyName = func() string {
	out, err := exec.Command("/bin/ps", "-o", "tty=", "-p", fmt.Sprint(os.Getpid())).Output() // #nosec G204 -- fixed binary and args
	t := strings.TrimSpace(string(out))
	if err != nil || t == "" || t == "??" {
		return ""
	}
	return "/dev/" + t
}

// viewPanes implements view.Panes for a sidebar process.
type viewPanes struct {
	v     *core.View
	call  func(op string, in, out any) error
	own   int // Kitty: the sidebar's own window id
	width int
	fit   fitGate // one fit at a time (also guards the Ghostty fields)

	// Ghostty width loop (G5: resize_split is in pixels): pixels per
	// column, calibrated from the last resize.
	px       float64
	lastCols int
	lastPx   int
	tries    int
	since    time.Time
}

func (p *viewPanes) FocusView(ctx context.Context) error { return focusPane(ctx, p.v.Pane) }

func (p *viewPanes) ToggleSidebar(ctx context.Context) error { return flipSidebar(ctx, p.call, p.v) }

func (p *viewPanes) FocusPane(ctx context.Context, loc core.PaneLoc) error {
	return focusPane(ctx, loc)
}

func (p *viewPanes) FitWidth(ctx context.Context) error { return p.fit.Run(ctx, p.fitWidth) }

func (p *viewPanes) fitWidth(ctx context.Context) error {
	switch p.v.Pane.Terminal {
	case "kitty":
		return kittyAt(p.v.Pane.KittySocket).SetWidth(ctx, p.own, p.width)
	case "ghostty":
		ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
		if err != nil {
			return nil //nolint:nilerr // no terminal: nothing to fit
		}
		cols := int(ws.Col)
		if p.lastPx != 0 && p.lastCols != 0 && cols != p.lastCols {
			p.px = float64(p.lastPx) / float64(abs(cols-p.lastCols))
		}
		if time.Since(p.since) > 5*time.Second {
			p.tries, p.since = 0, time.Now()
		}
		if cols == p.width || p.tries >= 3 {
			p.lastPx = 0
			return nil
		}
		if p.px == 0 {
			p.px = 8 // ~7.7 px per column at a typical font size (G5)
		}
		p.tries++
		action := ghostty.ResizeAction(p.width-cols, p.px)
		p.lastCols = cols
		_, _ = fmt.Sscanf(action[strings.Index(action, ",")+1:], "%d", &p.lastPx)
		return ghosttyApp().Action(ctx, p.v.Sidebar.GhosttyTerm, action)
	}
	return nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// viewFocused reports whether v's pane has focus in the frontmost
// terminal window.
func viewFocused(ctx context.Context, v *core.View) bool {
	switch v.Pane.Terminal {
	case "kitty":
		ws, err := kittyAt(v.Pane.KittySocket).LS(ctx)
		if err != nil {
			return false
		}
		for _, ow := range ws {
			for _, t := range ow.Tabs {
				for _, w := range t.Windows {
					if w.ID == v.Pane.KittyWindow {
						return ow.IsFocused && w.IsFocused
					}
				}
			}
		}
	case "ghostty":
		id, err := ghosttyApp().FocusedTerm(ctx)
		return err == nil && id != "" && id == v.Pane.GhosttyTerm
	}
	return false
}

// fallbackNotify shows a notification without a sidebar (SPEC §8.5):
// terminal-notifier (a click runs `acmd go`), else osascript.
func fallbackNotify(no ipc.Notice) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if tn, err := setup.LookPathIn("terminal-notifier", loginPath()); err == nil {
		args := []string{"-title", no.Title, "-message", no.Body, "-group", no.SessionID,
			"-execute", ghostty.Quote([]string{acmdPath(), "go", no.SessionID})}
		if no.Sound != "" {
			args = append(args, "-sound", no.Sound)
		}
		_ = exec.CommandContext(ctx, tn, args...).Run() // #nosec G204 -- terminal-notifier on the login PATH; argv without a shell
		return
	}
	script := `on run argv
	if item 3 of argv is "" then
		display notification (item 2 of argv) with title (item 1 of argv)
	else
		display notification (item 2 of argv) with title (item 1 of argv) sound name (item 3 of argv)
	end if
end run`
	sound := no.Sound
	if sound == "default" {
		sound = "Glass"
	}
	_, _ = ghosttyApp().Run(ctx, script, no.Title, no.Body, sound)
}
