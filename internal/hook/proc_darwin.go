//go:build darwin

package hook

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"

	"golang.org/x/sys/unix"
)

// procInfo reads a process's parent, name and argv from the kernel via
// sysctl (fast: no `ps` subprocess on the hook's hot path).
func procInfo(pid int) (Proc, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return Proc{}, err
	}
	if kp.Proc.P_pid == 0 && pid != 0 {
		return Proc{}, errors.New("no such process")
	}
	p := Proc{
		Pid:  pid,
		PPid: int(kp.Eproc.Ppid),
		Comm: unix.ByteSliceToString(kp.Proc.P_comm[:]),
	}
	if raw, err := unix.SysctlRaw("kern.procargs2", pid); err == nil {
		p.Args = parseProcArgs2(raw)
	}
	return p, nil
}

// parseProcArgs2 decodes KERN_PROCARGS2: argc (int32), the executable path,
// NUL padding, then argc NUL-terminated arguments (then the environment,
// which we ignore).
func parseProcArgs2(raw []byte) []string {
	if len(raw) < 4 {
		return nil
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	rest := raw[4:]
	// Skip the executable path and its NUL padding.
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return nil
	}
	rest = rest[i:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	args := make([]string, 0, argc)
	for len(args) < argc && len(rest) > 0 {
		j := bytes.IndexByte(rest, 0)
		if j < 0 {
			args = append(args, string(rest))
			break
		}
		args = append(args, strings.Clone(string(rest[:j])))
		rest = rest[j+1:]
	}
	return args
}
