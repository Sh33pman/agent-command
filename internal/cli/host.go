package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	xterm "github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/daemon"

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
	cmd.AddCommand(newHostServeCmd(), newHostAttachCmd())
	return cmd
}

// hostOptions say what a host runs.
type hostOptions struct {
	ID, Dir, Name, Prompt, Resume, ClaudeSession string
	Args                                         []string
}

// hostArgv builds the harness argv for a host.
func hostArgv(h harness.Harness, o hostOptions) []string {
	if o.Resume != "" {
		return h.Resume(o.Resume, o.Args)
	}
	return h.Command(harness.Spec{Dir: o.Dir, Name: o.Name, Prompt: o.Prompt, SessionID: o.ClaudeSession, Args: o.Args})
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
	f.StringVar(&o.ClaudeSession, "claude-session", "", "the Claude session id to start with (a UUID)")
	f.StringVar(&o.Resume, "resume", "", "continue this Claude session instead of starting a new one")
	return cmd
}

// execHosts starts session hosts as detached `acmd host serve` processes.
type execHosts struct{ paths paths.Paths }

// Start implements daemon.Hosts.
func (h execHosts) Start(_ context.Context, r daemon.HostRequest) error {
	exe, err := executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Clean(h.paths.DaemonLog()), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	args := []string{"host", "serve", "--session", r.ID, "--dir", r.Dir}
	if r.Name != "" {
		args = append(args, "--name", r.Name)
	}
	if r.Prompt != "" {
		args = append(args, "--prompt", r.Prompt)
	}
	if r.ClaudeSession != "" {
		args = append(args, "--claude-session", r.ClaudeSession)
	}
	if r.Resume != "" {
		args = append(args, "--resume", r.Resume)
	}
	args = append(args, "--")
	args = append(args, r.Args...)
	c := exec.Command(exe, args...) // #nosec G204 -- our own binary
	c.Env = procenv.Scrub(os.Environ())
	c.Stdout, c.Stderr = logf, logf
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}

// newHostAttachCmd shows a session's host in this terminal until it is
// detached (SIGTERM, how the view loop switches sessions) or the session's
// CLI exits. It is what the view loop runs in place of `claude attach`.
func newHostAttachCmd() *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:    "attach --session <id>",
		Short:  "Attach this terminal to a session's host (internal)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Default()
			if err != nil {
				return err
			}
			return attachHost(cmd.Context(), p.HostSocket(id))
		},
	}
	cmd.Flags().StringVar(&id, "session", "", "the acmd session")
	return cmd
}

// attachHost attaches the terminal to socket. A detach (SIGTERM) or the
// CLI exiting end it without an error; no host listening is an error.
func attachHost(parent context.Context, socket string) error {
	if !term.Running(socket) {
		return errors.New("the session's host isn't running")
	}
	ctx, cancel := signal.NotifyContext(parent, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	fd := os.Stdin.Fd()
	if state, err := xterm.MakeRaw(fd); err == nil {
		defer func() { _ = xterm.Restore(fd, state) }()
	}
	in := make(chan []byte, 16)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				in <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()
	size := func() (int, int) {
		cols, rows, err := xterm.GetSize(os.Stdout.Fd())
		if err != nil {
			return 24, 80
		}
		return rows, cols
	}
	sizes := make(chan [2]int, 1)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	go func() {
		for range winch {
			r, c := size()
			select {
			case sizes <- [2]int{r, c}:
			default:
			}
		}
	}()
	// Raw mode hands Ctrl+C/Ctrl+Z to the CLI as input; signals that still
	// arrive (sent to the process group) are ignored.
	ignored := make(chan os.Signal, 4)
	signal.Notify(ignored, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTSTP)
	defer signal.Stop(ignored)
	go func() {
		for range ignored {
		}
	}()
	rows, cols := size()
	err := term.Attach(ctx, socket, in, sizes, rows, cols, os.Stdout)
	if errors.Is(err, term.ErrEnded) {
		return nil // the session's CLI exited
	}
	return err
}
