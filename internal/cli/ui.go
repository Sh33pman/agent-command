package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/proc"
	"github.com/brettmostert/agent-command/internal/tui"
)

// A bare `acmd` opens a UI: the native app when this machine has one and
// you're at it, else the terminal UI, or help when stdin isn't a terminal.
// --app and --terminal choose.

// Which UI a bare acmd opens.
const (
	uiApp      = "app"
	uiTerminal = "terminal"
	uiHelp     = "help"
)

// uiEnv is what choosing a UI looks at.
type uiEnv struct {
	goos        string
	terminal    bool // stdin is a terminal
	ssh         bool // over SSH
	display     bool // Linux: a graphical session (Wayland or X)
	hasApp      bool // the native app is installed here
	app, inTerm bool // --app, --terminal
}

// chooseUI picks the UI a bare acmd opens.
func chooseUI(e uiEnv) (string, error) {
	switch {
	case e.app && e.inTerm:
		return "", usageError{fmt.Errorf("--app and --terminal: pick one")}
	case e.inTerm:
		if !e.terminal {
			return "", fmt.Errorf("the terminal UI needs a terminal")
		}
		return uiTerminal, nil
	case e.app:
		if !e.hasApp {
			return "", fmt.Errorf("there's no Agent Command app on this machine: acmd --terminal opens the terminal UI")
		}
		return uiApp, nil
	case !e.terminal:
		return uiHelp, nil
	case e.ssh || !e.hasApp:
		return uiTerminal, nil
	case e.goos == "darwin", e.goos == "linux" && e.display:
		return uiApp, nil
	}
	return uiTerminal, nil
}

// localUIEnv is this machine's.
var localUIEnv = func(cmd *cobra.Command) uiEnv {
	e := uiEnv{goos: runtime.GOOS, terminal: stdinTerminal(cmd)}
	e.ssh = os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CLIENT") != ""
	e.display = os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("DISPLAY") != ""
	switch runtime.GOOS {
	case "darwin":
		e.hasApp = hasBundle(bundleID)
	case "linux":
		e.hasApp = linuxUI() != ""
	}
	return e
}

// linuxUI is the Linux UI, agent-command-ui, next to acmd or on PATH ("" if
// there's none).
func linuxUI() string {
	if exe, err := executable(); err == nil {
		if p := filepath.Join(filepath.Dir(exe), "agent-command-ui"); isExecutableFile(p) {
			return p
		}
	}
	if p, err := exec.LookPath("agent-command-ui"); err == nil {
		return p
	}
	return ""
}

// openLinuxUI starts the Linux UI, on its own.
func openLinuxUI() error {
	ui := linuxUI()
	if ui == "" {
		return fmt.Errorf("there's no Linux UI (agent-command-ui) on this machine")
	}
	cmd := exec.Command(ui) // #nosec G204 -- the Linux UI next to acmd or on PATH
	proc.Detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// runUI runs what chooseUI picked.
func runUI(cmd *cobra.Command, newInstance, app, inTerm bool) error {
	e := localUIEnv(cmd)
	e.app, e.inTerm = app, inTerm
	which, err := chooseUI(e)
	if err != nil {
		return err
	}
	switch which {
	case uiApp:
		return openApp(cmd.OutOrStdout(), newInstance)
	case uiTerminal:
		return runTerminalUI(cmd)
	}
	return cmd.Help()
}

// runTerminalUIProgram runs the terminal UI; swappable in tests.
var runTerminalUIProgram = tui.Run

// runTerminalUI opens the terminal UI as this machine's terminal client.
func runTerminalUI(cmd *cobra.Command) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	c, err := connectTerminal(ctx)
	if err != nil {
		return err
	}
	defer c.close()
	var streams []*client
	defer func() {
		for _, s := range streams {
			s.close()
		}
	}()
	p, err := paths.Default()
	if err != nil {
		return err
	}
	o := tui.Options{
		Daemon: c.conn,
		Me:     c.terminal,
		Subscribe: func() (tui.Stream, error) {
			s, err := connectTerminal(ctx)
			if err != nil {
				return nil, err
			}
			streams = append(streams, s)
			if err := s.conn.Subscribe(ipc.SubscribeRequest{}); err != nil {
				return nil, err
			}
			return s.conn, nil
		},
		Attach: func(s ipc.Session) tea.ExecCommand {
			return &attachRun{ctx: ctx, socket: p.HostSocket(s.ID), what: attachWhat(s.Core()), id: s.ID, errOut: cmd.ErrOrStderr()}
		},
	}
	return runTerminalUIProgram(o, cmd.InOrStdin(), cmd.OutOrStdout())
}

// attachRun is an attach the terminal UI hands the terminal over to. It uses
// the process's own stdin and stdout, as acmd attach does, so the streams
// Bubble Tea offers aren't kept.
type attachRun struct {
	ctx          context.Context
	socket, what string
	id           string
	errOut       io.Writer
}

func (a *attachRun) Run() error {
	if !sessionRunning(a.socket) {
		return fmt.Errorf("%s isn't running: resume it with acmd resume %s", a.what, a.id)
	}
	fmt.Fprintf(a.errOut, "Attached to %s. Detach with Ctrl+].\r\n", a.what)
	return attachTerminal(a.ctx, a.socket)
}

func (a *attachRun) SetStdin(io.Reader)  {}
func (a *attachRun) SetStdout(io.Writer) {}
func (a *attachRun) SetStderr(io.Writer) {}
