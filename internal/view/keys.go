package view

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// TermKeys reads single keys from a terminal (or a pipe, in tests).
type TermKeys struct {
	In    *os.File
	saved *unix.Termios
}

// Start turns off line buffering and echo (keeping Ctrl+C etc.) when In
// is a terminal.
func (k *TermKeys) Start() error {
	fd := int(k.In.Fd())
	t, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return nil //nolint:nilerr // not a terminal: nothing to set up
	}
	saved := *t
	t.Lflag &^= unix.ICANON | unix.ECHO
	t.Cc[unix.VMIN], t.Cc[unix.VTIME] = 1, 0
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, t); err != nil {
		return err
	}
	k.saved = &saved
	return nil
}

// Stop restores the terminal.
func (k *TermKeys) Stop() {
	if k.saved != nil {
		_ = unix.IoctlSetTermios(int(k.In.Fd()), ioctlSetTermios, k.saved)
		k.saved = nil
	}
}

// Poll waits up to timeout for a key; 0 means none. EOF on In is an error.
func (k *TermKeys) Poll(timeout time.Duration) (byte, error) {
	fds := []unix.PollFd{{Fd: int32(k.In.Fd()), Events: unix.POLLIN}} // #nosec G115 -- small fd
	n, err := unix.Poll(fds, int(timeout/time.Millisecond))
	if errors.Is(err, unix.EINTR) || n == 0 {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var b [1]byte
	m, err := k.In.Read(b[:])
	if err != nil {
		return 0, err
	}
	if m == 0 {
		return 0, nil
	}
	return b[0], nil
}
