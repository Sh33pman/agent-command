//go:build darwin

package setup

// DefaultShell is the shell to use when the login environment doesn't name one.
func DefaultShell() string { return "/bin/zsh" }
