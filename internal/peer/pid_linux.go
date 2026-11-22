//go:build linux

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
	var cred *unix.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) { cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil || cerr != nil {
		return 0, false
	}
	return int(cred.Pid), cred.Pid > 0
}
