package cli

import (
	"context"
	"os"
	"syscall"
	"time"
)

// detachSignals detach an attached terminal: Go delivers the console closing,
// a logoff or a shutdown as SIGTERM. The detach key is the other way out.
var detachSignals = []os.Signal{syscall.SIGTERM}

// ignoredWhileAttached: raw mode turns Ctrl+C into input, so Windows sends no
// console signal for it while attached.
var ignoredWhileAttached = []os.Signal{os.Interrupt}

// resizeEvery is how often the console's size is read: Windows has no
// SIGWINCH.
const resizeEvery = 250 * time.Millisecond

// watchResize tells when size changes, by reading it every resizeEvery until
// ctx ends.
func watchResize(ctx context.Context, size func() (int, int)) <-chan struct{} {
	return pollResize(ctx, size, resizeEvery)
}
