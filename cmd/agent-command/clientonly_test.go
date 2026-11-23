package main

import (
	"fmt"
	"os"
	"runtime"
	"testing"
)

// TestMain skips this package on Windows: these run a hook end to end, and Windows is client-only: no hooks.
func TestMain(m *testing.M) {
	if runtime.GOOS == "windows" {
		fmt.Println("skipped on Windows: these run a hook end to end, and Windows is client-only: no hooks")
		os.Exit(0)
	}
	os.Exit(m.Run())
}
