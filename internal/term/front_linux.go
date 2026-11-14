//go:build linux

package term

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// front names the program in front of the PTY: its foreground process group's
// leader. ok is false when that can't be read.
func front(ptmx *os.File, shellPid int) (Front, bool) {
	pg, err := unix.IoctlGetInt(int(ptmx.Fd()), unix.TIOCGPGRP)
	if err != nil || pg <= 0 {
		return Front{}, false
	}
	if pg == shellPid {
		return Front{Shell: true}, true
	}
	if argv := procArgsLinux(pg); len(argv) > 0 {
		return Front{Name: programName(argv)}, true
	}
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pg))
	if err != nil {
		return Front{}, false
	}
	name := strings.TrimSpace(string(comm))
	if name == "" {
		return Front{}, false
	}
	return Front{Name: name}, true
}

func procArgsLinux(pid int) []string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil
	}
	b = bytes.TrimRight(b, "\x00")
	if len(b) == 0 {
		return nil
	}
	parts := bytes.Split(b, []byte{0})
	argv := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) > 0 {
			argv = append(argv, string(p))
		}
	}
	return argv
}
