// Package peer checks who is on the other end of a unix socket. The daemon's
// socket and each session's host socket use it: the sockets are 0600 in a 0700
// folder already, so this is defense in depth.
package peer

import (
	"errors"
	"fmt"
	"net"
	"os"
)

// Check rejects a connection that another user made.
func Check(c net.Conn) error { return check(c, os.Getuid()) }

func check(c net.Conn, want int) error {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return errors.New("not a unix connection")
	}
	got, known, err := uid(uc)
	if err != nil {
		return err
	}
	if known && got != want {
		return fmt.Errorf("peer uid %d is not %d", got, want)
	}
	return nil
}
