//go:build !darwin

package hook

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// procInfo reads /proc where available (Linux support is a non-goal for
// v1; this keeps the package building elsewhere).
func procInfo(pid int) (Proc, error) {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return Proc{}, err
	}
	// pid (comm) state ppid ...
	s := string(stat)
	open, closing := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || closing < open {
		return Proc{}, fmt.Errorf("bad stat for %d", pid)
	}
	fields := strings.Fields(s[closing+1:])
	if len(fields) < 2 {
		return Proc{}, fmt.Errorf("bad stat for %d", pid)
	}
	ppid, _ := strconv.Atoi(fields[1])
	p := Proc{Pid: pid, PPid: ppid, Comm: s[open+1 : closing]}
	if cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		p.Args = strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
	}
	return p, nil
}
