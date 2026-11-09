package term

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"

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
	if argv := procArgs(pg); len(argv) > 0 {
		return Front{Name: programName(argv)}, true
	}
	// Its arguments can't be read: the kernel's short name. For Claude that's
	// its version, so it isn't taken for an agent.
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pg)
	if err != nil {
		return Front{}, false
	}
	comm := k.Proc.P_comm[:]
	if i := bytes.IndexByte(comm, 0); i >= 0 {
		comm = comm[:i]
	}
	return Front{Name: string(comm)}, true
}

// procArgs returns a process's arguments, as it was started. The kernel gives
// argc, then the path it ran, padding, and the arguments.
func procArgs(pid int) []string {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(b) < 4 {
		return nil
	}
	argc := int(binary.LittleEndian.Uint32(b))
	rest := b[4:]
	i := bytes.IndexByte(rest, 0) // the path it ran
	if i < 0 {
		return nil
	}
	rest = bytes.TrimLeft(rest[i:], "\x00")
	var argv []string
	for len(argv) < argc && len(rest) > 0 {
		j := bytes.IndexByte(rest, 0)
		if j < 0 {
			j = len(rest)
		}
		argv = append(argv, string(rest[:j]))
		rest = rest[min(j+1, len(rest)):]
	}
	return argv
}

// interpreters run an agent written as a script; the script names it.
var interpreters = map[string]bool{"node": true, "bun": true, "deno": true, "python": true, "python3": true}

func programName(argv []string) string {
	name := filepath.Base(argv[0])
	if interpreters[name] {
		for _, a := range argv[1:] {
			if a != "" && a[0] != '-' {
				return filepath.Base(a)
			}
		}
	}
	return name
}
