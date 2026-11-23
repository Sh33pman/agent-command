//go:build !windows

package proc

import (
	"errors"
	"os/exec"
	"syscall"
)

// Detach makes cmd start in its own session, so it outlives the process that
// starts it and has no controlling terminal.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// Terminate asks pid to stop (SIGTERM).
func Terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// Alive says whether pid is a running process, even one of another user's.
func Alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
