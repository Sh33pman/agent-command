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

// Detach makes cmd start in a console of its own with no window, and in its
// own process group: it outlives the terminal that starts it, a Ctrl+C there
// doesn't reach it, and the console programs it runs (schtasks, cmd) share
// its hidden console instead of each opening a window. DETACHED_PROCESS,
// with no console at all, would give each of those a window.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP,
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
