//go:build !darwin

package doctor

// ManagedSettings are Claude Code's managed (admin) settings paths.
var ManagedSettings = []string{"/etc/claude-code/managed-settings.json"}
