//go:build !darwin

package daemon

import "net"

// checkPeer relies on the socket's 0600 permissions outside macOS
// (Linux support is a non-goal for v1).
func checkPeer(net.Conn) error { return nil }
