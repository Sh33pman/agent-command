//go:build !windows

package host

import (
	"errors"
	"syscall"
)

// addrInUse says a listen failed because the port is taken.
func addrInUse(err error) bool { return errors.Is(err, syscall.EADDRINUSE) }
