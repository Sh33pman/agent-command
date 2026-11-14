// Package doctor implements `acmd doctor`: read-only checks of
// acmd's setup, Claude Code and the daemon, each with a fix to run.
// Everything that looks at the machine comes in through Env, so the checks
// are testable without touching the real one.
package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/setup"
)

// Minimum versions acmd is tested with (M0 findings).
const (
	MinClaude  = "2.1.314"
	MinKitty   = "0.49.0"
	MinGhostty = "1.4.0"
)

// Status is a check's outcome.
type Status int

// Outcomes, from best to worst.
const (
	OK Status = iota
	Info
	Warn
	Fail
)

func (s Status) mark() string {
	return [...]string{"✓", "·", "!", "✗"}[s]
}

// Check is one line of the report.
type Check struct {
	Section string
	Status  Status
	Text    string
	Fix     string // a command or instruction; "" when nothing to do
}

// Report is the outcome of all checks.
type Report struct{ Checks []Check }

func (r *Report) add(section string, s Status, fix, format string, args ...any) {
	r.Checks = append(r.Checks, Check{Section: section, Status: s, Text: fmt.Sprintf(format, args...), Fix: fix})
}

// Count returns how many checks have status s.
func (r *Report) Count(s Status) int {
	n := 0
	for _, c := range r.Checks {
		if c.Status == s {
			n++
		}
	}
	return n
}

// Write prints the report grouped by section (in order of first appearance).
func (r *Report) Write(w io.Writer) {
	var sections []string
	bySection := map[string][]Check{}
	for _, c := range r.Checks {
		if _, ok := bySection[c.Section]; !ok {
			sections = append(sections, c.Section)
		}
		bySection[c.Section] = append(bySection[c.Section], c)
	}
	for i, s := range sections {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, s)
		for _, c := range bySection[s] {
			fmt.Fprintf(w, "  %s %s\n", c.Status.mark(), c.Text)
			if c.Fix != "" && c.Status >= Warn {
				fmt.Fprintf(w, "      fix: %s\n", c.Fix)
			}
		}
	}
	fails, warns := r.Count(Fail), r.Count(Warn)
	fmt.Fprintln(w)
	if fails == 0 && warns == 0 {
		fmt.Fprintln(w, "Everything looks good.")
		return
	}
	fmt.Fprintf(w, "%s, %s.\n", plural(fails, "problem"), plural(warns, "warning"))
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

// DaemonState is what a connection attempt to the daemon found.
type DaemonState struct {
	Running  bool
	Status   ipc.Status // when running this version
	Mismatch string     // version of a daemon of another version
	Binary   string     // the program it runs ("" = unknown)
}

// Env is everything the checks read.
type Env struct {
	Home     string
	Paths    paths.Paths
	Version  string // this binary's version
	AcmdPath string // the stable path of this binary (what init would write)
	Getenv   func(string) string
	Read     func(string) ([]byte, error)
	Stat     func(string) (fs.FileInfo, error)
	// Resolve follows symlinks (filepath.EvalSymlinks).
	Resolve func(string) (string, error)
	// LoginPath is the PATH of the saved login-shell environment.
	LoginPath string
	// LookPath finds an executable on LoginPath.
	LookPath func(string) (string, error)
	// Output runs a program and returns its stdout (for --version).
	Output    func(ctx context.Context, name string, args ...string) ([]byte, error)
	HasBundle func(id string) bool
	Daemon    func(ctx context.Context) (DaemonState, error)
	// InitPlan is what `acmd init` would change now (nil when setup can't
	// be planned).
	InitPlan func(ctx context.Context) (*setup.Plan, error)
}

// Run performs every check.
func Run(ctx context.Context, e Env) *Report {
	r := &Report{}
	rec, initDone := checkAcmd(r, e)
	checkClaude(ctx, r, e, rec, initDone)
	checkDaemon(ctx, r, e)
	if initDone {
		checkPlan(ctx, r, e)
	}
	return r
}

const (
	secAcmd   = "acmd"
	secClaude = "Claude Code"
	secDaemon = "Daemon"
	fixInit   = "acmd doctor --fix"
)

func checkAcmd(r *Report, e Env) (setup.Record, bool) {
	r.add(secAcmd, Info, "", "version %s at %s", e.Version, setup.Tilde(e.AcmdPath, e.Home))

	rec, ok, err := setup.ReadRecord(e.Paths.Setup())
	switch {
	case err != nil:
		r.add(secAcmd, Fail, fixInit, "setup record unreadable: %v", err)
	case !ok:
		r.add(secAcmd, Fail, fixInit, "setup hasn't run yet")
	default:
		r.add(secAcmd, OK, "", "set up by acmd %s on %s", rec.Version, rec.At.Local().Format("2006-01-02 15:04"))
		switch {
		case !e.executable(rec.AcmdPath):
			r.add(secAcmd, Fail, fixInit, "hooks use %s, which no longer exists", setup.Tilde(rec.AcmdPath, e.Home))
		case rec.AcmdPath != e.AcmdPath:
			r.add(secAcmd, Warn, "rerun `acmd init` with the acmd you want to keep",
				"hooks use %s, but this is %s", setup.Tilde(rec.AcmdPath, e.Home), setup.Tilde(e.AcmdPath, e.Home))
		}
	}

	if installs := e.installs(); len(installs) > 1 {
		r.add(secAcmd, Warn, "remove the ones you don't use (Homebrew: brew uninstall agent-command; dev: rm ~/.local/bin/acmd ~/.local/bin/agent-command)",
			"several acmd installs on your PATH (the first wins): %s", strings.Join(installs, ", "))
	}

	cfg, err := config.Load(e.Paths.Config())
	if err != nil {
		r.add(secAcmd, Fail, "fix the file, or move it away and run `acmd init` to recreate it", "%s: %v", setup.Tilde(e.Paths.Config(), e.Home), err)
	} else if ok {
		checkEditor(r, e, cfg.Editor) // before setup, init asks for it
	}

	switch st, err := e.Stat(e.Paths.Env()); {
	case errors.Is(err, fs.ErrNotExist):
		if ok {
			r.add(secAcmd, Warn, "acmd init --refresh-env", "no saved login-shell environment (sessions get a minimal PATH)")
		}
	case err != nil:
		r.add(secAcmd, Fail, "", "%s: %v", setup.Tilde(e.Paths.Env(), e.Home), err)
	case st.Mode().Perm()&0o077 != 0:
		r.add(secAcmd, Warn, "chmod 600 "+setup.Tilde(e.Paths.Env(), e.Home), "%s is readable by others (%s); it holds your environment",
			setup.Tilde(e.Paths.Env(), e.Home), st.Mode().Perm())
	default:
		r.add(secAcmd, OK, "", "login-shell environment saved")
	}
	for _, d := range []string{e.Paths.StateDir, e.Paths.RuntimeDir} {
		if st, err := e.Stat(d); err == nil && st.Mode().Perm()&0o077 != 0 {
			r.add(secAcmd, Warn, "chmod 700 "+setup.Tilde(d, e.Home), "%s is accessible by others (%s)", setup.Tilde(d, e.Home), st.Mode().Perm())
		}
	}
	return rec, ok
}

func checkEditor(r *Report, e Env, ed config.Editor) {
	switch ed.Preset {
	case "":
		r.add(secAcmd, Warn, "acmd editor set", "no editor chosen for opening session folders")
	case "custom":
		if len(ed.Command) == 0 {
			r.add(secAcmd, Fail, "acmd editor set custom -- <command> {dir}", "custom editor has no command")
			return
		}
		if _, err := e.LookPath(ed.Command[0]); err != nil {
			r.add(secAcmd, Fail, "acmd editor set", "editor command %q not found", ed.Command[0])
			return
		}
		r.add(secAcmd, OK, "", "editor: %s", strings.Join(ed.Command, " "))
	default:
		preset, ok := setup.PresetByName(ed.Preset)
		if !ok {
			r.add(secAcmd, Fail, "acmd editor set", "unknown editor preset %q", ed.Preset)
			return
		}
		if ed.Resolved != "" && e.executable(ed.Resolved) {
			r.add(secAcmd, OK, "", "editor: %s (%s)", preset.Label, setup.Tilde(ed.Resolved, e.Home))
			return
		}
		if l, err := e.LookPath(preset.Launcher); err == nil {
			r.add(secAcmd, Warn, fixInit, "editor launcher moved to %s", setup.Tilde(l, e.Home))
			return
		}
		for _, id := range preset.Bundles {
			if e.HasBundle(id) {
				r.add(secAcmd, OK, "", "editor: %s (opened as an app)", preset.Label)
				return
			}
		}
		r.add(secAcmd, Fail, "install it, or: acmd editor set", "editor %s isn't installed", preset.Label)
	}
}

// installs lists the distinct acmd binaries on the login PATH, in PATH
// order (symlinks to the same file are one install).
func (e Env) installs() []string {
	var out []string
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(e.LoginPath) {
		p := filepath.Join(dir, "acmd")
		if !filepath.IsAbs(dir) || !e.executable(p) {
			continue
		}
		target, err := e.Resolve(p)
		if err != nil {
			target = p
		}
		if !seen[target] {
			seen[target] = true
			out = append(out, setup.Tilde(p, e.Home))
		}
	}
	return out
}

func (e Env) executable(p string) bool {
	st, err := e.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

func checkClaude(ctx context.Context, r *Report, e Env, rec setup.Record, initDone bool) {
	claude, err := e.LookPath("claude")
	if err != nil {
		r.add(secClaude, Fail, "install Claude Code, then: acmd init --refresh-env", "claude not found on your login shell's PATH")
	} else {
		checkVersion(ctx, r, e, secClaude, claude, "claude", MinClaude, ParseClaudeVersion)
	}

	dir := setup.ClaudeConfigDir(e.Home, e.Getenv)
	if initDone && rec.ClaudeDir != "" && filepath.Clean(rec.ClaudeDir) != filepath.Clean(dir) {
		r.add(secClaude, Fail, "run `acmd init` from a shell with the CLAUDE_CONFIG_DIR your sessions use",
			"hooks are in %s, but Claude now reads %s (CLAUDE_CONFIG_DIR)", setup.Tilde(rec.ClaudeDir, e.Home), setup.Tilde(dir, e.Home))
	} else if filepath.Clean(dir) != filepath.Join(e.Home, ".claude") {
		r.add(secClaude, Info, "", "CLAUDE_CONFIG_DIR is set: hooks go into %s; sessions started without it won't be tracked", setup.Tilde(dir, e.Home))
	}

	settingsPath := filepath.Join(dir, "settings.json")
	settings, err := e.Read(settingsPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if initDone {
			r.add(secClaude, Fail, fixInit, "%s is missing, so no hooks are registered", setup.Tilde(settingsPath, e.Home))
		}
	case err != nil:
		r.add(secClaude, Fail, "", "%s: %v", setup.Tilde(settingsPath, e.Home), err)
	default:
		checkHooks(r, e, settingsPath, settings, initDone)
		if b, _ := boolSetting(settings, "disableAllHooks"); b {
			r.add(secClaude, Fail, "remove \"disableAllHooks\" from "+setup.Tilde(settingsPath, e.Home),
				"disableAllHooks is on in your settings: Claude runs no hooks, so acmd sees nothing")
		}
	}

	for _, m := range ManagedSettings {
		b, err := e.Read(m)
		if err != nil {
			continue
		}
		if v, _ := boolSetting(b, "disableAllHooks"); v {
			r.add(secClaude, Fail, "ask your administrator ("+m+")", "managed settings disable all hooks: acmd can't track sessions")
		}
		if v, _ := boolSetting(b, "allowManagedHooksOnly"); v {
			r.add(secClaude, Fail, "ask your administrator ("+m+")", "managed settings allow only managed hooks: acmd's hooks are ignored")
		}
	}
}

func checkHooks(r *Report, e Env, path string, settings []byte, initDone bool) {
	hookPaths, events := setup.HookPaths(settings)
	total := len(setup.HookEvents)
	switch {
	case events == 0:
		if initDone {
			r.add(secClaude, Fail, fixInit, "no acmd hooks in %s", setup.Tilde(path, e.Home))
		}
		return
	case events < total:
		r.add(secClaude, Fail, fixInit, "acmd hooks for only %d of %d events", events, total)
	}
	for _, p := range hookPaths {
		if !e.executable(p) {
			r.add(secClaude, Fail, fixInit, "hooks run %s, which doesn't exist (sessions aren't tracked)", setup.Tilde(p, e.Home))
		}
	}
	if len(hookPaths) > 1 {
		r.add(secClaude, Warn, fixInit, "hooks point at %d different acmd binaries", len(hookPaths))
	}
	if events == total && len(hookPaths) == 1 && e.executable(hookPaths[0]) {
		r.add(secClaude, OK, "", "hooks registered for all %d events", total)
	}
}

// boolSetting reads a top-level boolean from a settings JSON file.
func boolSetting(b []byte, key string) (value, ok bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return false, false
	}
	var v bool
	if json.Unmarshal(m[key], &v) != nil {
		return false, false
	}
	return v, true
}

func checkVersion(ctx context.Context, r *Report, e Env, section, bin, name, minimum string, parse func(string) string) {
	out, err := e.Output(ctx, bin, "--version")
	v := parse(string(out))
	switch {
	case err != nil || v == "":
		r.add(section, Warn, "", "couldn't read the %s version (%s --version)", name, setup.Tilde(bin, e.Home))
	case CompareVersions(v, minimum) < 0:
		r.add(section, Fail, "upgrade "+name, "%s %s is older than %s, the oldest acmd is tested with", name, v, minimum)
	default:
		r.add(section, OK, "", "%s %s (%s)", name, v, setup.Tilde(bin, e.Home))
	}
}

var semver = regexp.MustCompile(`\b(\d+\.\d+(?:\.\d+)?)\b`)

// ParseClaudeVersion extracts the version from `claude --version` ("2.1.314 (Claude Code)").
func ParseClaudeVersion(out string) string {
	return firstVersion(out)
}

func firstVersion(out string) string {
	if m := semver.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

// CompareVersions compares dotted numeric versions (missing parts are 0).
func CompareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(pa), len(pb)) {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func checkDaemon(ctx context.Context, r *Report, e Env) {
	d, err := e.Daemon(ctx)
	switch {
	case err != nil:
		r.add(secDaemon, Fail, "see "+setup.Tilde(e.Paths.DaemonLog(), e.Home), "can't talk to the daemon: %v", err)
	case d.Binary != "" && !e.executable(d.Binary):
		r.add(secDaemon, Fail, fixInit, "running from %s, which no longer exists", setup.Tilde(d.Binary, e.Home))
	case d.Mismatch != "":
		r.add(secDaemon, Warn, fixInit, "running acmd %s (this is %s)", d.Mismatch, e.Version)
	case !d.Running:
		r.add(secDaemon, Info, "", "not running (it starts with the first session or command)")
	default:
		r.add(secDaemon, OK, "", "running (pid %d): %s, %s recorded", d.Status.Pid, plural(d.Status.Sessions, "session"), plural(d.Status.Events, "event"))
	}
}

// checkPlan reports anything `acmd init` would still change: the catch-all
// for configs that drifted (moved binary, edited or deleted files).
func checkPlan(ctx context.Context, r *Report, e Env) {
	if e.InitPlan == nil {
		return
	}
	plan, err := e.InitPlan(ctx)
	if err != nil {
		r.add(secAcmd, Warn, "acmd init --dry-run", "couldn't check whether setup is current: %v", err)
		return
	}
	pending := plan.Pending()
	if len(pending) == 0 {
		r.add(secAcmd, OK, "", "setup is up to date")
		return
	}
	var files []string
	for _, c := range pending {
		files = append(files, setup.Tilde(c.Path, e.Home))
	}
	r.add(secAcmd, Warn, fixInit,
		"acmd init would update %s", strings.Join(files, ", "))
}
