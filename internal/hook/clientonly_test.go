package hook

import (
	"fmt"
	"os"
	"runtime"
	"testing"
)

// TestMain skips this package on Windows: agents' hooks run where sessions do, and Windows is client-only.
func TestMain(m *testing.M) {
	if runtime.GOOS == "windows" {
		fmt.Println("skipped on Windows: agents' hooks run where sessions do, and Windows is client-only")
		os.Exit(0)
	}
	os.Exit(m.Run())
}
