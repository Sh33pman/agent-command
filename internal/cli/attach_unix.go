//go:build !windows

package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// detachSignals detach an attached terminal: SIGTERM (how the view loop
// switches sessions) and SIGHUP (the terminal went away).
var detachSignals = []os.Signal{syscall.SIGTERM, syscall.SIGHUP}

// ignoredWhileAttached are the job-control signals raw mode would otherwise
// leave to the process group.
var ignoredWhileAttached = []os.Signal{syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTSTP}

// watchResize tells when the terminal's size changes (SIGWINCH), until ctx
// ends.
func watchResize(ctx context.Context, _ func() (int, int)) <-chan struct{} {
	out := make(chan struct{}, 1)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		defer signal.Stop(winch)
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case <-winch:
				select {
				case out <- struct{}{}:
				default:
				}
			}
		}
	}()
	return out
}
