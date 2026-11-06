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
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/claudecli"
	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/daemon"
	"github.com/brettmostert/agent-command/internal/hook"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/procenv"
	"github.com/brettmostert/agent-command/internal/setup"
	"github.com/brettmostert/agent-command/internal/version"
)

// Internal commands, run by acmd itself and by Claude Code hooks; hidden
// from help (SPEC §8.7).

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Stop or restart the background service (it starts automatically)",
		Long: `acmd's background service tracks your sessions. It starts by itself
when needed; ` + "`acmd daemon`" + ` on its own runs it in the foreground.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			logger := log.New(cmd.ErrOrStderr(), "acmd daemon: ", log.LstdFlags)
			cfg := daemon.Config{Paths: p, Version: version.Get().Version, Log: logger, Fallback: fallbackNotify}
			if home, err := os.UserHomeDir(); err == nil {
				cfg.ClaudeDir = claudecli.ConfigDir(os.Getenv, home)
			}
			if c, err := config.Load(p.Config()); err == nil {
				cfg.Notify = &c.Notify
			} else {
				logger.Printf("config: %v (notifications off)", err)
			}
			if claude, env, err := claudeCommand(p); err == nil {
				cli := claudecli.New(claude, env)
				cfg.Agents, cfg.Stopper, cfg.Remover = cli, cli, cli
				cfg.Hosts = execHosts{paths: p}
			} else {
				logger.Printf("claude not found; reconciliation disabled: %v", err)
			}
			err = daemon.Run(ctx, cfg)
			if errors.Is(err, daemon.ErrAlreadyRunning) {
				return nil // another daemon won the race; fine
			}
			return err
		},
	}
	cmd.AddCommand(newDaemonStopCmd(), newDaemonRestartCmd())
	return cmd
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the background service",
		Long: `Stop the background service. Open views and sidebars start it again
the next time they need it; close them first to keep it stopped.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			if stopDaemon(cmd.Context(), p) {
				fmt.Fprintln(cmd.OutOrStdout(), "Stopped the background service.")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "The background service wasn't running.")
			}
			return nil
		},
	}
}

func newDaemonRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the background service (open views reconnect by themselves)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			was := stopDaemon(cmd.Context(), p)
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			c, err := connect(ctx)
			if err != nil {
				return err
			}
			c.close()
			verb := "Restarted"
			if !was {
				verb = "Started"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s the background service (%s).\n", verb, version.Get().Version)
			return nil
		},
	}
}

func newHookCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "hook <Event>",
		Short:  "Handle a Claude Code hook event (called by Claude Code)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		// Always succeeds: a hook must never block or break Claude (SPEC §5.3).
		RunE: func(cmd *cobra.Command, args []string) error {
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
// environment when there is one, without CLAUDE* variables (SPEC §4.4).
func loginEnv(p paths.Paths) []string {
	env := os.Environ()
	if b, err := os.ReadFile(p.Env()); err == nil {
		var saved setup.LoginEnv
		if err := json.Unmarshal(b, &saved); err == nil && saved["PATH"] != "" {
			env = saved.Environ()
		}
	}
	return procenv.Scrub(env)
}

// claudeCommand is the claude binary and environment for processes acmd
// starts: config's claude_command (pinned to an absolute path by `acmd
// init`) and the saved login-shell environment, falling back to PATH and
// this process's environment. CLAUDE* variables are always stripped (M0 C5).
func claudeCommand(p paths.Paths) (string, []string, error) {
	cfg, err := config.Load(p.Config())
	if err != nil {
		return "", nil, err
	}
	env := loginEnv(p)
	claude := cfg.ClaudeCommand
	if !filepath.IsAbs(claude) {
		pathList := ""
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, "PATH="); ok {
				pathList = v
			}
		}
		if claude, err = setup.LookPathIn(claude, pathList); err != nil {
			if claude, err = exec.LookPath(cfg.ClaudeCommand); err != nil {
				return "", nil, err
			}
		}
	}
	return claude, env, nil
}
