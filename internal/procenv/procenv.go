// Package procenv builds environments for processes acmd starts.
package procenv

import "strings"

// Scrub returns env without Claude Code's session variables (CLAUDECODE,
// CLAUDE_CODE_*, CLAUDE_PID, …). Inherited from a Claude session (hooks run
// inside one) they make a `claude` started by acmd run as a child session
// with transcripts off (M0 C5), so every process acmd spawns gets a scrubbed
// environment.
func Scrub(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "CLAUDE") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
