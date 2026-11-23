package editor

import (
	"fmt"
	"os"
	"runtime"
	"testing"
)

// TestMain skips this package on Windows: the editor opens sessions' folders here, and Windows is client-only.
func TestMain(m *testing.M) {
	if runtime.GOOS == "windows" {
		fmt.Println("skipped on Windows: the editor opens sessions' folders here, and Windows is client-only")
		os.Exit(0)
	}
	os.Exit(m.Run())
}
