package harness

import (
	"fmt"
	"slices"
	"strings"
)

// Kinds of setting, which say how a form shows one.
const (
	KindText   = "text"   // one line
	KindLong   = "long"   // several lines
	KindChoice = "choice" // one of Choices
	KindList   = "list"   // several values
	KindPath   = "path"   // a file
	KindPaths  = "paths"  // several files or folders
)

// Setting is one setting an agent takes for a scheduled run, and the flag it
// becomes.
type Setting struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Help  string `json:"help,omitempty"`
	Kind  string `json:"kind"`
	// Choices for KindChoice; suggestions for KindText.
	Choices []string `json:"choices,omitempty"`
	Flag    string   `json:"flag"`
}

// ClaudeSettings are Claude Code's settings for a scheduled run.
var ClaudeSettings = []Setting{
	{Key: "model", Label: "Model", Kind: KindText, Flag: "--model",
		Help: "An alias for the latest model, or a model's full name. Empty: Claude's default.", Choices: []string{"fable", "opus", "sonnet", "haiku"}},
	{Key: "effort", Label: "Effort", Kind: KindChoice, Flag: "--effort",
		Choices: []string{"low", "medium", "high", "xhigh", "max"}},
	{Key: "permission-mode", Label: "Permission mode", Kind: KindChoice, Flag: "--permission-mode",
		Help:    "What Claude may do without asking. A run nobody watches stops at the first question.",
		Choices: []string{"manual", "acceptEdits", "auto", "plan", "dontAsk", "bypassPermissions"}},
	{Key: "allowed-tools", Label: "Allowed tools", Kind: KindList, Flag: "--allowedTools",
		Help: `Tools Claude may use without asking, like "Bash(git log:*)" or "Edit".`},
	{Key: "disallowed-tools", Label: "Disallowed tools", Kind: KindList, Flag: "--disallowedTools",
		Help: "Tools Claude may not use."},
	{Key: "add-dir", Label: "Extra folders", Kind: KindPaths, Flag: "--add-dir",
		Help: "Folders Claude may read and edit besides the job's folder."},
	{Key: "append-system-prompt", Label: "Extra system prompt", Kind: KindLong, Flag: "--append-system-prompt",
		Help: "Added to Claude's system prompt."},
	{Key: "agent", Label: "Agent", Kind: KindText, Flag: "--agent",
		Help: "A custom agent to run as, by name."},
	{Key: "mcp-config", Label: "MCP config", Kind: KindPaths, Flag: "--mcp-config",
		Help: "JSON files with MCP servers to load."},
	{Key: "settings", Label: "Settings file", Kind: KindPath, Flag: "--settings",
		Help: "A settings JSON file to load."},
}

// SettingsFor is a harness's settings for scheduled runs (nil: it has none).
func SettingsFor(harnessName string) []Setting {
	switch harnessName {
	case "", "claude":
		return ClaudeSettings
	}
	return nil
}

// CheckSettings reports the first setting that the harness doesn't take, or
// whose value isn't one it allows.
func CheckSettings(settings []Setting, values map[string][]string) error {
	for _, key := range sortedKeys(values) {
		i := slices.IndexFunc(settings, func(s Setting) bool { return s.Key == key })
		if i < 0 {
			return fmt.Errorf("the agent has no setting %q", key)
		}
		s, vs := settings[i], nonEmpty(values[key])
		if len(vs) > 1 && s.Kind != KindList && s.Kind != KindPaths {
			return fmt.Errorf("%s takes one value", s.Label)
		}
		for _, v := range vs {
			if s.Kind == KindChoice && !slices.Contains(s.Choices, v) {
				return fmt.Errorf("%s can't be %q: it's one of %s", s.Label, v, strings.Join(s.Choices, ", "))
			}
			if strings.HasPrefix(v, "-") && s.Kind != KindLong {
				return fmt.Errorf("%s can't start with a dash", s.Label)
			}
		}
	}
	return nil
}

// SettingsArgs turns settings into the harness's flags, in the table's order.
// Lists repeat their flag for each value.
func SettingsArgs(settings []Setting, values map[string][]string) []string {
	var args []string
	for _, s := range settings {
		for _, v := range nonEmpty(values[s.Key]) {
			args = append(args, s.Flag, v)
		}
	}
	return args
}

func nonEmpty(vs []string) []string {
	var out []string
	for _, v := range vs {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
