package proc

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// stillActive is GetExitCodeProcess's code for a process that hasn't exited.
const stillActive = 259

// Detach makes cmd start with no console and in its own process group, so it
// outlives the process that starts it and a Ctrl+C there doesn't reach it.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
}

// Terminate stops pid. Windows has no SIGTERM for a process with no console,
// so it ends it.
func Terminate(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// Alive says whether pid is a running process.
func Alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) // #nosec G115 -- a pid
	if err != nil {
		// Another user's process can't be opened, but is there.
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(h) //nolint:errcheck // read-only handle
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
