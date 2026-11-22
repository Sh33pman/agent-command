//go:build darwin

package peer

import (
	"net"

	"golang.org/x/sys/unix"
)

// PID is the process on the other end of c, when the system says.
func PID(c net.Conn) (int, bool) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, false
	}
	pid, perr := 0, error(nil)
	if err := raw.Control(func(fd uintptr) { pid, perr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID) }); err != nil || perr != nil {
		return 0, false
	}
	return pid, pid > 0
}
