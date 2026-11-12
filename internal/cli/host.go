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
	"time"

	xterm "github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/daemon"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/harness"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/procenv"
	"github.com/brettmostert/agent-command/internal/term"
	"github.com/brettmostert/agent-command/internal/version"
)

// Session hosts: an interactive CLI per session, run under a
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
	ID, Dir, Name, Prompt, Resume, ClaudeSession, Harness string
	Args                                                  []string
}

// hostArgv builds the harness argv for a host.
func hostArgv(h harness.Harness, o hostOptions) []string {
	if o.Resume != "" {
		argv := h.Resume(o.Resume, o.Args)
		if o.Prompt != "" && h.StatusSource() == harness.StatusHooks { // an agent, not a shell
			argv = append(argv, "--", o.Prompt)
		}
		return argv
	}
	return h.Command(harness.Spec{Dir: o.Dir, Name: o.Name, Prompt: o.Prompt, SessionID: o.ClaudeSession, Args: o.Args})
}

// hostEnv is the environment of a hosted CLI: the captured login-shell
// environment without CLAUDE* variables, and a terminal type
// of its own.
func hostEnv(base []string) []string {
	var out []string
	for _, kv := range procenv.Scrub(base) {
		switch name, _, _ := splitEnv(kv); name {
		case "TERM", "KITTY_WINDOW_ID", "KITTY_LISTEN_ON", "ACMD_VIEW_ID", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "PWD", "OLDPWD", "GHOSTTY_RESOURCES_DIR":
			continue // belong to whatever started the host, not to the session
		}
		out = append(out, kv)
	}
	return append(out, "TERM=xterm-256color")
}

// hostHarness resolves what a host runs and the environment to run it in.
func hostHarness(p paths.Paths, name string) (harness.Harness, []string, error) {
	switch name {
	case "", core.HarnessClaude:
		claude, env, err := claudeCommand(p)
		if err != nil {
			return nil, nil, fmt.Errorf("claude not found: %w", err)
		}
		// Read when the session starts, so a change applies to the next one.
		cfg, err := config.Load(p.Config())
		if err != nil {
			return nil, nil, err
		}
		return harness.Claude{Path: claude, RemoteControl: cfg.RemoteControl}, env, nil
	case core.HarnessTerminal:
		env := loginEnv(p)
		shell := "/bin/zsh"
		for _, kv := range env {
			if name, val, _ := splitEnv(kv); name == "SHELL" && val != "" {
				shell = val
			}
		}
		return harness.Terminal{Shell: shell}, env, nil
	}
	return nil, nil, fmt.Errorf("unknown harness %q", name)
}

// archiveSelf tells the daemon that this host's terminal is over. If the daemon
// can't be reached the reconciler pauses the session instead, and activating it
// starts a new shell.
func archiveSelf(p paths.Paths, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// A long-lived host can be older than the daemon, so a mismatch is fine.
	conn, err := ipc.Dial(ctx, p.Socket(), ipc.Hello{Proto: ipc.Proto, Version: version.Get().Version})
	var mismatch *ipc.ErrVersionMismatch
	if err != nil && !errors.As(err, &mismatch) {
		return
	}
	defer conn.Close()
	_ = conn.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutArchive, ID: id}, nil)
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
			h, env, err := hostHarness(p, o.Harness)
			if err != nil {
				return err
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
			env = append(hostEnv(env), "ACMD_SESSION_ID="+o.ID)
			srv := &term.Server{Socket: p.HostSocket(o.ID), Shell: hostArgv(h, o), Dir: o.Dir, Env: env}
			err = srv.Serve(ctx)
			// A terminal whose shell exited by itself is over. One ended by a
			// signal isn't: after a reboot it comes back as a new shell.
			if err == nil && o.Harness == core.HarnessTerminal && ctx.Err() == nil {
				archiveSelf(p, o.ID)
			}
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.ID, "session", "", "the acmd session")
	f.StringVar(&o.Harness, "harness", "", "what the session runs: claude (default) or terminal")
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
	if r.Harness != "" {
		args = append(args, "--harness", r.Harness)
	}
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
			return attachOrLeave(cmd.Context(), p.HostSocket(id), hostWait)
		},
	}
	cmd.Flags().StringVar(&id, "session", "", "the acmd session")
	return cmd
}

// hostWait is how long an attach waits for a host: one starting takes a moment.
const hostWait = 2 * time.Second

// errNoHost means no host is listening.
var errNoHost = errors.New("the session's host isn't running")

// attachOrLeave attaches once a host is listening. With none, it leaves without an
// error, which closes the pane's terminal, so the pane can offer to resume instead
// of showing a failure.
func attachOrLeave(ctx context.Context, socket string, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for !term.Running(socket) {
		if time.Now().After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err := attachHost(ctx, socket); !errors.Is(err, errNoHost) {
		return err
	}
	return nil
}

// attachHost attaches the terminal to socket. A detach (SIGTERM) or the
// CLI exiting end it without an error; no host listening is an error.
func attachHost(parent context.Context, socket string) error {
	if !term.Running(socket) {
		return errNoHost
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

func splitEnv(kv string) (string, string, bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	return kv, "", false
}
