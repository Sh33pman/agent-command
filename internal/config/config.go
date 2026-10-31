// Package config reads acmd's config.toml (SPEC §10) and edits single
// values in it without disturbing the user's comments and layout.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config holds the settings acmd reads so far; unknown keys are ignored
// (the file may carry settings of later versions).
type Config struct {
	ClaudeCommand string   `toml:"claude_command"`
	ClaudeArgs    []string `toml:"claude_args"`
	Editor        Editor   `toml:"editor"`
	Keys          Keys     `toml:"keys"`
}

// Editor is the [editor] section (SPEC §8.6).
type Editor struct {
	Preset   string   `toml:"preset"`   // vscode | cursor | zed | idea | custom | "" (not chosen)
	Command  []string `toml:"command"`  // custom only: argv template with {dir}
	Resolved string   `toml:"resolved"` // absolute launcher path, written by setup
	RunIn    string   `toml:"run_in"`   // background | tab
	Target   string   `toml:"target"`   // cwd | git_root
}

// Keys is the [keys] section: global terminal shortcuts (SPEC §8.3).
// Field order matches setup.Keys so the two convert directly.
type Keys struct {
	Toggle     string `toml:"toggle"`
	New        string `toml:"new"`
	Focus      string `toml:"focus"`
	Attention  string `toml:"attention"`
	Picker     string `toml:"picker"`
	Cheatsheet string `toml:"cheatsheet"`
	Editor     string `toml:"editor"`
	GotoPrefix string `toml:"goto_prefix"`
}

// Default is the configuration used for missing keys.
func Default() Config {
	return Config{
		ClaudeCommand: "claude",
		Editor:        Editor{RunIn: "background", Target: "cwd"},
		Keys: Keys{
			Toggle: "cmd+shift+b", New: "cmd+shift+n", Focus: "cmd+shift+s", Attention: "cmd+shift+a",
			Picker: "cmd+shift+l", Cheatsheet: "cmd+shift+k", Editor: "cmd+shift+e", GotoPrefix: "ctrl+cmd",
		},
	}
}

// Parse decodes config.toml over the defaults.
func Parse(b []byte) (Config, error) {
	c := Default()
	if _, err := toml.Decode(string(b), &c); err != nil {
		return Config{}, fmt.Errorf("config.toml: %w", err)
	}
	return c, nil
}

// Load reads config.toml; a missing file is the default configuration.
func Load(path string) (Config, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- acmd's own config file
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}
	return Parse(b)
}

// Template is the config.toml written on first setup: the settings acmd
// reads so far, at their defaults, with comments on their own lines (Set
// replaces whole lines).
const Template = `# agent-command (acmd) configuration. See: acmd help, SPEC §10.

# Absolute path of the claude binary (resolved by ` + "`acmd init`" + `).
claude_command = "claude"
# Extra arguments for new sessions (passed to ` + "`claude --bg`" + `).
claude_args = []

[editor]
# vscode | cursor | zed | idea | custom; "" asks on first use. Change with: acmd editor set
preset = ""
# custom only: argv template, e.g. ["nvim", "{dir}"]
command = []
# The launcher's absolute path (written by acmd).
resolved = ""
# background (GUI editors) | tab (terminal editors)
run_in = "background"
# cwd | git_root
target = "cwd"

[keys]
# Global terminal shortcuts. Rerun ` + "`acmd init`" + ` after changing them.
toggle = "cmd+shift+b"
new = "cmd+shift+n"
focus = "cmd+shift+s"
attention = "cmd+shift+a"
picker = "cmd+shift+l"
cheatsheet = "cmd+shift+k"
editor = "cmd+shift+e"
goto_prefix = "ctrl+cmd"
`

var sectionRe = regexp.MustCompile(`^\s*\[\s*([A-Za-z0-9_.-]+)\s*\]\s*(#.*)?$`)

// Set sets section.key (section "" is the top level) to value in the TOML
// text, replacing the key's line(s) or adding it to the section (adding
// the section if needed). Everything else is kept byte for byte. The
// result is checked to parse and to hold the new value.
func Set(text, section, key string, value any) (string, error) {
	rendered, err := renderValue(value)
	if err != nil {
		return "", err
	}
	line := key + " = " + rendered
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	// The section's line range [start, end).
	start, end := -1, len(lines)
	if section == "" {
		start = 0
	}
	for i, l := range lines {
		m := sectionRe.FindStringSubmatch(strings.TrimRight(l, "\r\n"))
		if m == nil && !strings.HasPrefix(strings.TrimSpace(l), "[[") {
			continue
		}
		if start >= 0 {
			end = i
			break
		}
		if m != nil && m[1] == section {
			start = i + 1
		}
	}

	var out []string
	switch {
	case start < 0: // no such section: append it
		out = append(out, lines...)
		if len(out) > 0 && !strings.HasSuffix(out[len(out)-1], "\n") {
			out[len(out)-1] += "\n"
		}
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "\n")
		}
		out = append(out, "["+section+"]\n", line+"\n")
	default:
		keyRe := regexp.MustCompile(`^(\s*)` + regexp.QuoteMeta(key) + `\s*=`)
		at := -1
		for i := start; i < end; i++ {
			if keyRe.MatchString(lines[i]) {
				at = i
				break
			}
		}
		if at >= 0 {
			n := valueExtent(lines[at:end])
			indent := keyRe.FindStringSubmatch(lines[at])[1]
			out = append(out, lines[:at]...)
			out = append(out, indent+line+"\n")
			out = append(out, lines[at+n:]...)
		} else {
			// Insert after the section's last non-blank line.
			ins := start
			for i := start; i < end; i++ {
				if strings.TrimSpace(lines[i]) != "" {
					ins = i + 1
				}
			}
			out = append(out, lines[:ins]...)
			if ins > 0 && !strings.HasSuffix(out[len(out)-1], "\n") {
				out[len(out)-1] += "\n"
			}
			out = append(out, line+"\n")
			out = append(out, lines[ins:]...)
		}
	}
	result := strings.Join(out, "")

	// Verify: the file still parses and holds the value.
	var doc map[string]any
	if _, err := toml.Decode(result, &doc); err != nil {
		return "", fmt.Errorf("config.toml: setting %s: %w", key, err)
	}
	if section != "" {
		sub, ok := doc[section].(map[string]any)
		if !ok {
			return "", fmt.Errorf("config.toml: [%s] is not a table", section)
		}
		doc = sub
	}
	got, _ := renderValue(doc[key])
	if got != rendered {
		return "", fmt.Errorf("config.toml: %s reads back as %s, want %s", key, got, rendered)
	}
	return result, nil
}

// valueExtent returns how many lines the key/value pair at lines[0]
// spans (multi-line arrays and strings).
func valueExtent(lines []string) int {
	for n := 1; n <= len(lines); n++ {
		var v map[string]any
		if _, err := toml.Decode(strings.Join(lines[:n], ""), &v); err == nil {
			return n
		}
	}
	return 1
}

// renderValue renders a single TOML value.
func renderValue(v any) (string, error) {
	if v == nil {
		return "", errors.New("config: nil value")
	}
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Slice && rv.Len() == 0 {
		return "[]", nil // the encoder writes nothing for nil slices
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(map[string]any{"v": v}); err != nil {
		return "", err
	}
	s := strings.TrimSpace(buf.String())
	return strings.TrimSpace(strings.TrimPrefix(s, "v =")), nil
}
