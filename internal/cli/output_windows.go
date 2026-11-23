package cli

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// pointOutputAt makes stdout and stderr append to path. Windows has no dup2:
// the process's standard handles are set to the file, which the runtime's own
// writes (a panic) use, and os.Stdout and os.Stderr are pointed at it. The
// file stays open for as long as the process runs.
func pointOutputAt(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	h := windows.Handle(f.Fd())
	for _, std := range []uint32{windows.STD_OUTPUT_HANDLE, windows.STD_ERROR_HANDLE} {
		if err := windows.SetStdHandle(std, h); err != nil {
			_ = f.Close()
			return err
		}
	}
	os.Stdout, os.Stderr = f, f
	return nil
}
