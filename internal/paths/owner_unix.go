//go:build unix

package paths

import (
	"os"
	"syscall"
)

// ownerUID is the user a file belongs to.
func ownerUID(fi os.FileInfo) (uid int, known bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}
