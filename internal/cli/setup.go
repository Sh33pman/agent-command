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
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/daemon"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
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
	hookBinary string // the path hooks run, when it isn't this binary's yet
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

	exe, _ := executable()
	g.in = setup.Input{
		Home:       home,
		ConfigFile: p.Config(),
		EnvFile:    p.Env(),
		AcmdPath:   setup.StableBinary(invokedAs, exe, exec.LookPath),
		ClaudeDir:  setup.ClaudeConfigDir(home, os.Getenv),
		Read:       os.ReadFile,
	}
	if captureEnv {
		g.in.LoginEnv = loginEnv
	}
	if claude, err := setup.LookPathIn("claude", g.path); err == nil {
		g.in.ClaudePath = claude
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
	if o.hookBinary != "" {
		g.in.AcmdPath = o.hookBinary
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
	if o.dryRun {
		return g.init, nil
	}
	if o.ask && len(plan.Pending()) > 0 {
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
	backups, err := plan.Apply(g.paths.Backups(), g.in.Home, time.Now())
	if err != nil {
		return false, err
	}
	rec := setup.Record{
		Version: version.Get().Version, At: time.Now().UTC(), AcmdPath: g.in.AcmdPath,
		ClaudeDir: g.in.ClaudeDir, ConfigFile: g.in.ConfigFile,
	}
	if err := setup.WriteRecord(g.paths.Setup(), rec); err != nil {
		return false, err
	}
	if len(plan.Pending()) == 0 {
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
	// Homebrew's install has no terminal to ask in (it passes --hook-binary).
	if o.hookBinary == "" {
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
	cmd.Flags().StringVar(&o.hookBinary, "hook-binary", "", "the path hooks run (the cask's, before it moves the app)")
	_ = cmd.Flags().MarkHidden("hook-binary")
	return cmd
}

func newUninstallCmd() *cobra.Command {
	var dryRun, yes, keepData, fromBrew bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "End running sessions, stop the daemon and remove acmd's hooks",
		Long: "Ends running sessions, stops the daemon and removes acmd's hook entries, after backing the file up.\n" +
			"`brew uninstall --cask agent-command` does this for you. Claude conversations can still be resumed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if fromBrew {
				return runBrewUninstall(cmd)
			}
			return runUninstall(cmd, dryRun, yes, keepData)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the changes without making them")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	cmd.Flags().BoolVar(&keepData, "keep-data", true, "keep sessions, groups, settings and logs (--keep-data=false deletes them)")
	cmd.Flags().BoolVar(&fromBrew, "brew", false, "run for the cask, which also runs this during upgrades")
	_ = cmd.Flags().MarkHidden("brew")
	return cmd
}

// runBrewUninstall is what the cask runs. Homebrew runs it for upgrades and
// reinstalls too: those only replace the daemon, and leave sessions running. It
// ends sessions only when it's sure this is a real uninstall.
func runBrewUninstall(cmd *cobra.Command) error {
	useUserTempDir()
	args := brewArgs()
	switch brewCommand(args) {
	case "uninstall", "remove", "rm":
	default:
		p, err := paths.Default()
		if err != nil {
			return err
		}
		stopDaemon(cmd.Context(), p)
		return nil
	}
	keep := true
	// --zap deletes the data anyway, so there's nothing to ask.
	if !strings.Contains(args, " --zap") {
		answer, _ := askTerminal("Keep your Agent Command sessions and settings? [Y/n] ")
		keep = !strings.HasPrefix(strings.ToLower(strings.TrimSpace(answer)), "n")
	}
	return runUninstall(cmd, false, true, keep)
}

// useUserTempDir restores this user's own temp folder, which Homebrew replaces in
// TMPDIR. The daemon's and hosts' sockets live there, so without this it finds
// neither of them.
func useUserTempDir() {
	out, err := exec.Command("/usr/bin/getconf", "DARWIN_USER_TEMP_DIR").Output()
	if dir := strings.TrimSpace(string(out)); err == nil && dir != "" {
		_ = os.Setenv("TMPDIR", dir)
	}
}

// brewCommandPattern finds Homebrew's subcommand in a process's arguments.
var brewCommandPattern = regexp.MustCompile(`brew\.rb\s+(\S+)`)

// brewCommand is the Homebrew subcommand in a process's arguments ("" = none).
func brewCommand(args string) string {
	if m := brewCommandPattern.FindStringSubmatch(args); m != nil {
		return m[1]
	}
	return ""
}

// brewArgs is the arguments of the Homebrew process that started this one, found
// by walking up from it. Homebrew doesn't pass its command on any other way.
var brewArgs = func() string {
	pid := os.Getppid()
	for range 6 {
		out, err := exec.Command("ps", "-o", "ppid=,args=", "-p", strconv.Itoa(pid)).Output() // #nosec G204 -- fixed program; a pid
		if err != nil {
			return ""
		}
		line := strings.TrimSpace(string(out))
		if brewCommand(line) != "" {
			return line
		}
		ppid, _, _ := strings.Cut(line, " ")
		if pid, err = strconv.Atoi(ppid); err != nil || pid <= 1 {
			return ""
		}
	}
	return ""
}

// askTerminal asks on the terminal directly, as Homebrew gives scripts no stdin.
// ok is false without a terminal.
var askTerminal = func(question string) (answer string, ok bool) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", false
	}
	defer tty.Close()
	defer takeForeground(tty)()
	if _, err := fmt.Fprint(tty, question); err != nil {
		return "", false
	}
	line, _ := bufio.NewReader(tty).ReadString('\n')
	return line, true
}

// takeForeground makes this process group the terminal's foreground group, and
// returns what restores it. Homebrew runs scripts in a background group, where
// reading the terminal gets a process stopped by the OS.
func takeForeground(tty *os.File) (restore func()) {
	// Changing the foreground group from the background raises this.
	signal.Ignore(syscall.SIGTTOU)
	fd := int(tty.Fd()) // #nosec G115 -- a file descriptor
	previous, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	if err != nil {
		return func() {}
	}
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, unix.Getpgrp()); err != nil {
		return func() {}
	}
	return func() { _ = unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, previous) }
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
	in := setup.Input{Home: home, ClaudeDir: setup.ClaudeConfigDir(home, os.Getenv), Read: os.ReadFile}
	if ok {
		in.ClaudeDir = rec.ClaudeDir
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
	me := ipc.Hello{Proto: ipc.Proto, Version: version.Get().Version}
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
		_ = syscall.Kill(pid, syscall.SIGTERM)
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
