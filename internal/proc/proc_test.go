package proc

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLookupThisProcess(t *testing.T) {
	p, err := Lookup(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if p.PPid != os.Getppid() || p.Comm == "" || len(p.Args) == 0 || !strings.HasSuffix(p.Args[0], filepath.Base(os.Args[0])) {
		t.Errorf("Lookup(self) = %+v (os.Args[0]=%s)", p, os.Args[0])
	}
	if runtime.GOOS == "darwin" && p.TTY != "" && !strings.HasPrefix(p.TTY, "/dev/ttys") {
		t.Errorf("tty %q", p.TTY)
	}
	if _, err := Lookup(999999); err == nil {
		t.Error("expected error for a nonexistent pid")
	}
}
