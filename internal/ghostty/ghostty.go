// Package ghostty is acmd's Ghostty adapter (SPEC §7.3): AppleScript via
// `osascript`, one script per operation, values passed as arguments and
// never put into script text, terminals addressed by id (M0 G2).
package ghostty

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ErrNotPermitted means macOS denied acmd's terminal Automation access to
// Ghostty (AppleScript error -1743).
var ErrNotPermitted = errors.New("not allowed to control Ghostty: allow it in System Settings → Privacy & Security → Automation")

// ErrNotFound means the terminal (pane) no longer exists.
var ErrNotFound = errors.New("Ghostty terminal not found") //nolint:staticcheck // ST1005: product name

// Runner runs an AppleScript with arguments and returns its result.
type Runner func(ctx context.Context, script string, args ...string) (string, error)

// App talks to the running Ghostty.
type App struct{ Run Runner }

// New returns an App using osascript.
func New() *App { return &App{Run: osascript} }

func osascript(ctx context.Context, script string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", append([]string{"-"}, args...)...) // #nosec G204 -- fixed binary; values are script arguments
	cmd.Stdin = strings.NewReader(script)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := stderr.String()
		if strings.Contains(msg, "-1743") || strings.Contains(msg, "Not authorized") {
			return "", ErrNotPermitted
		}
		return "", fmt.Errorf("osascript: %w: %s", err, strings.TrimSpace(msg))
	}
	return strings.TrimSpace(string(out)), nil
}

// find is prepended to scripts: the terminal with an id (or missing value).
const find = `
on findTerm(theId)
	tell application "Ghostty"
		repeat with w in windows
			repeat with t in tabs of w
				repeat with term in terminals of t
					if (id of term) is theId then return contents of term
				end repeat
			end repeat
		end repeat
	end tell
	return missing value
end findTerm
`

func (a *App) run(ctx context.Context, body string, args ...string) (string, error) {
	out, err := a.Run(ctx, find+body, args...)
	if err == nil && out == "not found" {
		return "", ErrNotFound
	}
	return out, err
}

// TermForTTY returns the id of the terminal whose tty is tty (/dev/ttysNNN).
func (a *App) TermForTTY(ctx context.Context, tty string) (string, error) {
	return a.run(ctx, `
on run argv
	tell application "Ghostty"
		repeat with w in windows
			repeat with t in tabs of w
				repeat with term in terminals of t
					if (tty of term) is item 1 of argv then return id of term
				end repeat
			end repeat
		end repeat
	end tell
	return "not found"
end run`, tty)
}

// AddSidebarLeft splits the view terminal to the left, running command
// in cwd with env (KEY=VALUE), and gives focus back to the view (G3).
// It returns the sidebar terminal's id.
func (a *App) AddSidebarLeft(ctx context.Context, view, command, cwd string, env []string) (string, error) {
	return a.run(ctx, `
on run argv
	set view to my findTerm(item 1 of argv)
	if view is missing value then return "not found"
	tell application "Ghostty"
		set cfg to new surface configuration
		set command of cfg to item 2 of argv
		set initial working directory of cfg to item 3 of argv
		if item 4 of argv is not "" then
			set AppleScript's text item delimiters to linefeed
			set environment variables of cfg to text items of (item 4 of argv)
		end if
		set side to split view direction left with configuration cfg
		set sideId to id of side
		focus view
		return sideId
	end tell
end run`, view, command, cwd, strings.Join(env, "\n"))
}

// Action performs a Ghostty action (e.g. "toggle_split_zoom",
// "resize_split:right,40") on a terminal.
func (a *App) Action(ctx context.Context, term, action string) error {
	_, err := a.run(ctx, `
on run argv
	set term to my findTerm(item 1 of argv)
	if term is missing value then return "not found"
	tell application "Ghostty" to perform action (item 2 of argv) on term
	return "ok"
end run`, term, action)
	return err
}

// Focus focuses a terminal and raises its window (G6).
func (a *App) Focus(ctx context.Context, term string) error {
	_, err := a.run(ctx, `
on run argv
	set term to my findTerm(item 1 of argv)
	if term is missing value then return "not found"
	tell application "Ghostty" to focus term
	return "ok"
end run`, term)
	return err
}

// Close closes a terminal acmd created (asynchronous, G12).
func (a *App) Close(ctx context.Context, term string) error {
	_, err := a.run(ctx, `
on run argv
	set term to my findTerm(item 1 of argv)
	if term is missing value then return "not found"
	tell application "Ghostty" to close term
	return "ok"
end run`, term)
	return err
}

// Exists reports whether a terminal still exists.
func (a *App) Exists(ctx context.Context, term string) (bool, error) {
	out, err := a.run(ctx, `
on run argv
	if my findTerm(item 1 of argv) is missing value then return "no"
	return "yes"
end run`, term)
	return out == "yes", err
}

// NewTab opens a tab in the window holding term, running command in cwd.
// Used for prompts and terminal editors (Ghostty has no overlays).
func (a *App) NewTab(ctx context.Context, term, command, cwd string, env []string) error {
	_, err := a.run(ctx, `
on run argv
	tell application "Ghostty"
		repeat with w in windows
			repeat with t in tabs of w
				repeat with x in terminals of t
					if (id of x) is item 1 of argv then
						set cfg to new surface configuration
						set command of cfg to item 2 of argv
						set initial working directory of cfg to item 3 of argv
						if item 4 of argv is not "" then
							set AppleScript's text item delimiters to linefeed
							set environment variables of cfg to text items of (item 4 of argv)
						end if
						new tab in (contents of w) with configuration cfg
						return "ok"
					end if
				end repeat
			end repeat
		end repeat
	end tell
	return "not found"
end run`, term, command, cwd, strings.Join(env, "\n"))
	return err
}

// Running reports whether Ghostty runs, without launching it; when it
// does, it also checks acmd may control it (ErrNotPermitted).
func (a *App) Running(ctx context.Context) (bool, error) {
	out, err := a.Run(ctx, `
if application "Ghostty" is running then
	tell application "Ghostty" to get version
	return "running"
end if
return "stopped"`)
	return out == "running", err
}

// ReloadConfig makes Ghostty reload its config (G11), if it's running.
func (a *App) ReloadConfig(ctx context.Context) error {
	_, err := a.Run(ctx, `
if application "Ghostty" is running then
	tell application "Ghostty"
		if (count of windows) > 0 then
			perform action "reload_config" on (focused terminal of selected tab of front window)
		end if
	end tell
end if
return "ok"`)
	return err
}

// Quote makes argv one command line for Ghostty's `command` (run by a
// shell): every argument single-quoted.
func Quote(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

// ResizeAction is the Ghostty action that changes the width of a left
// sidebar by delta columns at pxPerCol pixels per column (G5: pixels).
func ResizeAction(delta int, pxPerCol float64) string {
	dir := "right"
	if delta < 0 {
		dir, delta = "left", -delta
	}
	return fmt.Sprintf("resize_split:%s,%d", dir, int(float64(delta)*pxPerCol+0.5))
}

// Type types text into a terminal and presses Enter (G9: delivered as a
// paste). Used only for "move into acmd", with confirmation (SPEC §6.3).
func (a *App) Type(ctx context.Context, term, text string) error {
	_, err := a.run(ctx, `
on run argv
	set term to my findTerm(item 1 of argv)
	if term is missing value then return "not found"
	tell application "Ghostty"
		input text (item 2 of argv) to term
		send key "enter" to term
	end tell
	return "ok"
end run`, term, text)
	return err
}

// NewWindow opens a window running command in cwd (split out, SPEC §6.6).
func (a *App) NewWindow(ctx context.Context, command, cwd string, env []string) error {
	_, err := a.Run(ctx, `
on run argv
	tell application "Ghostty"
		set cfg to new surface configuration
		set command of cfg to item 1 of argv
		set initial working directory of cfg to item 2 of argv
		if item 3 of argv is not "" then
			set AppleScript's text item delimiters to linefeed
			set environment variables of cfg to text items of (item 3 of argv)
		end if
		new window with configuration cfg
	end tell
	return "ok"
end run`, command, cwd, strings.Join(env, "\n"))
	return err
}

// FocusedTerm is the focused terminal's id when Ghostty is the frontmost
// app, else "" (notification suppression, SPEC §8.5).
func (a *App) FocusedTerm(ctx context.Context) (string, error) {
	return a.Run(ctx, `
if application "Ghostty" is running then
	tell application "Ghostty"
		if frontmost and (count of windows) > 0 then return id of focused terminal of selected tab of front window
	end tell
end if
return ""`)
}
