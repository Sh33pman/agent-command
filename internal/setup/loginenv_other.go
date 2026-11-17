//go:build !darwin

package setup

import "os"

// DefaultShell is the shell to use when the login environment doesn't name one.
func DefaultShell() string {
	if _, err := os.Stat("/bin/bash"); err == nil {
		return "/bin/bash"
	}
	return "/bin/sh"
}
