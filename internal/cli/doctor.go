package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/brettmostert/agent-command/internal/doctor"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/paths"
	"github.com/brettmostert/agent-command/internal/setup"
	"github.com/brettmostert/agent-command/internal/version"
)

// ErrSilent makes acmd exit 1 without printing an error (the command
// already explained what's wrong).
var ErrSilent = errors.New("silent failure")

// versionOutput runs `<bin> --version`; swappable in tests.
var versionOutput = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output() // #nosec G204 -- claude/kitty found on PATH, fixed args
}

func newDoctorCmd() *cobra.Command {
	var fix, asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check acmd's setup, Claude Code and the daemon, and show how to fix problems",
		Long: "Checks that acmd's hooks are in place and point at this binary, that nothing disables\n" +
			"Claude Code's hooks and that versions are supported. It changes\n" +
			"nothing unless you pass --fix, which removes and re-adds the hooks and replaces a stale daemon.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if fix {
				if err := runFix(cmd); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout())
			}
			env, err := doctorEnv()
			if err != nil {
				return err
			}
			if asJSON {
				// The daemon's health list, which the apps show: the same checks.
				// It always succeeds: the problems are the answer.
				health, err := daemonHealth(cmd.Context(), env)
				if err != nil {
					return err
				}
				b, err := json.MarshalIndent(struct {
					Health []ipc.Health `json:"health"`
				}{health}, "", "  ")
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return err
			}
			report := doctor.Run(cmd.Context(), env)
			report.Write(cmd.OutOrStdout())
			if report.Count(doctor.Fail) > 0 {
				return ErrSilent
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "fix what it can: re-add the hooks and replace a stale daemon")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the health list as JSON: what the apps warn about, each with its fix")
	return cmd
}

// runFix removes and re-adds the hooks, and stops a daemon that doesn't match
// this binary. The next command or the app starts the right one.
func runFix(cmd *cobra.Command) error {
	if _, err := runInit(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), initOptions{}); err != nil {
		return err
	}
	p, err := paths.Default()
	if err != nil {
		return err
	}
	d, err := daemonState(cmd.Context(), p)
	if err != nil {
		return err
	}
	if d.Mismatch != "" || (d.Binary != "" && !isExecutableFile(d.Binary)) {
		stopDaemon(cmd.Context(), p)
		fmt.Fprintln(cmd.OutOrStdout(), "Stopped the old background service.")
	}
	return nil
}

func doctorEnv() (doctor.Env, error) {
	p, err := paths.Default()
	if err != nil {
		return doctor.Env{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return doctor.Env{}, err
	}
	exe, _ := executable()
	pathList := loginPath()
	return doctor.Env{
		Home:      home,
		Paths:     p,
		Version:   version.Get().Version,
		AcmdPath:  setup.StableBinary(invokedAs, exe, exec.LookPath),
		Getenv:    os.Getenv,
		Read:      os.ReadFile,
		Stat:      os.Stat,
		Resolve:   filepath.EvalSymlinks,
		LoginPath: pathList,
		LookPath:  func(n string) (string, error) { return setup.LookPathIn(n, pathList) },
		Output:    versionOutput,
		HasBundle: hasBundle,
		Daemon:    func(ctx context.Context) (doctor.DaemonState, error) { return daemonState(ctx, p) },
		InitPlan: func(ctx context.Context) (*setup.Plan, error) {
			g, err := gather(ctx, false)
			if err != nil {
				return nil, err
			}
			return setup.PlanInit(g.in)
		},
	}, nil
}

// daemonState connects to a running daemon without ever starting one.
func daemonState(ctx context.Context, p paths.Paths) (doctor.DaemonState, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := ipc.Dial(ctx, p.Socket(), ipc.Hello{Proto: ipc.Proto, Version: version.Get().Version})
	var mismatch *ipc.ErrVersionMismatch
	switch {
	case errors.As(err, &mismatch):
		_ = conn.Close()
		return doctor.DaemonState{Running: true, Mismatch: mismatch.Daemon.Version, Binary: processBinary(mismatch.Daemon.Pid)}, nil
	case errors.Is(err, fs.ErrPermission):
		return doctor.DaemonState{}, err
	case err != nil:
		return doctor.DaemonState{}, nil // no daemon (or a stale socket)
	}
	defer func() { _ = conn.Close() }()
	var st ipc.Status
	if err := conn.Call(ipc.OpStatus, nil, &st); err != nil {
		return doctor.DaemonState{}, err
	}
	var hosts []ipc.RemoteHost
	_ = conn.Call(ipc.OpRemoteHosts, nil, &hosts) // none, when it can't say
	return doctor.DaemonState{Running: true, Status: st, Binary: processBinary(st.Pid), RemoteHosts: hosts}, nil
}

// processBinary is the program a process runs, even if its file is gone.
func processBinary(pid int) string {
	if pid <= 0 {
		return ""
	}
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output() // #nosec G204 -- fixed program; a pid
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// checkHealth is the daemon's setup checks (daemon.Config.CheckHealth).
func checkHealth(ctx context.Context) []ipc.Health {
	env, err := doctorEnv()
	if err != nil {
		return nil
	}
	return doctor.Health(ctx, env)
}

// healthFiles are the agents' settings files the daemon watches, so a hook
// added or lost shows at once.
func healthFiles() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(setup.ClaudeConfigDir(home, os.Getenv), "settings.json"),
		filepath.Join(setup.AntigravityConfigDir(home), "hooks.json"),
	}
}

// daemonInit is the init op: acmd init, run by the daemon for a client, with
// the login shell's environment captured afresh (a daemon started by launchd
// has a minimal one). Nobody's there to answer questions.
func daemonInit(ctx context.Context) (ipc.InitResult, error) {
	var out bytes.Buffer
	done, err := runInit(ctx, strings.NewReader(""), &out, initOptions{refreshEnv: true, noWake: true})
	return ipc.InitResult{Output: out.String(), Done: done}, err
}

// daemonHealth is the running daemon's health list, or, with none running,
// the setup checks it would make: doctor --json starts no daemon.
func daemonHealth(ctx context.Context, env doctor.Env) ([]ipc.Health, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := ipc.Dial(ctx, env.Paths.Socket(), ipc.Hello{Proto: ipc.Proto, Version: version.Get().Version})
	if err != nil {
		if conn != nil {
			_ = conn.Close()
		}
		h := doctor.Health(ctx, env)
		if h == nil {
			h = []ipc.Health{}
		}
		return h, nil
	}
	defer conn.Close()
	var s ipc.Snapshot
	if err := conn.Call(ipc.OpSnapshot, nil, &s); err != nil {
		return nil, err
	}
	if s.Health == nil {
		s.Health = []ipc.Health{}
	}
	return s.Health, nil
}
