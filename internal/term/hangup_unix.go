//go:build !windows

package term

import (
	"os"
	"os/signal"
	"syscall"
	"time"
)

// hangupGroup hangs up the shell's process group (pty.Start sets setsid), then
// terminates and kills it if it's still there after 5 s each.
func hangupGroup(p *os.Process, exited <-chan struct{}) {
	if p == nil {
		return
	}
	pgid := -p.Pid
	if signal.Ignored(syscall.SIGHUP) {
		// Parent ignored SIGHUP (e.g. nohup), so the child inherited SIG_IGN.
		_ = syscall.Kill(pgid, syscall.SIGTERM)
		go func() {
			select {
			case <-exited:
			case <-time.After(5 * time.Second):
				_ = syscall.Kill(pgid, syscall.SIGKILL)
			}
		}()
		return
	}
	_ = syscall.Kill(pgid, syscall.SIGHUP)
	go func() {
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(pgid, syscall.SIGTERM)
			select {
			case <-exited:
			case <-time.After(5 * time.Second):
				_ = syscall.Kill(pgid, syscall.SIGKILL)
			}
		}
	}()
}
