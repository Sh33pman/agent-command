package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/harness"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/procenv"
	"github.com/brettmostert/agent-command/internal/term"
)

// Session hosts (SPEC §4.5): an interactive CLI per session, run under a
// term.Server in its own detached process (`acmd host serve`), so it
// outlives the view, the terminal and the daemon. Views attach to the
// host's socket (HostSocket).

func newHostCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "host",
		Short:  "Session hosts (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
	}
	cmd.AddCommand(newHostServeCmd())
	return cmd
}

// hostOptions say what a host runs.
type hostOptions struct {
	ID, Dir, Name, Prompt, Resume string
	Args                          []string
}

// hostArgv builds the harness argv for a host.
func hostArgv(h harness.Harness, o hostOptions) []string {
	if o.Resume != "" {
		return h.Resume(o.Resume, o.Args)
	}
	return h.Command(o.Dir, o.Name, o.Prompt, o.Args)
}

// hostEnv is the environment of a hosted CLI: the captured login-shell
// environment without CLAUDE* variables (SPEC §4.4), and a terminal type
// of its own.
func hostEnv(base []string) []string {
	var out []string
	for _, kv := range procenv.Scrub(base) {
		switch name, _, _ := splitEnv(kv); name {
		case "TERM", "KITTY_WINDOW_ID", "KITTY_LISTEN_ON", "ACMD_VIEW_ID", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "PWD", "OLDPWD":
			continue // belong to whatever started the host, not to the session
		}
		out = append(out, kv)
	}
	return append(out, "TERM=xterm-256color")
}

func newHostServeCmd() *cobra.Command {
	var o hostOptions
	cmd := &cobra.Command{
		Use:    "serve --session <id> --dir <folder> [--name N] [--prompt P | --resume <claude session id>] [-- claude args]",
		Short:  "Run a session's CLI under a PTY host until it exits",
		Hidden: true,
		RunE: func(_ *cobra.Command, args []string) error {
			if o.ID == "" || o.Dir == "" {
				return fmt.Errorf("--session and --dir are required")
			}
			o.Args = args
			p, err := paths.Default()
			if err != nil {
				return err
			}
			claude, env, err := claudeCommand(p)
			if err != nil {
				return fmt.Errorf("claude not found: %w", err)
			}
			if st, err := os.Stat(o.Dir); err != nil || !st.IsDir() {
				return fmt.Errorf("folder %s is gone", o.Dir)
			}
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
			defer stop()
			if err := os.WriteFile(p.HostIDFile(o.ID), []byte(o.ID+"\n"), 0o600); err != nil {
				return err
			}
			defer os.Remove(p.HostIDFile(o.ID))
			srv := &term.Server{Socket: p.HostSocket(o.ID), Shell: hostArgv(harness.Claude{Path: claude}, o), Dir: o.Dir, Env: hostEnv(env)}
			return srv.Serve(ctx)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.ID, "session", "", "the acmd session")
	f.StringVar(&o.Dir, "dir", "", "the folder to run in")
	f.StringVar(&o.Name, "name", "", "the session's name")
	f.StringVar(&o.Prompt, "prompt", "", "the first prompt")
	f.StringVar(&o.Resume, "resume", "", "continue this Claude session instead of starting a new one")
	return cmd
}
