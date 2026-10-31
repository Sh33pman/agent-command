package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/claudecli"
	"github.com/brettmostert/agent-command/internal/daemon"
	"github.com/brettmostert/agent-command/internal/hook"
	"github.com/brettmostert/agent-command/internal/paths"
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
			// The claude path is resolved from PATH for now; `acmd init` pins
			// an absolute path in config (M3).
			if claude, err := exec.LookPath("claude"); err == nil {
				cli := claudecli.New(claude, os.Environ())
				cfg.Agents, cfg.Stopper = cli, cli
			} else {
				logger.Printf("claude not found on PATH; reconciliation disabled: %v", err)
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
