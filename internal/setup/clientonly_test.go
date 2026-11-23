package setup

import (
	"fmt"
	"os"
	"runtime"
	"testing"
)

// TestMain skips this package on Windows: the agents' hooks and the login shell's environment are for sessions here, and Windows is client-only (its init is covered in the daemon and cli tests).
func TestMain(m *testing.M) {
	if runtime.GOOS == "windows" {
		fmt.Println("skipped on Windows: the agents' hooks and the login shell's environment are for sessions here, and Windows is client-only (its init is covered in the daemon and cli tests)")
		os.Exit(0)
	}
	os.Exit(m.Run())
}
