package host

import (
	"errors"

	"golang.org/x/sys/windows"
)

// addrInUse says a listen failed because the port is taken: Winsock's
// WSAEADDRINUSE, which isn't syscall.EADDRINUSE there.
func addrInUse(err error) bool { return errors.Is(err, windows.WSAEADDRINUSE) }
