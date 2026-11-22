//go:build !darwin && !linux

package peer

import "net"

// PID is the process on the other end of c: unknown here.
func PID(net.Conn) (int, bool) { return 0, false }
