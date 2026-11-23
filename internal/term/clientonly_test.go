package term

import (
	"fmt"
	"os"
	"runtime"
	"testing"
)

// TestMain skips this package on Windows: a session's terminal needs a pty, and Windows has none here (no ConPTY host).
func TestMain(m *testing.M) {
	if runtime.GOOS == "windows" {
		fmt.Println("skipped on Windows: a session's terminal needs a pty, and Windows has none here (no ConPTY host)")
		os.Exit(0)
	}
	os.Exit(m.Run())
}
