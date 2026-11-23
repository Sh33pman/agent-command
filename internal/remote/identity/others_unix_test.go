//go:build !windows

package identity

import (
	"os"
	"testing"
)

// notFound is how the OS says a file isn't there.
const notFound = "no such file"

// readableBy makes path readable by others ("others") or its group ("group").
func readableBy(t *testing.T, path, who string) {
	t.Helper()
	mode := os.FileMode(0o604)
	if who == "group" {
		mode = 0o640
	}
	must(t, os.Chmod(path, mode))
}

// private checks path has mode want (0700 or 0600: only you).
func private(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if got := perm(t, path); got != want {
		t.Errorf("%s is %o, want %o", path, got, want)
	}
}
