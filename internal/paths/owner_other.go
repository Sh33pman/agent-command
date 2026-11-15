//go:build !unix

package paths

import "os"

// ownerUID isn't known here: acmd runs on macOS and Linux.
func ownerUID(os.FileInfo) (uid int, known bool) { return 0, false }
