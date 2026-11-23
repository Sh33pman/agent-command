package service

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"

	"github.com/brettmostert/agent-command/internal/proc"
)

// Local is this machine.
func Local() (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, err
	}
	name := os.Getenv("USER")
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	return Env{
		GOOS:         runtime.GOOS,
		Home:         home,
		UID:          os.Getuid(),
		User:         name,
		Run:          run,
		Detach:       detach,
		Systemd:      func() bool { _, err := os.Stat("/run/systemd/system"); return err == nil },
		WSL:          isWSL(),
		LocalAppData: os.Getenv("LOCALAPPDATA"),
		Exists:       func(p string) bool { _, err := os.Stat(p); return err == nil },
		Alive:        isDaemon,
		Kill:         proc.Terminate,
		ReadFile:     os.ReadFile,
		WriteFile:    os.WriteFile,
		Remove:       os.Remove,
	}, nil
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput() // #nosec G204 -- systemctl, loginctl, launchctl or schtasks, with acmd's own arguments
}

func isWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	b, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}
