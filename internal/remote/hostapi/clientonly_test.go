package hostapi

import (
	"fmt"
	"os"
	"runtime"
	"testing"
)

// TestMain skips this package on Windows: the host API serves a host's
// sessions, and Windows never hosts them (client-only, no ConPTY host).
func TestMain(m *testing.M) {
	if runtime.GOOS == "windows" {
		fmt.Println("skipped on Windows: the host API runs on hosts, and Windows is client-only")
		os.Exit(0)
	}
	os.Exit(m.Run())
}
