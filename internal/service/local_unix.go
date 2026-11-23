//go:build !windows

package service

import (
	"os/exec"

	"github.com/brettmostert/agent-command/internal/proc"
)

// detach runs script in its own session, so it outlives the daemon it
// restarts.
func detach(script string) error {
	cmd := exec.Command("/bin/sh", "-c", script) // #nosec G204 -- a restart acmd built
	proc.Detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// isDaemon says whether pid runs. Only Windows asks: systemd and launchd know
// their daemon's pid.
func isDaemon(pid int) bool { return proc.Alive(pid) }
