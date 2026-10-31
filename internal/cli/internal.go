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
	return &cobra.Command{
		Use:    "daemon",
		Short:  "Run the acmd daemon (started automatically)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			logger := log.New(cmd.ErrOrStderr(), "acmd daemon: ", log.LstdFlags)
			cfg := daemon.Config{Paths: p, Version: version.Get().Version, Log: logger}
			if claude, env, err := claudeCommand(p); err == nil {
				cli := claudecli.New(claude, env)
				cfg.Agents, cfg.Stopper, cfg.Claude = cli, cli, cli
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

// claudeCommand is the claude binary and environment for processes acmd
// starts: config's claude_command (pinned to an absolute path by `acmd
// init`) and the saved login-shell environment, falling back to PATH and
// this process's environment. CLAUDE* variables are always stripped (M0 C5).
func claudeCommand(p paths.Paths) (string, []string, error) {
	cfg, err := config.Load(p.Config())
	if err != nil {
		return "", nil, err
	}
	env := os.Environ()
	if b, err := os.ReadFile(p.Env()); err == nil {
		var saved setup.LoginEnv
		if err := json.Unmarshal(b, &saved); err == nil && saved["PATH"] != "" {
			env = saved.Environ()
		}
	}
	env = procenv.Scrub(env)
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
