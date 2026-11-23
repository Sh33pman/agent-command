package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/daemon"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/proc"
	"github.com/brettmostert/agent-command/internal/service"
	"github.com/brettmostert/agent-command/internal/setup"
	"github.com/brettmostert/agent-command/internal/term"
	"github.com/brettmostert/agent-command/internal/version"
	"github.com/brettmostert/agent-command/internal/wake"
)

// Setup commands: init, uninstall, editor, and the automatic
// first run. Anything that looks outside acmd's own files is behind a
// variable so tests never touch the real machine.
var (
	invokedAs       = os.Args[0]
	executable      = os.Executable
	captureLoginEnv = setup.CaptureLoginEnv
	hasBundle       = mdfindBundle
)

type initOptions struct {
	dryRun     bool
	ask        bool // show the plan and ask before applying (automatic first run)
	refreshEnv bool
	noWake     bool // don't offer waking the Mac afterwards (nobody's there to answer)
	inDaemon   bool // the init op, which runs in the daemon
}

// gather collects everything planning needs.
type gathered struct {
	in    setup.Input
	paths paths.Paths
	cfg   config.Config
	path  string // PATH used to find claude and editors
	rec   setup.Record
	init  bool // setup.json exists
}

func gather(ctx context.Context, captureEnv bool) (*gathered, error) {
	p, err := paths.Default()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(p.Config())
	if err != nil {
		return nil, err
	}
	g := &gathered{paths: p, cfg: cfg}
	if g.rec, g.init, err = setup.ReadRecord(p.Setup()); err != nil {
		return nil, err
	}

	exe, _ := executable()
	if clientOnly {
		// No agents run here, so nothing needs their hooks or a login shell's
		// environment.
		g.in = setup.Input{ClientOnly: true, Home: home, ConfigFile: p.Config(), EnvFile: p.Env(),
			AcmdPath: setup.StableBinary(invokedAs, exe, exec.LookPath), Read: os.ReadFile}
		return g, nil
	}

	var loginEnv setup.LoginEnv
	if b, err := os.ReadFile(p.Env()); err == nil && !captureEnv {
		if json.Unmarshal(b, &loginEnv) != nil {
			captureEnv = true // unreadable: capture again
		}
	} else {
		captureEnv = true
	}
	if captureEnv {
		if loginEnv, err = captureLoginEnv(ctx, os.Getenv("SHELL")); err != nil {
			return nil, err
		}
	}
	g.path = loginEnv["PATH"]
	if g.path == "" {
		g.path = os.Getenv("PATH")
	}

	g.in = setup.Input{
		Home:        home,
		ConfigFile:  p.Config(),
		EnvFile:     p.Env(),
		AcmdPath:    setup.StableBinary(invokedAs, exe, exec.LookPath),
		ClaudeDir:   setup.ClaudeConfigDir(home, os.Getenv),
		HarnessDirs: harnessFolders(ctx, p, home),
		Read:        os.ReadFile,

		AntigravityDir: setup.AntigravityConfigDir(home),
	}
	if captureEnv {
		g.in.LoginEnv = loginEnv
	}
	if claude, err := setup.LookPathIn("claude", g.path); err == nil {
		g.in.ClaudePath = claude
	}
	if agy, err := setup.LookPathIn("agy", g.path); err == nil {
		g.in.AgyPath = agy
	}

	return g, nil
}

// mdfindBundle reports whether Spotlight knows an app with the bundle id.
// The ids are constants from setup.EditorPresets.
func mdfindBundle(id string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "mdfind", "kMDItemCFBundleIdentifier == '"+id+"'").Output() // #nosec G204 -- constant bundle ids
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// runInit plans and (unless dry-run or declined) applies `acmd init`. It reports
// whether setup is in place afterwards.
func runInit(ctx context.Context, in io.Reader, out io.Writer, o initOptions) (bool, error) {
	g, err := gather(ctx, o.refreshEnv)
	if err != nil {
		return false, err
	}
	plan, err := setup.PlanInit(g.in)
	if err != nil {
		return false, err
	}
	if o.dryRun {
		fmt.Fprintln(out, "acmd init would make these changes (dry run: nothing is changed):")
	} else {
		fmt.Fprintln(out, "acmd init:")
	}
	plan.Write(out, g.in.Home)
	svcPending := writeServicePlan(out, g.in.AcmdPath, g.in.Home)
	if o.dryRun {
		return g.init, nil
	}
	if o.ask && (len(plan.Pending()) > 0 || svcPending) {
		fmt.Fprint(out, "\nSet up acmd now? [Y/n] ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "" && a != "y" && a != "yes" {
			fmt.Fprintln(out, "Skipped; nothing was changed. Run `acmd init` any time.")
			return false, nil
		}
	}
	if err := g.paths.Ensure(); err != nil {
		return false, err
	}
	// A running daemon is config.toml's only writer: it makes the change.
	settings := plan.TakeSettings(daemonRunning(ctx, g.paths))
	backups, err := plan.Apply(g.paths.Backups(), g.in.Home, time.Now())
	if err != nil {
		return false, err
	}
	if len(settings) > 0 {
		if _, err := setSettings(ctx, settings); err != nil {
			return false, err
		}
	}
	rec := setup.Record{
		Version: version.Get().Version, At: time.Now().UTC(), AcmdPath: g.in.AcmdPath,
		ClaudeDir: g.in.ClaudeDir, ClaudeDirs: g.in.HarnessDirs, ConfigFile: g.in.ConfigFile,
	}
	if err := setup.WriteRecord(g.paths.Setup(), rec); err != nil {
		return false, err
	}
	// Last: the service's daemon reads what init just wrote.
	notes, svcErr := installService(ctx, g.paths, g.in.AcmdPath, serviceOptions{inDaemon: o.inDaemon})
	if len(plan.Pending()) == 0 && !svcPending {
		fmt.Fprintln(out, "\nAlready set up; nothing changed.")
	} else {
		fmt.Fprintln(out, "\nDone.")
	}
	if backups != "" {
		fmt.Fprintf(out, "Originals backed up to %s\n", setup.Tilde(backups, g.in.Home))
	}
	for _, a := range plan.After {
		fmt.Fprintln(out, a)
	}
	for _, n := range notes {
		fmt.Fprintln(out, n)
	}
	if svcErr != nil {
		fmt.Fprintf(out, "Couldn't start the service (%v). The daemon starts when something needs it; see `acmd service status`.\n", svcErr)
	}
	if !o.noWake {
		if err := offerWake(ctx, in, out); err != nil {
			return true, err
		}
	}
	return true, nil
}

func newInitCmd() *cobra.Command {
	var o initOptions
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Set up Claude Code hooks and acmd's config (safe to rerun)",
		Long: "Registers acmd's Claude Code hooks and saves your login-shell environment. Every changed file is backed\n" +
			"up first, and `acmd uninstall` removes the hooks again. Use --dry-run to see the changes without making them.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := runInit(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), o)
			return err
		},
	}
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "show the changes without making them")
	cmd.Flags().BoolVar(&o.refreshEnv, "refresh-env", false, "capture the login-shell environment again")
	return cmd
}

func newUninstallCmd() *cobra.Command {
	var dryRun, yes, keepData bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "End running sessions, stop the daemon and remove acmd's hooks",
		Long: "Ends running sessions, stops the daemon, removes its service and acmd's hook entries, after backing the\n" +
			"file up. Run it before `brew uninstall acmd`. Claude conversations can still be resumed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUninstall(cmd, dryRun, yes, keepData)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the changes without making them")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	cmd.Flags().BoolVar(&keepData, "keep-data", true, "keep sessions, groups, settings and logs (--keep-data=false deletes them)")
	return cmd
}

// useUserTempDir sets TMPDIR to this user's own temp folder, where the daemon's
// and hosts' sockets live, for a daemon whose launcher gave it another or none.
func useUserTempDir() {
	if runtime.GOOS != "darwin" {
		return
	}
	out, err := exec.Command("/usr/bin/getconf", "DARWIN_USER_TEMP_DIR").Output()
	if dir := strings.TrimSpace(string(out)); err == nil && dir != "" {
		_ = os.Setenv("TMPDIR", dir)
	}
}

// endSessions ends the running hosts and reports how many. The daemon must have
// stopped first: then a terminal's host can't archive itself, so with the data
// kept it comes back as a new shell, as it does after a reboot.
func endSessions(p paths.Paths) int {
	sockets, err := filepath.Glob(filepath.Join(p.RuntimeDir, "h-*.sock"))
	if err != nil {
		return 0
	}
	ended := 0
	for _, socket := range sockets {
		if killHost(socket) == nil {
			ended++
		}
	}
	return ended
}

// killHost is swappable in tests.
var killHost = term.Kill

func runUninstall(cmd *cobra.Command, dryRun, yes, keepData bool) error {
	out := cmd.OutOrStdout()
	p, err := paths.Default()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	rec, ok, err := setup.ReadRecord(p.Setup())
	if err != nil {
		return err
	}
	in := setup.Input{Home: home, ClaudeDir: setup.ClaudeConfigDir(home, os.Getenv), Read: os.ReadFile,
		AntigravityDir: setup.AntigravityConfigDir(home)}
	if ok {
		in.ClaudeDir, in.HarnessDirs = rec.ClaudeDir, rec.ClaudeDirs
	}
	plan, err := setup.PlanUninstall(in)
	if err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintln(out, "acmd uninstall would make these changes (dry run: nothing is changed):")
	} else {
		fmt.Fprintln(out, "acmd uninstall:")
	}
	plan.Write(out, home)
	svc, svcInstalled := installedService()
	if svcInstalled {
		fmt.Fprintf(out, "  remove    %s\n            the service that runs the daemon\n", setup.Tilde(svc.File(), home))
	}
	fmt.Fprintln(out, "  stop      the acmd daemon, and end running sessions (Claude conversations can be resumed)")
	wakeRule := ruleInstalled()
	if wakeRule {
		fmt.Fprintf(out, "  remove    %s, and acmd's Mac wakes (asks for your password)\n", wake.RulePath)
	}
	if !keepData {
		fmt.Fprintf(out, "  delete    %s and %s (backups are kept)\n", setup.Tilde(p.StateDir, home), setup.Tilde(p.ConfigDir, home))
	}
	if dryRun {
		return nil
	}
	if err := confirm(cmd.InOrStdin(), out, yes, "\nUninstall?"); err != nil {
		return err
	}
	backups, err := plan.Apply(p.Backups(), home, time.Now())
	if err != nil {
		return err
	}
	// Before ending sessions: without this, their last hooks restart the daemon.
	if err := os.Remove(p.Setup()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Before stopping the daemon: the service would start it again.
	if svcInstalled {
		if _, err := svc.Uninstall(cmd.Context()); err != nil {
			fmt.Fprintf(out, "Couldn't remove the service: %v\n", err)
		}
	}
	stopDaemon(cmd.Context(), p)
	switch ended := endSessions(p); ended {
	case 0:
	case 1:
		fmt.Fprintln(out, "Ended 1 running session.")
	default:
		fmt.Fprintf(out, "Ended %d running sessions.\n", ended)
	}
	if wakeRule {
		if err := removeWakeRule(cmd.Context(), cmd.InOrStdin(), out); err != nil {
			fmt.Fprintf(out, "Couldn't remove %s (%v). Remove it with: sudo rm %s\n", wake.RulePath, err, wake.RulePath)
		}
	}
	// Last: the daemon and hosts are using these, and a lock removed under a
	// running daemon would let a second one start.
	if err := p.RemoveRuntime(); err != nil {
		fmt.Fprintf(out, "Couldn't remove %s: %v\n", setup.Tilde(p.RuntimeDir, home), err)
	}
	if !keepData {
		if err := deleteData(p); err != nil {
			return err
		}
	}
	fmt.Fprintln(out, "\nDone.")
	if backups != "" {
		fmt.Fprintf(out, "Originals backed up to %s\n", setup.Tilde(backups, home))
	}
	for _, a := range plan.After {
		fmt.Fprintln(out, a)
	}
	return nil
}

// stopDaemon asks a running daemon (of any version) to exit and waits up
// to 3s for it to go, then signals it if it hasn't; it never starts one. It
// reports whether one was running.
func stopDaemon(ctx context.Context, p paths.Paths) bool {
	me := ipc.HelloFrom(version.Get().Version)
	dial := func() (*ipc.Conn, bool) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		conn, err := ipc.Dial(ctx, p.Socket(), me)
		var mismatch *ipc.ErrVersionMismatch
		return conn, err == nil || errors.As(err, &mismatch)
	}
	conn, up := dial()
	if !up {
		return false
	}
	pid := conn.Daemon.Pid
	_ = conn.Call(ipc.OpShutdown, nil, nil)
	_ = conn.Close()
	waitDaemonGone(ctx, p)
	if daemon.Running(p) && pid > 0 {
		_ = proc.Terminate(pid)
		waitDaemonGone(ctx, p)
	}
	return true
}

// waitDaemonGone waits up to 3s for a stopping daemon to release its lock,
// so a daemon started next doesn't see it and exit as a duplicate.
func waitDaemonGone(ctx context.Context, p paths.Paths) {
	for deadline := time.Now().Add(3 * time.Second); daemon.Running(p) && time.Now().Before(deadline) && ctx.Err() == nil; {
		time.Sleep(25 * time.Millisecond)
	}
}

// deleteData removes acmd's state (except config backups) and config.
func deleteData(p paths.Paths) error {
	entries, err := os.ReadDir(p.StateDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		if filepath.Join(p.StateDir, e.Name()) == p.Backups() {
			continue
		}
		if err := os.RemoveAll(filepath.Join(p.StateDir, e.Name())); err != nil {
			return err
		}
	}
	return os.RemoveAll(p.ConfigDir)
}

// isInteractive reports whether both stdin and stdout are terminals.
func isInteractive(cmd *cobra.Command) bool {
	in, ok := cmd.InOrStdin().(*os.File)
	if !ok || !isTerminal(in) {
		return false
	}
	out, ok := cmd.OutOrStdout().(*os.File)
	return ok && isTerminal(out)
}

// needsSetup marks commands that trigger the automatic first run.
const needsSetup = "acmd/needs-setup"

// autoInit runs before user-facing commands: on first use it shows what
// `acmd init` would change and asks (decision: show the plan, ask Y/n).
// Without a terminal it only prints a hint.
func autoInit(cmd *cobra.Command) error {
	marked := false
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations[needsSetup] != "" {
			marked = true
		}
	}
	if !marked || os.Getenv("ACMD_NO_AUTO_INIT") != "" {
		return nil
	}
	p, err := paths.Default()
	if err != nil {
		return nil //nolint:nilerr // the command itself reports path problems
	}
	if _, ok, _ := setup.ReadRecord(p.Setup()); ok {
		return nil
	}
	if !isInteractive(cmd) {
		fmt.Fprintln(cmd.ErrOrStderr(), "acmd isn't set up yet: run `acmd init` (or `acmd init --dry-run` to preview the changes).")
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), "acmd isn't set up yet. This is what setup changes:")
	_, err = runInit(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), initOptions{ask: true})
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "setup failed:", err)
	}
	fmt.Fprintln(cmd.OutOrStdout())
	return nil
}

// daemonRunning says whether a daemon answers on p's socket, without starting
// one.
func daemonRunning(parent context.Context, p paths.Paths) bool {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	conn, err := ipc.Dial(ctx, p.Socket(), ipc.HelloFrom(version.Get().Version))
	if conn != nil {
		_ = conn.Close()
	}
	return err == nil
}

// writeServicePlan prints what init does to the service, as the plan prints
// files, and says whether that changes anything.
func writeServicePlan(out io.Writer, binary, home string) bool {
	svc, err := localService()
	if err != nil {
		return false
	}
	mgr, err := svc.Manager()
	if err != nil {
		fmt.Fprintf(out, "  %-9s the service: %s\n", "skip", firstSentence(unsupportedText(err)))
		return false
	}
	action := "create"
	if old, err := svc.ReadFile(svc.File()); err == nil {
		action = "modify"
		if string(old) == string(svc.Definition(binary)) {
			action = "unchanged"
		}
	}
	when := "from login"
	if mgr == service.Systemd {
		when = "from boot (with lingering)"
	}
	fmt.Fprintf(out, "  %-9s %s\n            the service, which runs the daemon %s (%s)\n", action, setup.Tilde(svc.File(), home), when, mgr)
	return action != "unchanged"
}

// firstSentence is s up to its first full stop.
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
}
