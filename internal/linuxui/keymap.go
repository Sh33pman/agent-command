//go:build linux

package linuxui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/brettmostert/agent-command/internal/paths"
)

// ModMask represents modifier keys for configurable Linux UI shortcuts.
type ModMask uint8

const (
	ModCtrl ModMask = 1 << iota
	ModAlt
	ModShift
	ModSuper
)

// Action names for configurable Linux UI keybindings.
const (
	ActionNewSession  = "new_session"
	ActionNewTerminal = "new_terminal"
	ActionFilter      = "filter"
	ActionCopy        = "copy"
	ActionPaste       = "paste"
	ActionQuit        = "quit"
)

// ActionSessionN returns the action name for jumping to numbered session n (1..9).
func ActionSessionN(n int) string {
	return fmt.Sprintf("session_%d", n)
}

// KeyCombo is a normalized modifier mask + lowercase key name (e.g. "n", "1", "return").
type KeyCombo struct {
	Mods ModMask
	Key  string
}

// ParseKeyCombo parses strings like "ctrl+shift+n" or "alt+1".
func ParseKeyCombo(text string) (KeyCombo, bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(text)), "+")
	if len(parts) == 0 {
		return KeyCombo{}, false
	}
	key := strings.TrimSpace(parts[len(parts)-1])
	if key == "" {
		return KeyCombo{}, false
	}
	var mods ModMask
	for _, p := range parts[:len(parts)-1] {
		switch strings.TrimSpace(p) {
		case "ctrl", "control":
			mods |= ModCtrl
		case "alt", "opt", "option", "meta":
			mods |= ModAlt
		case "shift":
			mods |= ModShift
		case "super", "win", "cmd", "command":
			mods |= ModSuper
		default:
			return KeyCombo{}, false
		}
	}
	return KeyCombo{Mods: mods, Key: normalizeKeyName(key)}, true
}

func normalizeKeyName(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	switch k {
	case "kp_enter", "enter":
		return "return"
	case "esc":
		return "escape"
	default:
		return k
	}
}

// String returns the canonical config form, e.g. "ctrl+shift+n".
func (k KeyCombo) String() string {
	var parts []string
	if k.Mods&ModCtrl != 0 {
		parts = append(parts, "ctrl")
	}
	if k.Mods&ModAlt != 0 {
		parts = append(parts, "alt")
	}
	if k.Mods&ModShift != 0 {
		parts = append(parts, "shift")
	}
	if k.Mods&ModSuper != 0 {
		parts = append(parts, "super")
	}
	parts = append(parts, k.Key)
	return strings.Join(parts, "+")
}

// Display returns a human-readable label like "Ctrl+Shift+N".
func (k KeyCombo) Display() string {
	var parts []string
	if k.Mods&ModCtrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if k.Mods&ModAlt != 0 {
		parts = append(parts, "Alt")
	}
	if k.Mods&ModShift != 0 {
		parts = append(parts, "Shift")
	}
	if k.Mods&ModSuper != 0 {
		parts = append(parts, "Super")
	}
	parts = append(parts, strings.ToUpper(k.Key))
	return strings.Join(parts, "+")
}

type actionMeta struct {
	action string
	title  string
	def    string
}

var defaultActions = []actionMeta{
	{ActionNewSession, "New Session", "ctrl+shift+n"},
	{ActionNewTerminal, "New Terminal", "ctrl+shift+t"},
	{ActionFilter, "Filter Sessions", "ctrl+shift+f"},
	{ActionCopy, "Copy", "ctrl+shift+c"},
	{ActionPaste, "Paste", "ctrl+shift+v"},
	{ActionQuit, "Quit", "ctrl+q"},
	{ActionSessionN(1), "Session 1", "alt+1"},
	{ActionSessionN(2), "Session 2", "alt+2"},
	{ActionSessionN(3), "Session 3", "alt+3"},
	{ActionSessionN(4), "Session 4", "alt+4"},
	{ActionSessionN(5), "Session 5", "alt+5"},
	{ActionSessionN(6), "Session 6", "alt+6"},
	{ActionSessionN(7), "Session 7", "alt+7"},
	{ActionSessionN(8), "Session 8", "alt+8"},
	{ActionSessionN(9), "Session 9", "alt+9"},
}

// Keymap maps action names to configurable KeyCombos.
type Keymap struct {
	bindings map[string]KeyCombo
}

// DefaultKeymap returns the default Linux UI keybindings.
func DefaultKeymap() *Keymap {
	m := &Keymap{bindings: make(map[string]KeyCombo, len(defaultActions))}
	for _, a := range defaultActions {
		combo, _ := ParseKeyCombo(a.def)
		m.bindings[a.action] = combo
	}
	return m
}

// KeymapPath returns the default user keymap config path (~/.config/agent-command/linux-keys.json).
func KeymapPath() (string, error) {
	p, err := paths.Default()
	if err != nil {
		return "", err
	}
	return filepath.Join(p.ConfigDir, "linux-keys.json"), nil
}

// LoadKeymap loads overrides from path (if it exists) on top of DefaultKeymap.
func LoadKeymap(path string) (*Keymap, error) {
	m := DefaultKeymap()
	if path == "" {
		return m, nil
	}
	b, err := os.ReadFile(path) // #nosec G304 -- user keymap config path
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return m, nil
		}
		return m, err
	}
	var raw map[string]string
	if err := json.Unmarshal(b, &raw); err != nil {
		return m, fmt.Errorf("parse keymap %s: %w", path, err)
	}
	for action, spec := range raw {
		if spec == "" {
			continue
		}
		if err := m.Set(action, spec); err != nil {
			return m, err
		}
	}
	return m, nil
}

// Save writes the current bindings to path as JSON.
func (m *Keymap) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	out := make(map[string]string, len(m.bindings))
	for k, v := range m.bindings {
		out[k] = v.String()
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// Set updates an action's keybinding.
func (m *Keymap) Set(action, spec string) error {
	combo, ok := ParseKeyCombo(spec)
	if !ok {
		return fmt.Errorf("invalid keybinding %q for %s", spec, action)
	}
	m.bindings[action] = combo
	return nil
}

// Get returns the KeyCombo bound to action.
func (m *Keymap) Get(action string) (KeyCombo, bool) {
	c, ok := m.bindings[action]
	return c, ok
}

// Match returns the action bound to (mods, key), if any.
func (m *Keymap) Match(mods ModMask, key string) (string, bool) {
	norm := normalizeKeyName(key)
	for _, a := range defaultActions {
		if c, ok := m.bindings[a.action]; ok && c.Mods == mods && c.Key == norm {
			return a.action, true
		}
	}
	for action, c := range m.bindings {
		if c.Mods == mods && c.Key == norm {
			return action, true
		}
	}
	return "", false
}

// DaemonPayload builds the OpKeysSet payload so `acmd keys` lists Linux UI shortcuts.
func (m *Keymap) DaemonPayload() map[string]any {
	type item struct {
		Action   string `json:"action"`
		Title    string `json:"title"`
		Shortcut string `json:"shortcut"`
	}
	items := make([]item, 0, len(defaultActions))
	for _, a := range defaultActions {
		c := m.bindings[a.action]
		items = append(items, item{
			Action:   a.action,
			Title:    a.title,
			Shortcut: c.Display(),
		})
	}
	return map[string]any{
		"sections": []map[string]any{
			{
				"title": "Linux UI",
				"items": items,
			},
		},
	}
}
