package core

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// A Harness is something sessions and jobs run in: one of a harness kind
// (HarnessClaude or HarnessAntigravity) with its own name and settings, so
// one kind can be set up more than once (two Claude accounts, each with its
// own CLAUDE_CONFIG_DIR). A terminal isn't a harness: its sessions have the
// kind HarnessTerminal and no harness.
type Harness struct {
	ID   string
	Kind string // HarnessClaude or HarnessAntigravity
	Name string
	// Order is its place in the list (from 1); the first one of a kind whose
	// program is installed is the default when none is chosen.
	Order int
	// Command is the program it runs; "" is the kind's own (claude or agy),
	// found on the login shell's PATH.
	Command string
	// Args are extra arguments for every session it starts.
	Args []string
	// ConfigDir is Claude's CLAUDE_CONFIG_DIR for it ("~/" allowed). "" is
	// Claude's standard folder: CLAUDE_CONFIG_DIR is left as it is, so Claude
	// reads ~/.claude (and ~/.claude.json, and its usual keychain entry).
	ConfigDir string
	CreatedAt time.Time
}

// HarnessKinds are the kinds a harness can be, in the order clients list them.
var HarnessKinds = []string{HarnessClaude, HarnessAntigravity}

// IsHarnessKind says whether kind is one a harness can be.
func IsHarnessKind(kind string) bool {
	return kind == HarnessClaude || kind == HarnessAntigravity
}

// KindTitle is how clients name a harness kind.
func KindTitle(kind string) string {
	switch kind {
	case HarnessClaude:
		return "Claude"
	case HarnessAntigravity:
		return antigravityAgent
	case HarnessTerminal:
		return "Terminal"
	}
	return kind
}

// KindCommand is the program a kind runs when its harness names none.
func KindCommand(kind string) string {
	if kind == HarnessAntigravity {
		return "agy"
	}
	return "claude"
}

// RunCommand is the program h runs.
func (h Harness) RunCommand() string {
	if c := strings.TrimSpace(h.Command); c != "" {
		return c
	}
	return KindCommand(h.Kind)
}

var harnessNameRe = regexp.MustCompile(`^[A-Za-z]+( [A-Za-z]+)*$`)

// ErrHarnessName is a harness name that breaks the rule.
var ErrHarnessName = errors.New("a harness's name is letters, with single spaces between words")

// CheckHarnessName says why name can't be a harness's name; it's checked as
// typed (trimmed), never cleaned up.
func CheckHarnessName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("a harness needs a name")
	}
	if !harnessNameRe.MatchString(name) {
		return fmt.Errorf("%q: %w (no digits or other characters)", name, ErrHarnessName)
	}
	return nil
}

// HarnessSlug is name as it goes in a folder, and how names are compared:
// lower case, spaces as "_". "My Work" and "my work" are the same harness.
func HarnessSlug(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), " ", "_")
}

// DefaultConfigDir is a new Claude harness's folder: ~/.claude-<slug>.
func DefaultConfigDir(name string) string {
	return "~/.claude-" + HarnessSlug(name)
}

// ExpandHome expands a leading "~/" (or a bare "~") to home.
func ExpandHome(path, home string) string {
	switch {
	case path == "~":
		return home
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(home, path[2:])
	}
	return path
}

// ClaudeDir is the folder Claude reads for h: its ConfigDir, or Claude's
// standard folder (CLAUDE_CONFIG_DIR from getenv, else ~/.claude) for "".
func (h Harness) ClaudeDir(getenv func(string) string, home string) string {
	if h.ConfigDir != "" {
		return ExpandHome(h.ConfigDir, home)
	}
	if d := getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(home, ".claude")
}

// Env is env with h's CLAUDE_CONFIG_DIR, for a Claude harness with its own
// folder. Claude's standard folder leaves env as it is.
func (h Harness) Env(env []string, home string) []string {
	if h.Kind != HarnessClaude || h.ConfigDir == "" {
		return env
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			out = append(out, kv)
		}
	}
	return append(out, "CLAUDE_CONFIG_DIR="+ExpandHome(h.ConfigDir, home))
}
