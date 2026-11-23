//go:build !windows

package paths

import (
	"fmt"
	"os"
)

// MakePrivate makes dir a folder only the user can use: 0700.
func MakePrivate(dir string) error {
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if st.Mode().Perm() == 0o700 {
		return nil
	}
	return os.Chmod(dir, 0o700) //nolint:gosec // G302: a folder needs its execute bit; 0700 is owner-only
}

// OthersCanRead says whether anyone besides the user can read path (its group
// or others' read bits), as the mode, and how to fix it.
func OthersCanRead(path string) (who, fix string, err error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", "", err
	}
	if st.Mode().Perm()&0o077 == 0 {
		return "", "", nil
	}
	return st.Mode().Perm().String(), fmt.Sprintf("run chmod 600 %s", path), nil
}
