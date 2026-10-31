package cli

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check acmd's setup, Claude Code, Kitty and the daemon, and show how to fix problems",
		Long: "Checks that acmd's hooks and shortcuts are in place and point at this binary, that nothing disables\n" +
			"Claude Code's hooks, that Kitty's remote control works and that versions are supported. It changes\n" +
			"nothing: each problem comes with the command that fixes it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := doctorEnv()
			if err != nil {
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
			g, err := gather(ctx, false, false)
			if err != nil {
				return nil, err
			}
			return setup.PlanInit(g.in)
		},
		KittyBinary: findKitty(pathList),
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
		return doctor.DaemonState{Running: true, Mismatch: mismatch.Daemon.Version}, nil
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
	return doctor.DaemonState{Running: true, Status: st}, nil
}
