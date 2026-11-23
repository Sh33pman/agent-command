package proc

import "errors"

// Lookup isn't there on Windows: its only callers are the hook, which doesn't
// run there, and the daemon naming who is on its socket, which on Windows has
// no peer pid to look up (internal/peer).
func Lookup(int) (Proc, error) {
	return Proc{}, errors.New("process lookup isn't built for Windows")
}
