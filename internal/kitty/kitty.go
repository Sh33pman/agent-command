// Package kitty is acmd's Kitty adapter (SPEC §7.2): it adds a view's
// sidebar split, keeps its width, hides/shows it and focuses panes, all
// through `kitten @` on the instance's remote-control socket. It only ever
// changes acmd's own panes, plus the layout of the tab a view lives in
// (decision 26: switched to splits, restored when the view closes).
package kitty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Runner runs `kitten @ --to <socket> <args…>` and returns stdout.
type Runner func(ctx context.Context, kitten, to string, args ...string) ([]byte, error)

// Kitty talks to one Kitty instance.
type Kitty struct {
	Kitten string // absolute path of the kitten binary
	To     string // remote-control address, e.g. unix:/…/kitty-acmd-123
	Run    Runner
}

// New returns a Kitty for socket using the real runner.
func New(kitten, socket string) *Kitty {
	return &Kitty{Kitten: kitten, To: socket, Run: execRunner}
}

func execRunner(ctx context.Context, kitten, to string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, kitten, append([]string{"@", "--to", to}, args...)...) // #nosec G204 -- fixed kitten binary, argv not via a shell
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("kitten @ %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func (k *Kitty) run(ctx context.Context, args ...string) ([]byte, error) {
	if k.To == "" {
		return nil, errors.New("no Kitty remote-control socket (restart Kitty after `acmd init`)")
	}
	return k.Run(ctx, k.Kitten, k.To, args...)
}

// FindKitten locates the kitten binary: on pathList, then inside Kitty.app.
func FindKitten(lookPath func(string) (string, error)) string {
	if p, err := lookPath("kitten"); err == nil {
		return p
	}
	for _, p := range []string{"/Applications/kitty.app/Contents/MacOS/kitten", "/opt/homebrew/bin/kitten", "/usr/local/bin/kitten"} {
		if st, err := os.Stat(p); err == nil && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// Window is a Kitty window (a pane) from `kitten @ ls`.
type Window struct {
	ID        int               `json:"id"`
	Columns   int               `json:"columns"`
	IsFocused bool              `json:"is_focused"`
	UserVars  map[string]string `json:"user_vars"`
}

// Tab is a Kitty tab.
type Tab struct {
	ID       int      `json:"id"`
	Layout   string   `json:"layout"`
	IsActive bool     `json:"is_active"`
	Windows  []Window `json:"windows"`
}

// OSWindow is a Kitty OS window.
type OSWindow struct {
	ID        int   `json:"id"`
	IsFocused bool  `json:"is_focused"`
	Tabs      []Tab `json:"tabs"`
}

// LS lists the instance's windows.
func (k *Kitty) LS(ctx context.Context) ([]OSWindow, error) {
	out, err := k.run(ctx, "ls")
	if err != nil {
		return nil, err
	}
	var ws []OSWindow
	if err := json.Unmarshal(out, &ws); err != nil {
		return nil, fmt.Errorf("kitten @ ls: %w", err)
	}
	return ws, nil
}

// Find returns the window with id and its tab; ok is false if it's gone.
func Find(ws []OSWindow, id int) (Window, Tab, bool) {
	for _, ow := range ws {
		for _, t := range ow.Tabs {
			for _, w := range t.Windows {
				if w.ID == id {
					return w, t, true
				}
			}
		}
	}
	return Window{}, Tab{}, false
}

// FindVar returns the first window whose user variable name has value.
func FindVar(ws []OSWindow, name, value string) (Window, Tab, bool) {
	for _, ow := range ws {
		for _, t := range ow.Tabs {
			for _, w := range t.Windows {
				if w.UserVars[name] == value {
					return w, t, true
				}
			}
		}
	}
	return Window{}, Tab{}, false
}

func match(id int) string { return "id:" + strconv.Itoa(id) }

// SidebarSpec describes the sidebar to add next to a view pane.
type SidebarSpec struct {
	View    int      // the view pane's window id
	ViewID  string   // acmd's view id (user var acmd_view)
	Width   int      // columns
	Command []string // argv, absolute path first
	Env     []string // KEY=VALUE for the sidebar process
}

// AddSidebarLeft splits the view's tab, puts the sidebar on the left edge
// at Width columns and gives focus back to the view pane (M0 K6, K12).
// It returns the sidebar's window id and the tab layout before (to
// restore when the view closes).
func (k *Kitty) AddSidebarLeft(ctx context.Context, s SidebarSpec) (sidebar int, prevLayout string, err error) {
	ws, err := k.LS(ctx)
	if err != nil {
		return 0, "", err
	}
	_, tab, ok := Find(ws, s.View)
	if !ok {
		return 0, "", fmt.Errorf("kitty window %d not found", s.View)
	}
	prevLayout = tab.Layout
	if tab.Layout != "splits" {
		if _, err := k.run(ctx, "goto-layout", "--match", match(s.View), "splits"); err != nil {
			return 0, prevLayout, err
		}
	}
	args := []string{"launch", "--location=vsplit", "--match", match(s.View),
		"--var", "acmd_role=sidebar", "--var", "acmd_view=" + s.ViewID, "--title", "acmd"}
	for _, e := range s.Env {
		args = append(args, "--env", e)
	}
	out, err := k.run(ctx, append(append(args, "--"), s.Command...)...)
	if err != nil {
		return 0, prevLayout, err
	}
	if sidebar, err = strconv.Atoi(strings.TrimSpace(string(out))); err != nil {
		return 0, prevLayout, fmt.Errorf("kitten @ launch printed %q", out)
	}
	// move_to_screen_edge only works on the focused window of the active tab.
	if _, err := k.run(ctx, "focus-window", "--match", match(sidebar)); err != nil {
		return sidebar, prevLayout, err
	}
	if _, err := k.run(ctx, "action", "--match", match(sidebar), "layout_action", "move_to_screen_edge", "left"); err != nil {
		return sidebar, prevLayout, err
	}
	if err := k.SetWidth(ctx, sidebar, s.Width); err != nil {
		return sidebar, prevLayout, err
	}
	_, err = k.run(ctx, "focus-window", "--match", match(s.View))
	return sidebar, prevLayout, err
}

// minView is the fewest columns SetWidth leaves the other panes of the row.
const minView = 20

// SetWidth resizes window id to cols columns: resize, read back, correct
// (resizes are off by the border width, M0 K2), at most 3 tries. The width
// is capped so the rest of the tab keeps minView columns, it stops when a
// resize makes no progress, and Kitty refusing a resize ("could not
// resize", the split is at its limit) is not an error.
func (k *Kitty) SetWidth(ctx context.Context, id, cols int) error {
	last := -1
	for range 3 {
		ws, err := k.LS(ctx)
		if err != nil {
			return err
		}
		w, tab, ok := Find(ws, id)
		if !ok {
			return fmt.Errorf("kitty window %d not found", id)
		}
		if tab.Layout == "stack" {
			return nil // hidden (stacked) sidebars keep their split (K5)
		}
		want := cols
		if len(tab.Windows) > 1 {
			// The widest other pane approximates the rest of the row.
			rest := 0
			for _, o := range tab.Windows {
				if o.ID != id {
					rest = max(rest, o.Columns)
				}
			}
			want = min(cols, max(1, w.Columns+rest-minView))
		}
		if w.Columns == want || w.Columns == last {
			return nil
		}
		last = w.Columns
		if _, err := k.run(ctx, "resize-window", "--match", match(id), "--axis", "horizontal",
			"--increment", strconv.Itoa(want-w.Columns)); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "could not resize") {
				return nil
			}
			return err
		}
	}
	return nil
}

// Hidden reports whether the view's sidebar is hidden (its tab is stacked).
func (k *Kitty) Hidden(ctx context.Context, view int) (bool, error) {
	ws, err := k.LS(ctx)
	if err != nil {
		return false, err
	}
	_, tab, ok := Find(ws, view)
	if !ok {
		return false, fmt.Errorf("kitty window %d not found", view)
	}
	return tab.Layout == "stack", nil
}

// ToggleSidebar hides or shows the sidebar by toggling the view tab's
// stack layout, which restores the exact split (K5).
func (k *Kitty) ToggleSidebar(ctx context.Context, view int) error {
	_, err := k.run(ctx, "action", "--match", match(view), "toggle_layout", "stack")
	return err
}

// Focus focuses a window (switching tab and raising its OS window).
func (k *Kitty) Focus(ctx context.Context, id int) error {
	_, err := k.run(ctx, "focus-window", "--match", match(id))
	return err
}

// Close closes a window acmd created.
func (k *Kitty) Close(ctx context.Context, id int) error {
	_, err := k.run(ctx, "close-window", "--match", match(id))
	return err
}

// SetLayout sets the layout of the tab holding window id.
func (k *Kitty) SetLayout(ctx context.Context, id int, layout string) error {
	_, err := k.run(ctx, "goto-layout", "--match", match(id), layout)
	return err
}

// Overlay runs argv in an overlay window over window id (it closes when
// the program exits). Used for prompts a background shortcut can't show.
func (k *Kitty) Overlay(ctx context.Context, id int, argv, env []string) error {
	args := []string{"launch", "--type=overlay", "--match", match(id)}
	for _, e := range env {
		args = append(args, "--env", e)
	}
	_, err := k.run(ctx, append(append(args, "--"), argv...)...)
	return err
}

// LaunchTab runs argv in a new tab next to window id (in its OS window),
// in dir. Used for terminal editors (`run_in = "tab"`, SPEC §8.6).
func (k *Kitty) LaunchTab(ctx context.Context, id int, dir string, argv, env []string) error {
	args := []string{"launch", "--type=tab", "--match", match(id), "--cwd", dir}
	for _, e := range env {
		args = append(args, "--env", e)
	}
	_, err := k.run(ctx, append(append(args, "--"), argv...)...)
	return err
}
