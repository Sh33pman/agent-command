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
	"github.com/brettmostert/agent-command/internal/remote/identity"
	"github.com/brettmostert/agent-command/internal/service"
	"github.com/brettmostert/agent-command/internal/setup"
)

// MinClaude is the oldest Claude Code acmd is tested with.
const MinClaude = "2.1.314"

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
	// RemoteHosts are the Remote hosts it knows, and their connections.
	RemoteHosts []ipc.RemoteHost
}

// Env is everything the checks read.
type Env struct {
	// ClientOnly is a machine whose daemon runs no sessions (Windows): no
	// agents, so their checks are left out.
	ClientOnly bool
	Home       string
	Paths      paths.Paths
	Version    string // this binary's version
	AcmdPath   string // the stable path of this binary (what init would write)
	Getenv     func(string) string
	Read       func(string) ([]byte, error)
	Stat       func(string) (fs.FileInfo, error)
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
	// Service is the service that runs the daemon (nil: not checked).
	Service func(ctx context.Context) service.Status
	// OthersCanUse says who besides the user can read a file or open a
	// folder, and how to fix it (paths.OthersCanRead, for Windows' access
	// lists). nil reads the mode bits, as on Unix.
	OthersCanUse func(path string) (who, fix string, err error)
}

// Run performs every check.
func Run(ctx context.Context, e Env) *Report {
	r := &Report{}
	rec, initDone := checkAcmd(r, e)
	if !e.ClientOnly {
		checkClaude(ctx, r, e, rec, initDone)
		checkAntigravity(r, e, initDone)
	}
	checkDaemon(ctx, r, e)
	checkService(ctx, r, e)
	checkIdentity(r, e)
	if initDone && !e.ClientOnly {
		checkPlan(ctx, r, e)
	}
	return r
}

const (
	secAcmd   = "acmd"
	secClaude = "Claude Code"
	secAgy    = "Antigravity"
	secDaemon = "Daemon"
	secSvc    = "Service"
	secRemote = "Remote sessions"
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

	if e.ClientOnly {
		// No sessions here, so no login-shell environment for them.
		checkPrivate(r, e)
		return rec, ok
	}
	switch st, err := e.Stat(e.Paths.Env()); {
	case e.OthersCanUse != nil && err == nil:
		if who, fix, _ := e.OthersCanUse(e.Paths.Env()); who != "" {
			r.add(secAcmd, Warn, fix, "%s is readable by others (%s); it holds your environment", setup.Tilde(e.Paths.Env(), e.Home), who)
		} else {
			r.add(secAcmd, OK, "", "login-shell environment saved")
		}
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
	checkPrivate(r, e)
	return rec, ok
}

// checkPrivate warns about acmd's folders when others can use them.
func checkPrivate(r *Report, e Env) {
	for _, d := range []string{e.Paths.StateDir, e.Paths.RuntimeDir} {
		if e.OthersCanUse != nil {
			if who, fix, _ := e.OthersCanUse(d); who != "" {
				r.add(secAcmd, Warn, fix, "%s is accessible by others (%s)", setup.Tilde(d, e.Home), who)
			}
			continue
		}
		if st, err := e.Stat(d); err == nil && st.Mode().Perm()&0o077 != 0 {
			r.add(secAcmd, Warn, "chmod 700 "+setup.Tilde(d, e.Home), "%s is accessible by others (%s)", setup.Tilde(d, e.Home), st.Mode().Perm())
		}
	}
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
	return err == nil && paths.IsExecutable(st)
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

	// Each Claude harness with its own folder has its own settings.json.
	for _, dir := range rec.ClaudeDirs {
		path := filepath.Join(dir, "settings.json")
		if filepath.Clean(dir) == filepath.Clean(setup.ClaudeConfigDir(e.Home, e.Getenv)) {
			continue
		}
		b, err := e.Read(path)
		if err != nil {
			r.add(secClaude, Fail, fixInit, "%s is missing, so a harness's sessions there aren't tracked", setup.Tilde(path, e.Home))
			continue
		}
		checkHooks(r, e, path, b, initDone)
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

// checkAntigravity reports agy's hooks. agy is optional: without it, and
// without acmd's hooks for it, the section isn't shown.
func checkAntigravity(r *Report, e Env, initDone bool) {
	path := filepath.Join(setup.AntigravityConfigDir(e.Home), "hooks.json")
	file, err := e.Read(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		r.add(secAgy, Fail, "", "%s: %v", setup.Tilde(path, e.Home), err)
		return
	}
	st := setup.AntigravityHooks(file)
	agy, lookErr := e.LookPath("agy")
	if lookErr != nil {
		if st.Events > 0 {
			r.add(secAgy, Info, "", "agy isn't on your login shell's PATH; acmd's hooks for it are still in %s", setup.Tilde(path, e.Home))
		}
		return
	}
	r.add(secAgy, OK, "", "agy (%s)", setup.Tilde(agy, e.Home))
	total := len(setup.AntigravityEvents)
	switch {
	case st.Events == 0:
		if initDone {
			r.add(secAgy, Warn, fixInit, "no acmd hooks in %s, so Antigravity sessions show no status", setup.Tilde(path, e.Home))
		}
		return
	case st.Events < total:
		r.add(secAgy, Fail, fixInit, "acmd hooks for only %d of %d events", st.Events, total)
	}
	if st.Disabled {
		r.add(secAgy, Fail, "turn the \""+setup.AntigravityHookName+"\" hook back on in agy's /hooks", "acmd's hook is turned off in %s", setup.Tilde(path, e.Home))
	}
	for _, p := range st.Paths {
		if !e.executable(p) {
			r.add(secAgy, Fail, fixInit, "hooks run %s, which doesn't exist (sessions aren't tracked)", setup.Tilde(p, e.Home))
		}
	}
	if len(st.Paths) > 1 {
		r.add(secAgy, Warn, fixInit, "hooks point at %d different acmd binaries", len(st.Paths))
	}
	if st.Events == total && !st.Disabled && len(st.Paths) == 1 && e.executable(st.Paths[0]) {
		r.add(secAgy, OK, "", "hooks registered for all %d events (no waiting status: agy has no hook for its prompts)", total)
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
		checkRemote(r, e, d.Status.RemoteSessions)
		checkRemoteHosts(r, d.RemoteHosts)
	}
}

// checkService says whether the service that runs the daemon from boot or
// login is installed and running, and runs this acmd.
func checkService(ctx context.Context, r *Report, e Env) {
	if e.Service == nil {
		return
	}
	s := e.Service(ctx)
	switch {
	case !s.Supported:
		r.add(secSvc, Info, "", "%s", firstSentence(s.Note))
	case !s.Installed:
		r.add(secSvc, Warn, "acmd service install", "not installed, so the daemon doesn't start at %s", startsAt(s))
	case s.Binary != "" && !samePath(e, s.Binary, e.AcmdPath):
		r.add(secSvc, Warn, "acmd service install", "runs %s, not this acmd", setup.Tilde(s.Binary, e.Home))
	case !s.Running:
		r.add(secSvc, Warn, "acmd daemon restart", "installed, but not running")
	case s.Linger != nil && !*s.Linger:
		r.add(secSvc, Warn, "sudo loginctl enable-linger "+e.Getenv("USER"),
			"running (pid %d), but it starts at login, not at boot: lingering is off", s.Pid)
	default:
		r.add(secSvc, OK, "", "running (pid %d), from %s (%s)", s.Pid, startsAt(s), s.Manager)
	}
}

func startsAt(s service.Status) string {
	if s.Manager == service.Systemd {
		return "boot"
	}
	return "login"
}

// firstSentence is s up to its first full stop.
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
}

// checkRemoteHosts warns about Remote hosts that can't be used until someone
// acts: one whose fingerprint doesn't match, one that refused this machine,
// and one of another protocol version. (Unreachable and pending ones may
// sort themselves out.)
func checkRemoteHosts(r *Report, hosts []ipc.RemoteHost) {
	for _, h := range hosts {
		switch h.State {
		case ipc.HostFingerprint:
			r.add(secRemote, Warn, "if its identity changed on purpose: acmd remote edit "+h.Name+" --fingerprint "+h.Seen,
				"Remote host %s: fingerprint mismatch (it shows %s, you pinned %s)", h.Name, h.Seen, h.Fingerprint)
		case ipc.HostUnauthorized:
			r.add(secRemote, Warn, "on "+h.Name+": acmd remote authorize "+h.OurFingerprint,
				"Remote host %s refused this machine", h.Name)
		case ipc.HostVersion:
			r.add(secRemote, Warn, "update acmd on both machines to the same version",
				"Remote host %s speaks another protocol version: %s", h.Name, h.Error)
		}
	}
}

// checkIdentity checks the identities for Remote sessions: the generated one
// once there is one, or the certs config.toml names for Hosting
// ([remote.host]) and for connecting to hosts ([remote.client]). Others must
// not be able to read a key, and each cert and key must belong together. When
// the two roles use different identities, each is checked under its name.
func checkIdentity(r *Report, e Env) {
	c, err := config.Load(e.Paths.Config())
	if err != nil {
		c = config.Default() // checkAcmd reports the file
	}
	host := roleFiles{"[remote.host]", c.Remote.Host.Cert, c.Remote.Host.Key}
	client := roleFiles{"[remote.client]", c.Remote.Client.Cert, c.Remote.Client.Key}
	if host.cert == client.cert && host.key == client.key {
		checkRole(r, e, "identity", host)
		return
	}
	checkRole(r, e, "Hosting identity", host)
	checkRole(r, e, "client identity", client)
}

// roleFiles are the cert and key a role uses, as config.toml names them (""
// for the generated identity).
type roleFiles struct{ section, cert, key string }

func checkRole(r *Report, e Env, what string, f roleFiles) {
	fix := "acmd remote identity --regenerate (peers must then trust the new fingerprint)"
	cert, key := f.cert, f.key
	switch {
	case cert == "" && key == "":
		dir := e.Paths.RemoteIdentityDir()
		if !identity.Exists(dir) {
			return
		}
		cert, key = identity.Files(dir)
	case cert == "" || key == "":
		r.add(secRemote, Fail, "set both cert and key in "+f.section+", or neither", "%s: %s names only one of cert and key", what, f.section)
		return
	default:
		fix = "check cert and key in " + f.section + " of " + setup.Tilde(e.Paths.Config(), e.Home)
	}
	id, err := identity.Load(cert, key)
	switch {
	case errors.Is(err, identity.ErrKeyPermissions):
		keyFix := "chmod 600 " + setup.Tilde(key, e.Home)
		if e.OthersCanUse != nil {
			if _, f, _ := e.OthersCanUse(key); f != "" {
				keyFix = f
			}
		}
		r.add(secRemote, Fail, keyFix, "%s key %s is readable by others", what, setup.Tilde(key, e.Home))
	case err != nil:
		r.add(secRemote, Fail, fix, "%s unusable: %v", what, err)
	default:
		r.add(secRemote, OK, "", "%s %s (%s)", what, id.Fingerprint(), id.CommonName)
	}
}

// checkRemote warns about Remote sessions that no client can reach, because
// Hosting is off.
func checkRemote(r *Report, e Env, remote int) {
	if remote == 0 {
		return
	}
	cfg, err := config.Load(e.Paths.Config())
	if err != nil {
		return // checkAcmd reports a config that doesn't load
	}
	if !cfg.Remote.Host.Enabled {
		r.add(secRemote, Warn, "acmd remote hosting on"+
			", or make them local with acmd remote off <session>",
			"%s marked Remote, but Hosting is off: no client can reach them", plural(remote, "session"))
		return
	}
	r.add(secRemote, OK, "", "%s, Hosting on at %s", plural(remote, "Remote session"), cfg.Remote.Host.BindAddress())
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
