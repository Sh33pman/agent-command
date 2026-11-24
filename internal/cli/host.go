package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"syscall"
	"time"

	xterm "github.com/charmbracelet/x/term"
	"github.com/muesli/cancelreader"
	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/daemon"
	"github.com/brettmostert/agent-command/internal/harness"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/proc"
	"github.com/brettmostert/agent-command/internal/procenv"
	"github.com/brettmostert/agent-command/internal/setup"
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
	// Command and ConfigDir are the harness's program and, for Claude, its
	// CLAUDE_CONFIG_DIR ("" for Claude's standard folder).
	Command, ConfigDir string
	Args               []string
}

// hostArgv builds the harness argv for a host.
func hostArgv(h harness.Harness, o hostOptions) []string {
	if o.Resume != "" {
		argv := h.Resume(o.Resume, o.Args)
		if p, ok := h.(harness.Prompter); ok && o.Prompt != "" { // an agent, not a shell
			argv = append(argv, p.PromptArgs(o.Prompt)...)
		}
		return argv
	}
	return h.Command(harness.Spec{Dir: o.Dir, Name: o.Name, Prompt: o.Prompt, SessionID: o.ClaudeSession, Args: o.Args})
}

// hostEnv is the environment of a hosted CLI: the captured login-shell
// environment without CLAUDE* variables, a terminal type of its own, and
// for a Claude harness with its own folder, its CLAUDE_CONFIG_DIR (set after
// the scrub, which would remove it).
func hostEnv(base []string, o hostOptions) []string {
	var out []string
	for _, kv := range procenv.Scrub(base) {
		switch name, _, _ := splitEnv(kv); name {
		case "TERM", "KITTY_WINDOW_ID", "KITTY_LISTEN_ON", "ACMD_VIEW_ID", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "PWD", "OLDPWD", "GHOSTTY_RESOURCES_DIR":
			continue // belong to whatever started the host, not to the session
		}
		out = append(out, kv)
	}
	out = append(out, "TERM=xterm-256color")
	if o.Harness == "" || o.Harness == core.HarnessClaude {
		home, _ := os.UserHomeDir()
		out = core.Harness{Kind: core.HarnessClaude, ConfigDir: o.ConfigDir}.Env(out, home)
	}
	return out
}

// hostHarness resolves what a host runs and the environment to run it in:
// o.Harness is the kind, with its harness's program and folder.
func hostHarness(p paths.Paths, o hostOptions) (harness.Harness, []string, error) {
	h := core.Harness{Kind: o.Harness, Command: o.Command, ConfigDir: o.ConfigDir}
	switch o.Harness {
	case "", core.HarnessClaude:
		h.Kind = core.HarnessClaude
		claude, env, err := harnessCommand(p, h)
		if err != nil {
			return nil, nil, err
		}
		// Read when the session starts, so a change applies to the next one.
		cfg, err := config.Load(p.Config())
		if err != nil {
			return nil, nil, err
		}
		return harness.Claude{Path: claude, RemoteControl: cfg.AgentRemoteControl}, env, nil
	case core.HarnessAntigravity:
		agy, env, err := harnessCommand(p, h)
		if err != nil {
			return nil, nil, err
		}
		// Read when the session starts, so a change applies to the next one.
		cfg, err := config.Load(p.Config())
		if err != nil {
			return nil, nil, err
		}
		// agy reports working, waiting on you, done or failed over OSC 7501 when it
		// thinks the terminal supports it. The host reads it, whatever the pane
		// attached to it can show, so it is always on.
		env = append(env, "AGY_CLI_OSC7501=on")
		return harness.Antigravity{Path: agy, RemoteControl: cfg.AgentRemoteControl}, env, nil
	case core.HarnessTerminal:
		env := loginEnv(p)
		shell := setup.DefaultShell() // zsh on macOS; bash, or sh, on Linux
		for _, kv := range env {
			if name, val, _ := splitEnv(kv); name == "SHELL" && val != "" {
				shell = val
			}
		}
		return harness.Terminal{Shell: shell}, env, nil
	}
	return nil, nil, fmt.Errorf("unknown harness kind %q", o.Harness)
}

// archiveSelf tells the daemon that this host's terminal is over. If the daemon
// can't be reached the reconciler pauses the session instead, and activating it
// starts a new shell.
func archiveSelf(p paths.Paths, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// A long-lived host can be older than the daemon, so a mismatch is fine.
	conn, err := ipc.Dial(ctx, p.Socket(), ipc.HelloFrom(version.Get().Version))
	var mismatch *ipc.ErrVersionMismatch
	if err != nil && !errors.As(err, &mismatch) {
		return
	}
	defer conn.Close()
	_ = conn.Call(ipc.OpMutate, ipc.Mutation{Kind: ipc.MutArchive, Targets: ipc.Here(id)}, nil)
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
			// The harness's own arguments come first in args (the daemon puts
			// them there), then the session's.
			h, env, err := hostHarness(p, o)
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
			env = append(hostEnv(env, o), "ACMD_SESSION_ID="+o.ID)
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
	f.StringVar(&o.Harness, "harness", "", "the harness kind: claude (default), antigravity or terminal")
	f.StringVar(&o.Command, "command", "", "the harness's program (default: claude or agy)")
	f.StringVar(&o.ConfigDir, "config-dir", "", "a Claude harness's CLAUDE_CONFIG_DIR (default: Claude's standard folder)")
	f.StringVar(&o.Dir, "dir", "", "the folder to run in")
	f.StringVar(&o.Name, "name", "", "the session's name")
	f.StringVar(&o.Prompt, "prompt", "", "the first prompt")
	f.StringVar(&o.ClaudeSession, "claude-session", "", "the Claude session id to start with (a UUID)")
	f.StringVar(&o.Resume, "resume", "", "continue this conversation (Claude's session id, or agy's conversation id) instead of starting a new one")
	return cmd
}

// execHosts starts session hosts as detached `acmd host serve` processes.
type execHosts struct{ paths paths.Paths }

// Start implements daemon.Hosts.
func (h execHosts) Start(_ context.Context, r daemon.HostRequest) error {
	// The harness's program is checked here, not in the host: its errors
	// only reach the daemon's log.
	if r.Harness != nil {
		if _, _, err := harnessCommand(h.paths, *r.Harness); err != nil {
			return err
		}
	}
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
	if r.Kind != "" {
		args = append(args, "--harness", r.Kind)
	}
	sessionArgs := r.Args
	if hr := r.Harness; hr != nil {
		if hr.Command != "" {
			args = append(args, "--command", hr.Command)
		}
		if hr.Kind == core.HarnessClaude && hr.ConfigDir != "" {
			home, _ := os.UserHomeDir()
			args = append(args, "--config-dir", core.ExpandHome(hr.ConfigDir, home))
		}
		sessionArgs = append(slices.Clone(hr.Args), r.Args...)
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
	args = append(args, sessionArgs...)
	c := exec.Command(exe, args...) // #nosec G204 -- our own binary
	c.Env = procenv.Scrub(os.Environ())
	c.Stdout, c.Stderr = logf, logf
	proc.Detach(c)
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}

// newHostAttachCmd shows a session's host in this terminal until it is
// detached (SIGTERM, how the view loop switches sessions) or the session's
// CLI exits. It is what the view loop runs in place of `claude attach`.
func newHostAttachCmd() *cobra.Command {
	var id, mode string
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
			return attachOrLeave(cmd.Context(), p.HostSocket(id), mode, hostWait)
		},
	}
	cmd.Flags().StringVar(&id, "session", "", "the acmd session")
	cmd.Flags().StringVar(&mode, "mode", term.ModeStream, "how the terminal comes: stream, its own bytes (the only one now)")
	return cmd
}

// hostWait is how long an attach waits for a host: one starting takes a moment.
const hostWait = 2 * time.Second

// errNoHost means no host is listening.
var errNoHost = errors.New("the session's host isn't running")

// attachOrLeave attaches once a host is listening. With none, it leaves without an
// error, which closes the pane's terminal, so the pane can offer to resume instead
// of showing a failure.
func attachOrLeave(ctx context.Context, socket, mode string, wait time.Duration) error {
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
	if err := attachHost(ctx, socket, mode, false); !errors.Is(err, errNoHost) {
		return err
	}
	return nil
}

// detachKey is what detaches `acmd remote open|attach` from a session: Ctrl+]
// (as in telnet), which agents and shells leave alone.
const detachKey = 0x1d

// detachSeq is Ctrl+] as a terminal sends it once a program turns on its
// keyboard enhancements, as Claude Code does: the kitty keyboard protocol's
// CSI 93;<mods>u (with an optional event type), or xterm's modifyOtherKeys
// CSI 27;<mods>;93~.
var detachSeq = regexp.MustCompile(`\x1b\[(?:93(?::[0-9]*)*;([0-9]+)(?::([0-9]+))?(?:;[0-9:]*)?u|27;([0-9]+);93~)`)

// lockMods are Caps Lock and Num Lock in a key's modifiers: Ctrl+] with
// them is still Ctrl+].
const lockMods = 64 | 128

// attachReset turns off the modes a session may have turned on in this
// terminal, for what runs in it next: the alternate screen, the kitty
// keyboard flags (on both screens), modifyOtherKeys, mouse reporting, focus
// events, bracketed paste, application cursor keys and keypad, a hidden
// cursor and the text style.
const attachReset = "\x1b[<99u\x1b[?1049l\x1b[<99u\x1b[>4m" +
	"\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1015l\x1b[?1004l" +
	"\x1b[?2004l\x1b[?1l\x1b>\x1b[?25h\x1b[0m"

// attachHost attaches the terminal to socket. A detach (SIGTERM, or typing
// Ctrl+] when detach is set) or the CLI exiting end it without an error; no
// host listening is an error. Once it ends, stdin is left for whatever reads
// it next, and the session's terminal modes are reset.
func attachHost(parent context.Context, socket, mode string, detach bool) error {
	if !term.Running(socket) {
		return errNoHost
	}
	ctx, cancel := signal.NotifyContext(parent, detachSignals...)
	defer cancel()
	fd := os.Stdin.Fd()
	if state, err := xterm.MakeRaw(fd); err == nil {
		defer func() { _ = xterm.Restore(fd, state) }()
	}
	defer func() {
		if isTerminal(os.Stdout) {
			_, _ = os.Stdout.WriteString(attachReset)
		}
	}()
	in, stop := readInput(ctx, cancel, detach)
	defer stop()
	size := func() (int, int) {
		cols, rows, err := xterm.GetSize(os.Stdout.Fd())
		if err != nil {
			return 24, 80
		}
		return rows, cols
	}
	sizes := make(chan [2]int, 1)
	resized := watchResize(ctx, size)
	go func() {
		for range resized {
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
	signal.Notify(ignored, ignoredWhileAttached...)
	defer signal.Stop(ignored)
	go func() {
		for range ignored {
		}
	}()
	rows, cols := size()
	err := term.AttachMode(ctx, socket, mode, in, sizes, rows, cols, os.Stdout)
	if errors.Is(err, term.ErrEnded) {
		return nil // the session's CLI exited
	}
	return err
}

// attachStdin is what an attach reads its input from; swappable in tests.
var attachStdin cancelreader.File = os.Stdin

// readInput reads stdin for an attach until stop, calling detached (and
// ending) at Ctrl+] when detach is set. stop returns once nothing reads stdin
// any more: input typed after the attach goes to what runs next (the
// terminal UI's list), not to a reader left behind.
func readInput(ctx context.Context, detached func(), detach bool) (<-chan []byte, func()) {
	in := make(chan []byte, 16)
	r, err := cancelreader.NewReader(attachStdin)
	if err != nil {
		// Not a file it can watch (/dev/null under CI's epoll): read it as
		// is, without canceling.
		r = plainReader{attachStdin}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				b, d := beforeDetach(buf[:n], detach)
				if len(b) > 0 {
					select {
					case in <- b:
					case <-ctx.Done():
						return
					}
				}
				if d {
					detached()
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return in, func() {
		// A reader that can't be canceled (not the console) is left to end
		// by itself.
		if r.Cancel() {
			<-done
		}
		_ = r.Close()
	}
}

// plainReader reads stdin when it can't be watched for canceling.
type plainReader struct{ io.Reader }

func (plainReader) Cancel() bool { return false }
func (plainReader) Close() error { return nil }

// beforeDetach is a copy of the input up to Ctrl+], in any of its forms, and
// whether it was there (never, when detach isn't set).
func beforeDetach(b []byte, detach bool) ([]byte, bool) {
	if detach {
		at := bytes.IndexByte(b, detachKey)
		for _, m := range detachSeq.FindAllSubmatchIndex(b, -1) {
			if at >= 0 && m[0] > at {
				break
			}
			if ctrlOnly(b, m) {
				at = m[0]
				break
			}
		}
		if at >= 0 {
			return append([]byte(nil), b[:at]...), true
		}
	}
	return append([]byte(nil), b...), false
}

// ctrlOnly says whether the detachSeq match m is Ctrl+] pressed: Ctrl the
// only modifier besides the locks, and not a key release.
func ctrlOnly(b []byte, m []int) bool {
	lo, hi := m[2], m[3]
	if lo < 0 {
		lo, hi = m[6], m[7] // the modifyOtherKeys form
	}
	mods, err := strconv.Atoi(string(b[lo:hi]))
	if err != nil || (mods-1)&^lockMods != 4 {
		return false
	}
	return m[4] < 0 || string(b[m[4]:m[5]]) != "3"
}

func splitEnv(kv string) (string, string, bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	return kv, "", false
}
