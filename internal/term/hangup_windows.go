package term

import "os"

// hangupGroup kills the shell. Windows has no pty here (pty.Start answers
// ErrUnsupported), so a session's shell never starts and this isn't reached.
func hangupGroup(p *os.Process, _ <-chan struct{}) {
	if p != nil {
		_ = p.Kill()
	}
}
