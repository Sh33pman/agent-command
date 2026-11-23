//go:build !windows

package daemon

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// lock takes an exclusive, non-blocking flock on path.
func lock(path string) (func(), error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, err
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}, nil
}
