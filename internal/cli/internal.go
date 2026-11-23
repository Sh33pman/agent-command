package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/agycli"
	"github.com/brettmostert/agent-command/internal/claudecli"
	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/daemon"
	"github.com/brettmostert/agent-command/internal/hook"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/procenv"
	"github.com/brettmostert/agent-command/internal/report"
	"github.com/brettmostert/agent-command/internal/service"
	"github.com/brettmostert/agent-command/internal/setup"
	"github.com/brettmostert/agent-command/internal/version"
	"github.com/brettmostert/agent-command/internal/wake"
)

// Internal commands, run by acmd itself and by Claude Code hooks; hidden
// from help.

func newDaemonCmd() *cobra.Command {
	var foreground bool
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Stop or restart the daemon (it starts automatically)",
		Long: `acmd's daemon tracks your sessions. The service (` + "`acmd service`" + `) runs it from boot or
login; without the service, it starts by itself when needed. ` + "`acmd daemon`" + ` on its own starts
the service, or without one runs the daemon in the foreground.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if os.Getenv(service.EnvVar) != "" {
				startedByService = true
				// Sessions mustn't inherit it: an acmd daemon run in one isn't the service's.
				_ = os.Unsetenv(service.EnvVar)
				// launchd may give its agents no TMPDIR, where the sockets live.
				useUserTempDir()
			} else if svc, ok := installedService(); ok && !foreground {
				// Something wants the daemon: the app, the Linux UI, a hook or a command.
				// With the service installed, it's the service's to run.
				err := svc.Start(cmd.Context())
				if err == nil {
					fmt.Fprintln(cmd.ErrOrStderr(), "acmd daemon: started the service, which runs the daemon")
					return nil
				}
				// Better a daemon outside the service than none (launchd has no login
				// session to start it in, over SSH).
				fmt.Fprintf(cmd.ErrOrStderr(), "acmd daemon: couldn't start the service (%v); running the daemon here\n", err)
			}
			p, err := paths.Default()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			ctx, end := context.WithCancel(ctx)
			defer end()
			endDaemon.Lock()
			endDaemon.fn = end
			endDaemon.Unlock()
			if err := logDaemonOutput(ctx, p.DaemonLog()); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "acmd daemon: can't write to %s: %v\n", p.DaemonLog(), err)
			}
			logger := log.New(report.NewRedactor().Writer(cmd.ErrOrStderr()), "acmd daemon: ", log.LstdFlags)
			cfg := daemon.Config{Paths: p, Version: version.Get().Version, Log: logger, Fallback: fallbackNotify}
			// Only macOS can wake the computer for a job (pmset, through sudo). Elsewhere
			// the daemon has no waker, and schedules say that jobs don't wake it.
			if runtime.GOOS == "darwin" {
				cfg.Waker = wake.NewMac()
			}
			cfg.LockWait = 3 * time.Second
			if home, err := os.UserHomeDir(); err == nil {
				cfg.ClaudeDir = claudecli.ConfigDir(os.Getenv, home)
				cfg.AgyDir = agycli.Dir(home)
			}
			if c, err := config.Load(p.Config()); err == nil {
				cfg.Notify = &c.Notify
			} else {
				logger.Printf("config: %v (notifications off)", err)
			}
			cfg.ConfigFile = p.Config()
			cfg.Init = daemonInit
			cfg.Doctor = daemonDoctor
			if clientOnly {
				// Windows: it connects to hosts and runs no sessions of its own.
				cfg.ClientOnly = true
			} else {
				localSessions(&cfg, p, logger)
			}
			if startedByService {
				followUpgrades(ctx, end, logger)
			}
			err = daemon.Run(ctx, cfg)
			if errors.Is(err, daemon.ErrAlreadyRunning) {
				return nil // another daemon won the race; fine
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&foreground, "foreground", false, "run the daemon here even when the service is installed (stop the service first)")
	cmd.AddCommand(newDaemonStopCmd(), newDaemonRestartCmd())
	return cmd
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon",
		Long: `Stop the daemon, through the service when it's installed. Sessions keep
running. The app, a hook or a command starts it again the next time it's
needed; close the app first to keep it stopped.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			was := false
			if svc, ok := installedService(); ok {
				was = svc.Running(cmd.Context())
				if err := svc.Stop(cmd.Context()); err != nil {
					return err
				}
			}
			// A daemon outside the service too, if one runs.
			if stopDaemon(cmd.Context(), p) {
				was = true
			}
			if was {
				fmt.Fprintln(cmd.OutOrStdout(), "Stopped the daemon.")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "The daemon wasn't running.")
			}
			return nil
		},
	}
}

func newDaemonRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon (sessions keep running, and the app reconnects by itself)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			if svc, ok := installedService(); ok {
				was := svc.Running(ctx)
				// One outside the service would keep the service's from starting.
				if outsidePid(ctx, p, svc) > 0 {
					was = true
					stopDaemon(ctx, p)
				}
				conn, err := restartService(ctx, p, svc)
				if err != nil {
					return err
				}
				_ = conn.Close()
				fmt.Fprintf(cmd.OutOrStdout(), "%s the daemon through the service (%s).\n", startedVerb(was), version.Get().Version)
				return nil
			}
			was := stopDaemon(cmd.Context(), p)
			c, err := connect(ctx)
			if err != nil {
				return err
			}
			c.close()
			fmt.Fprintf(cmd.OutOrStdout(), "%s the daemon (%s).\n", startedVerb(was), version.Get().Version)
			return nil
		},
	}
}

func startedVerb(was bool) string {
	if was {
		return "Restarted"
	}
	return "Started"
}

func newHookCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "hook <Event>",
		Short:  "Handle a Claude Code or Antigravity hook event (called by the agent)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		// Always succeeds: a hook must never block or break the agent.
		RunE: func(cmd *cobra.Command, args []string) error {
			if core.IsAntigravityHook(args[0]) {
				// First, so agy has its answer whatever happens next.
				fmt.Fprint(cmd.OutOrStdout(), hook.AntigravityReply)
			}
			p, err := paths.Default()
			if err != nil {
				debugf(cmd, "paths: %v", err)
				return nil
			}
			out, err := hook.Run(args[0], hook.Options{Paths: p, Version: version.Get().Version, Stdin: cmd.InOrStdin()})
			debugf(cmd, "hook %s: %s %v", args[0], out, err)
			return nil
		},
	}
}

func debugf(cmd *cobra.Command, format string, a ...any) {
	if os.Getenv("ACMD_DEBUG") != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "acmd: "+format+"\n", a...)
	}
}

// loginEnv is the environment for what acmd starts: the captured login-shell
// environment when there is one, with this login's values for what changes each
// login (setup.LiveEnv), and without CLAUDE* variables.
func loginEnv(p paths.Paths) []string {
	env := os.Environ()
	if b, err := os.ReadFile(p.Env()); err == nil {
		var saved setup.LoginEnv
		if err := json.Unmarshal(b, &saved); err == nil && saved["PATH"] != "" {
			env = saved.WithLive(os.LookupEnv).Environ()
		}
	}
	return procenv.Scrub(env)
}

// claudeCommand is the claude binary and environment for processes acmd
// starts on Claude's standard folder: claude on the saved login shell's PATH,
// then on acmd's own. CLAUDE* variables are always stripped (M0 C5).
func claudeCommand(p paths.Paths) (string, []string, error) {
	return harnessCommand(p, core.Harness{Kind: core.HarnessClaude})
}

// errNoAgy is what starting an Antigravity session says when agy can't be found.
var errNoAgy = errors.New("Antigravity's CLI (agy) isn't installed") //nolint:staticcheck // ST1005: starts with a name

// harnessCommand is the program a harness runs, and the environment to run
// it in: its command (or its kind's, claude or agy) found on the saved login
// shell's PATH, then on acmd's own, and for a Claude harness with its own
// folder, CLAUDE_CONFIG_DIR.
func harnessCommand(p paths.Paths, h core.Harness) (string, []string, error) {
	env := loginEnv(p)
	bin, err := findCommand(h.RunCommand(), env)
	if err != nil {
		if h.Kind == core.HarnessAntigravity {
			return "", nil, errNoAgy
		}
		return "", nil, fmt.Errorf("%s not found: %w", h.RunCommand(), err)
	}
	home, _ := os.UserHomeDir()
	return bin, h.Env(env, home), nil
}

// installedHarnesses says whether a harness's program is found, for clients
// to show which can start sessions. It's asked for every snapshot, so an
// answer is kept for half a minute.
func installedHarnesses(p paths.Paths) func(core.Harness) bool {
	var mu sync.Mutex
	type found struct {
		ok bool
		at time.Time
	}
	cache := map[string]found{}
	return func(h core.Harness) bool {
		key := h.Kind + "\x00" + h.RunCommand()
		mu.Lock()
		defer mu.Unlock()
		if f, ok := cache[key]; ok && time.Since(f.at) < 30*time.Second {
			return f.ok
		}
		_, _, err := harnessCommand(p, h)
		cache[key] = found{err == nil, time.Now()}
		return err == nil
	}
}

// claudeFor is Claude's CLI for a harness's own folder, for its background
// sessions.
func claudeFor(p paths.Paths) func(core.Harness) daemon.ClaudeCLI {
	return func(h core.Harness) daemon.ClaudeCLI {
		bin, env, err := harnessCommand(p, h)
		if err != nil {
			bin = h.RunCommand() // running it says it's missing
			home, _ := os.UserHomeDir()
			env = h.Env(loginEnv(p), home)
		}
		return claudecli.New(bin, env)
	}
}

// findCommand is name's absolute path: name itself if it is one, else found on
// env's PATH, then on acmd's own.
func findCommand(name string, env []string) (string, error) {
	if filepath.IsAbs(name) {
		return name, nil
	}
	pathList := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			pathList = v
		}
	}
	if found, err := setup.LookPathIn(name, pathList); err == nil {
		return found, nil
	}
	return exec.LookPath(name)
}

// harnessHelp returns a harness's program's --help, read once and kept for
// an hour, so job settings can be checked against the installed version. ""
// when it can't run. The program is found each time it's read, so one
// installed after the daemon started counts.
func harnessHelp(p paths.Paths) func(core.Harness) string {
	var mu sync.Mutex
	type read struct {
		text string
		at   time.Time
	}
	cache := map[string]read{}
	return func(h core.Harness) string {
		key := h.Kind + "\x00" + h.RunCommand()
		mu.Lock()
		defer mu.Unlock()
		if r := cache[key]; r.text != "" && time.Since(r.at) < time.Hour {
			return r.text
		}
		bin, env, err := harnessCommand(p, h)
		if err != nil {
			return ""
		}
		text := helpText(bin, env)
		if text != "" {
			cache[key] = read{text, time.Now()}
		}
		return text
	}
}

// helpText is bin's --help. agy writes it to stderr, so both are read.
func helpText(bin string, env []string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, bin, "--help") // #nosec G204 -- the configured agent
	c.Env = env
	out, err := c.CombinedOutput()
	if err != nil {
		return ""
	}
	return string(out)
}

// localSessions wires what a daemon that runs sessions here needs: hosts for
// its sessions, the agents and their hooks, health checks, the editor, and
// claude's CLI for reconciling.
func localSessions(cfg *daemon.Config, p paths.Paths, logger *log.Logger) {
	cfg.ResolveEditor = resolveEditor
	// Sessions run Claude or agy, whichever is there: starting a host checks
	// for the one it runs.
	cfg.Hosts = execHosts{paths: p}
	cfg.Installed = installedHarnesses(p)
	cfg.ClaudeFor = claudeFor(p)
	cfg.HarnessesChanged = hooksForHarnesses
	cfg.AgentHelp = harnessHelp(p)
	cfg.CheckHealth = checkHealth
	cfg.HealthFiles = healthFiles()
	cfg.OpenEditor = func(ctx context.Context, r ipc.EditorOpen) error {
		return openInEditor(ctx, r.In, r.Path, r.Line, r.Column)
	}
	cfg.OSVersion = macOSVersion
	if claude, env, err := claudeCommand(p); err == nil {
		cli := claudecli.New(claude, env)
		cfg.Agents, cfg.Stopper, cfg.Remover = cli, cli, cli
	} else {
		logger.Printf("claude not found; reconciliation disabled: %v", err)
	}
}

// followUpgrades makes the service's daemon make way for a new build of the
// binary the service runs: it ends this daemon (end), and the service starts
// the new one. Windows' logon task has no restart rule, so there the task is
// run again as this one goes.
func followUpgrades(ctx context.Context, end func(), logger *log.Logger) {
	svc, err := localService()
	if err != nil {
		return
	}
	bin := svc.Status(ctx).Binary
	if bin == "" {
		return
	}
	w, err := newBinaryWatch(bin, version.Get().Version)
	if err != nil {
		logger.Printf("upgrades: can't watch %s: %v", bin, err)
		return
	}
	go w.follow(ctx, upgradeEvery, func(v string) {
		logger.Printf("upgrades: %s is acmd %s now: stopping, so the service starts it", bin, v)
		if err := svc.StartAfterExit(); err != nil {
			logger.Printf("upgrades: %v", err)
		}
		end()
	})
}
