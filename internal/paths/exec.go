package paths

import (
	"io/fs"
	"runtime"
)

// IsExecutable says whether fi is a file that can be run: a regular file with
// an execute bit on Unix. Windows has no execute bits (Go reports none): a
// file runs by its extension there, so any regular file counts.
func IsExecutable(fi fs.FileInfo) bool {
	return isExecutable(runtime.GOOS, fi)
}

func isExecutable(goos string, fi fs.FileInfo) bool {
	if fi == nil || !fi.Mode().IsRegular() {
		return false
	}
	return goos == "windows" || fi.Mode().Perm()&0o111 != 0
}
