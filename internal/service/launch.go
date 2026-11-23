package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/brettmostert/agent-command/internal/proc"
)

// Launch is what acmdw.exe, the logon task's launcher, does. args are the pid
// file, acmd, and acmd's arguments (daemon). It starts acmd as the service's
// daemon (EnvVar set), in a console of its own with no window, writes its pid
// to the pid file, and returns: the task ends there, and the daemon runs on.
func Launch(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: acmdw <pid file> <acmd> [arguments]")
	}
	pidFile, acmd := args[0], args[1]
	cmd := exec.Command(acmd, args[2:]...) // #nosec G204 -- the acmd the logon task names
	cmd.Env = append(os.Environ(), EnvVar+"=1")
	proc.Detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", acmd, err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	tmp := pidFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, pidFile)
}

// LaunchFailed records why Launch failed where you'll look: the daemon's log,
// next to the pid file. A launcher with no console has nowhere else to say it.
func LaunchFailed(args []string, err error) {
	if len(args) == 0 {
		return
	}
	f, ferr := os.OpenFile(filepath.Join(filepath.Dir(args[0]), "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s acmdw: %v\n", time.Now().Format("2006/01/02 15:04:05"), err)
}
