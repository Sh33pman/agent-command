// Package proc looks up processes: a process's parent, name, arguments and
// terminal. The hook uses it to find the agent that ran it, and the daemon to
// tell who is on its socket.
package proc

// Proc is one process.
type Proc struct {
	Pid  int
	PPid int
	Comm string
	Args []string
	TTY  string // controlling terminal, e.g. /dev/ttys010 ("" = none)
}
