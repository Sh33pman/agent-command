//go:build !darwin && !linux

package peer

import "net"

// uid isn't known here: acmd runs on macOS and Linux, and elsewhere the
// socket's 0600 permissions are the only check.
func uid(*net.UnixConn) (id int, known bool, err error) { return 0, false, nil }
