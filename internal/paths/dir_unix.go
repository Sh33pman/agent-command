//go:build !windows

package paths

import (
	"fmt"
	"os"
)

func ensureDir(dir string, realFolder bool) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return ownDir(dir, os.Getuid(), realFolder)
}

// ownDir checks that dir is a folder that belongs to uid, and makes it 0700.
func ownDir(dir string, uid int, realFolder bool) error {
	if realFolder {
		lst, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if lst.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink, not a folder: refusing to use it", dir)
		}
	}
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a folder", dir)
	}
	if owner, known := ownerUID(st); known && owner != uid {
		return fmt.Errorf("%s belongs to user %d, not %d: refusing to use it", dir, owner, uid)
	}
	if st.Mode().Perm() == 0o700 {
		return nil
	}
	// The mode is changed through the open folder, after checking it is the one
	// that was just looked at, so it can't be swapped for another in between.
	f, err := os.Open(dir) //nolint:gosec // G304: acmd's own folder, checked above
	if err != nil {
		return err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(st, opened) {
		return fmt.Errorf("%s changed while it was being checked: refusing to use it", dir)
	}
	// Folders need the execute bit; 0700 is owner-only.
	return f.Chmod(0o700)
}
