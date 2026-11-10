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
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config holds the settings acmd reads so far; unknown keys are ignored
// (the file may carry settings of later versions).
type Config struct {
	ClaudeCommand string   `toml:"claude_command"`
	ClaudeArgs    []string `toml:"claude_args"`
	// RemoteControl starts sessions with `claude --remote-control`, so they can be
	// used from claude.ai and the Claude app. Off by default.
	RemoteControl bool   `toml:"remote_control"`
	SidebarWidth  int    `toml:"sidebar_width"`
	Editor        Editor `toml:"editor"`
	Keys          Keys   `toml:"keys"`
	Notify        Notify `toml:"notify"`
	Popup         Popup  `toml:"popup"`
	Speech        Speech `toml:"speech"`
}

// Popup is the [popup] section: the reply popup.
type Popup struct {
	Enabled  bool   `toml:"enabled"`  // off by default; init asks
	AutoPlay bool   `toml:"autoplay"` // read each item aloud when it's shown
	Hotkey   string `toml:"hotkey"`   // system-wide: reopen the popup
	// ReadHotkey is system-wide: read the latest response or question of the
	// session you're in aloud (press again to stop). Empty turns it off.
	ReadHotkey string    `toml:"read_hotkey"`
	Keys       PopupKeys `toml:"keys"` // inside the popup
}

// PopupKeys are the popup's own keys (e.g. "cmd+ctrl+left", "escape").
type PopupKeys struct {
	Prev  string `toml:"prev"`
	Next  string `toml:"next"`
	Play  string `toml:"play"`
	Skip  string `toml:"skip"`
	Close string `toml:"close"`
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

// Keys is the [keys] section: global terminal shortcuts.
// Field order matches setup.Keys so the two convert directly.
type Keys struct {
	Toggle     string `toml:"toggle"`
	New        string `toml:"new"`
	Focus      string `toml:"focus"`
	Attention  string `toml:"attention"`
	Picker     string `toml:"picker"`
	Cheatsheet string `toml:"cheatsheet"`
	Editor     string `toml:"editor"`
	Adopt      string `toml:"adopt"`
	Term       string `toml:"term"`
	GotoPrefix string `toml:"goto_prefix"`
}

// Default is the configuration used for missing keys.
func Default() Config {
	return Config{
		ClaudeCommand: "claude",
		SidebarWidth:  32,
		Notify: Notify{Enabled: true, OnWaiting: true, OnError: true, OnDone: true,
			DoneMinDuration: "10s", Cooldown: "20s", Sound: "default"},
		Editor: Editor{RunIn: "background", Target: "cwd"},
		Popup: Popup{Hotkey: "cmd+ctrl+o", ReadHotkey: "cmd+ctrl+p", Keys: PopupKeys{Prev: "cmd+ctrl+left", Next: "cmd+ctrl+right",
			Play: "cmd+ctrl+p", Skip: "cmd+ctrl+k", Close: "escape"}},
		Speech: Speech{Voice: "af_heart:0.6,af_bella:0.4", Speed: 1.0},
		Keys: Keys{
			Toggle: "cmd+shift+b", New: "cmd+shift+n", Focus: "cmd+shift+s", Attention: "cmd+shift+a",
			Picker: "cmd+shift+l", Cheatsheet: "cmd+shift+k", Editor: "cmd+shift+e", Adopt: "cmd+shift+m", Term: "cmd+shift+x", GotoPrefix: "ctrl+cmd",
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
const Template = `# agent-command (acmd) configuration. See: acmd help.

# Absolute path of the claude binary (resolved by ` + "`acmd init`" + `).
claude_command = "claude"
# Extra arguments for new sessions (passed to ` + "`claude`" + `).
claude_args = []
# Start sessions with Remote Control, so you can use them from claude.ai and
# the Claude app. Applies to sessions started or resumed after a change.
# Change with: acmd remote-control on|off
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

[keys]
# Global terminal shortcuts. Rerun ` + "`acmd init`" + ` after changing them.
toggle = "cmd+shift+b"
new = "cmd+shift+n"
focus = "cmd+shift+s"
attention = "cmd+shift+a"
picker = "cmd+shift+l"
cheatsheet = "cmd+shift+k"
editor = "cmd+shift+e"
adopt = "cmd+shift+m"
term = "cmd+shift+x"
goto_prefix = "ctrl+cmd"

[popup]
# The reply popup: finished turns, questions and permissions of sessions,
# shown while you're not in acmd. ` + "`acmd init`" + ` asks to turn it on.
enabled = false
# Read each item aloud when it's shown (needs speech: acmd speech install).
autoplay = false
# System-wide shortcut that reopens the popup.
hotkey = "cmd+ctrl+o"

[popup.keys]
prev = "cmd+ctrl+left"
next = "cmd+ctrl+right"
play = "cmd+ctrl+p"
skip = "cmd+ctrl+k"
close = "escape"

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
