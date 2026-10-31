package core

import (
	"fmt"
	"path/filepath"
	"strings"
)

// MaxAutoNameRunes is the length of names derived from prompts (SPEC §5.5).
const MaxAutoNameRunes = 40

// NameFromPrompt derives a session name from the first line of a prompt,
// trimmed to MaxAutoNameRunes with an ellipsis.
func NameFromPrompt(prompt string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(prompt), "\n")
	line = strings.Join(strings.Fields(clean(line, 4096)), " ")
	r := []rune(line)
	if len(r) <= MaxAutoNameRunes {
		return line
	}
	return strings.TrimSpace(string(r[:MaxAutoNameRunes-1])) + "…"
}

// DefaultName picks a session name in SPEC §5.5 order: Claude's own title,
// the first prompt, the folder name, then "session N".
func DefaultName(claudeTitle, firstPrompt, cwd string, n int) string {
	if t := clean(claudeTitle, MaxAutoNameRunes*2); t != "" {
		return NameFromPrompt(t)
	}
	if p := NameFromPrompt(firstPrompt); p != "" {
		return p
	}
	if cwd != "" {
		if b := filepath.Base(filepath.Clean(cwd)); b != "/" && b != "." {
			return b
		}
	}
	return fmt.Sprintf("session %d", n)
}
