// Package config reads acmd's config.toml and edits single
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
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config holds the settings acmd reads; unknown keys are ignored (the file may
// carry settings of later versions, or of earlier ones: see Retired).
type Config struct {
	ClaudeCommand string   `toml:"claude_command"`
	ClaudeArgs    []string `toml:"claude_args"`
	// RemoteControl starts sessions with `claude --remote-control`, so they can be
	// used from claude.ai and the Claude app. Off by default.
	RemoteControl bool   `toml:"remote_control"`
	Editor        Editor `toml:"editor"`
	Notify        Notify `toml:"notify"`
	Speech        Speech `toml:"speech"`
}

// Retired are what acmd v1 kept in config.toml and nothing reads any more: its
// terminal shortcuts and popup (the app keeps its own) and the sidebar's width.
// `acmd init` takes them out.
var Retired = struct {
	Sections []string
	Keys     []string // top-level keys
}{
	Sections: []string{"keys", "popup", "popup.keys"},
	Keys:     []string{"sidebar_width"},
}

// Speech is the [speech] section: text to speech.
// (Older configs may still have engine and model, from the Python engine;
// they're ignored.)
type Speech struct {
	Voice string  `toml:"voice"` // a voice, or a blend "af_heart:0.6,af_bella:0.4"
	Speed float64 `toml:"speed"`
}

// Notify is the [notify] section.
type Notify struct {
	Enabled         bool   `toml:"enabled"`
	OnWaiting       bool   `toml:"on_waiting"`
	OnError         bool   `toml:"on_error"`
	OnDone          bool   `toml:"on_done"`
	OnPaused        bool   `toml:"on_paused"`
	DoneMinDuration string `toml:"done_min_duration"` // only "finished" for turns at least this long
	Cooldown        string `toml:"cooldown"`          // at most one notification per session per this
	Sound           string `toml:"sound"`             // "" for silent
}

// Durations parses DoneMinDuration and Cooldown (defaults when invalid).
func (n Notify) Durations() (doneMin, cooldown time.Duration) {
	doneMin, cooldown = 10*time.Second, 20*time.Second
	if d, err := time.ParseDuration(n.DoneMinDuration); err == nil && d >= 0 {
		doneMin = d
	}
	if d, err := time.ParseDuration(n.Cooldown); err == nil && d >= 0 {
		cooldown = d
	}
	return doneMin, cooldown
}

// Editor is the [editor] section.
type Editor struct {
	Preset   string   `toml:"preset"`   // vscode | cursor | zed | idea | custom | "" (not chosen)
	Command  []string `toml:"command"`  // custom only: argv template with {dir}
	Resolved string   `toml:"resolved"` // absolute launcher path, written by setup
	RunIn    string   `toml:"run_in"`   // background | tab
	Target   string   `toml:"target"`   // cwd | git_root
}

// Default is the configuration used for missing keys.
func Default() Config {
	return Config{
		ClaudeCommand: "claude",
		Notify: Notify{Enabled: true, OnWaiting: true, OnError: true, OnDone: true,
			DoneMinDuration: "10s", Cooldown: "20s", Sound: "default"},
		Editor: Editor{RunIn: "background", Target: "cwd"},
		Speech: Speech{Voice: "af_heart:0.6,af_bella:0.4", Speed: 1.0},
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
const Template = `# agent-command (acmd) configuration. See: acmd help. The app's Settings
# (⌘,) and ` + "`acmd config set`" + ` change it too.

# Absolute path of the claude binary (resolved by ` + "`acmd init`" + `).
claude_command = "claude"
# Extra arguments for new sessions (passed to ` + "`claude`" + `).
claude_args = []
# Start sessions with Remote Control, so you can use them from claude.ai and
# the Claude app. Applies to sessions started or resumed after a change.
remote_control = false

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

[notify]
# Notifications, for sessions that need you, fail, finish or stop. A change
# applies straight away.
enabled = true
on_waiting = true
on_error = true
on_done = true
on_paused = false
# Only say a turn finished when it took at least this long.
done_min_duration = "10s"
# At most one notification per session in this long.
cooldown = "20s"
# "default" plays the notification sound; "" is silent.
sound = "default"

[speech]
# Kokoro's voice; a blend is "voice:weight,voice:weight". American English
# voices only (af_, am_). Run "acmd speech install" after changing it.
voice = "af_heart:0.6,af_bella:0.4"
speed = 1.0
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

var retiredKeyRe = regexp.MustCompile(`^\s*(` + strings.Join(Retired.Keys, "|") + `)\s*=`)

// Retire takes the Retired sections and top-level keys out of text, and keeps the
// rest as it is. A text without them comes back unchanged. The result is checked
// to parse.
func Retire(text string) (string, error) {
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var out []string
	removed, topLevel, inRetired := false, true, false
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		m := sectionRe.FindStringSubmatch(strings.TrimRight(l, "\r\n"))
		if m != nil || strings.HasPrefix(strings.TrimSpace(l), "[[") {
			topLevel = false
			inRetired = m != nil && slices.Contains(Retired.Sections, m[1])
		}
		switch {
		case inRetired:
			removed = true
		case topLevel && retiredKeyRe.MatchString(l):
			removed = true
			i += valueExtent(lines[i:]) - 1
		default:
			out = append(out, l)
		}
	}
	if !removed {
		return text, nil
	}
	// What was taken out leaves blank lines behind: one between sections is enough.
	var tidy []string
	for _, l := range out {
		blank := strings.TrimSpace(l) == ""
		if blank && (len(tidy) == 0 || strings.TrimSpace(tidy[len(tidy)-1]) == "") {
			continue
		}
		tidy = append(tidy, l)
	}
	for len(tidy) > 0 && strings.TrimSpace(tidy[len(tidy)-1]) == "" {
		tidy = tidy[:len(tidy)-1]
	}
	result := strings.Join(tidy, "")
	if result != "" && !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	var doc map[string]any
	if _, err := toml.Decode(result, &doc); err != nil {
		return "", fmt.Errorf("config.toml: retiring v1 settings: %w", err)
	}
	return result, nil
}
