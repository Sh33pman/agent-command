package service

import (
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/brettmostert/agent-command/internal/proc"
)

// detach runs script with cmd.exe, with no window, so it outlives the daemon
// it restarts.
func detach(script string) error {
	cmd := exec.Command("cmd.exe", "/C", script) // #nosec G204 -- a restart acmd built
	proc.Detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// isDaemon says whether pid is still an acmd: a pid in PidFile can be reused
// by another program once the daemon has gone.
func isDaemon(pid int) bool {
	if !proc.Alive(pid) {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // G115: a pid
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h) //nolint:errcheck // read-only handle
	buf := make([]uint16, windows.MAX_PATH)
	n := uint32(windows.MAX_PATH)
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return false
	}
	return strings.EqualFold(filepath.Base(windows.UTF16ToString(buf[:n])), "acmd.exe")
}
