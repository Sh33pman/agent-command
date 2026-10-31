// Package setup holds the side-effect-free config editors behind `acmd
// init` and `acmd uninstall` (SPEC §9): Claude Code hooks in settings.json,
// Kitty's managed config, keybinding conflict detection and the login-shell
// environment. Callers do the file I/O, backups and confirmation.
package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// HookEvents are the Claude Code hook events acmd registers (SPEC §9.1).
var HookEvents = []string{
	"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure",
	"PermissionRequest", "Notification", "Stop", "StopFailure", "SubagentStart", "SubagentStop",
	"PreCompact", "PostCompact", "CwdChanged", "SessionEnd",
}

// HookMarker identifies acmd's hook entries (JSON has no comments, so the
// marker is a trailing shell comment in the command).
const HookMarker = "# managed by acmd"

// HookTimeout is the per-hook timeout in seconds (SPEC §9.1).
const HookTimeout = 2

// HookCommand is the command Claude runs for one event. The guard means a
// removed binary (brew uninstall without acmd uninstall) never makes Claude
// show hook errors.
func HookCommand(acmdPath, event string) string {
	q := shellQuote(acmdPath)
	return fmt.Sprintf("[ -x %s ] && %s hook %s || true %s", q, q, event, HookMarker)
}

type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type hookGroup struct {
	Hooks []hookEntry `json:"hooks"`
}

// AddHooks registers acmd's hooks in a Claude settings.json, preserving
// everything else (key order included). It's idempotent, and updates the
// binary path of existing acmd entries. changed reports whether the
// content changed.
func AddHooks(settings []byte, acmdPath string) (out []byte, changed bool, err error) {
	root, err := parseObject(settings)
	if err != nil {
		return nil, false, fmt.Errorf("settings.json: %w", err)
	}
	hooks := newObject()
	if raw, ok := root.get("hooks"); ok {
		if hooks, err = parseObject(raw); err != nil {
			return nil, false, fmt.Errorf("settings.json hooks: %w", err)
		}
	}
	for _, event := range HookEvents {
		var groups []json.RawMessage
		if raw, ok := hooks.get(event); ok {
			if err := json.Unmarshal(raw, &groups); err != nil {
				return nil, false, fmt.Errorf("settings.json hooks.%s: %w", event, err)
			}
		}
		ours, err := json.Marshal(hookGroup{Hooks: []hookEntry{{Type: "command", Command: HookCommand(acmdPath, event), Timeout: HookTimeout}}})
		if err != nil {
			return nil, false, err
		}
		replaced := false
		for i, g := range groups {
			if isOurs(g) {
				groups[i], replaced = ours, true
			}
		}
		if !replaced {
			groups = append(groups, ours)
		}
		b, err := json.Marshal(groups)
		if err != nil {
			return nil, false, err
		}
		hooks.set(event, b)
	}
	hc, err := hooks.compact()
	if err != nil {
		return nil, false, err
	}
	root.set("hooks", hc)
	out, err = root.pretty()
	if err != nil {
		return nil, false, err
	}
	return out, !sameJSON(settings, out), nil
}

// RemoveHooks removes acmd's hook entries (and only those), dropping event
// lists and the hooks object when they become empty.
func RemoveHooks(settings []byte) (out []byte, changed bool, err error) {
	root, err := parseObject(settings)
	if err != nil {
		return nil, false, fmt.Errorf("settings.json: %w", err)
	}
	raw, ok := root.get("hooks")
	if !ok {
		return settings, false, nil
	}
	hooks, err := parseObject(raw)
	if err != nil {
		return nil, false, fmt.Errorf("settings.json hooks: %w", err)
	}
	for _, event := range append([]string(nil), hooks.keys...) {
		var groups []json.RawMessage
		if err := json.Unmarshal(hooks.vals[event], &groups); err != nil {
			continue // not a list: not ours to touch
		}
		var keep []json.RawMessage
		for _, g := range groups {
			if !isOurs(g) {
				keep = append(keep, g)
			}
		}
		if len(keep) == len(groups) {
			continue
		}
		if len(keep) == 0 {
			hooks.del(event)
			continue
		}
		b, err := json.Marshal(keep)
		if err != nil {
			return nil, false, err
		}
		hooks.set(event, b)
	}
	if hooks.empty() {
		root.del("hooks")
	} else {
		hc, err := hooks.compact()
		if err != nil {
			return nil, false, err
		}
		root.set("hooks", hc)
	}
	out, err = root.pretty()
	if err != nil {
		return nil, false, err
	}
	return out, !sameJSON(settings, out), nil
}

// HasHooks reports whether every acmd hook is registered with acmdPath.
func HasHooks(settings []byte, acmdPath string) bool {
	root, err := parseObject(settings)
	if err != nil {
		return false
	}
	raw, ok := root.get("hooks")
	if !ok {
		return false
	}
	hooks, err := parseObject(raw)
	if err != nil {
		return false
	}
	for _, event := range HookEvents {
		var groups []hookGroup
		if json.Unmarshal(hooks.vals[event], &groups) != nil {
			return false
		}
		found := false
		for _, g := range groups {
			for _, h := range g.Hooks {
				if h.Command == HookCommand(acmdPath, event) {
					found = true
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// isOurs reports whether a hook group contains an acmd entry.
func isOurs(group json.RawMessage) bool {
	var g hookGroup
	if json.Unmarshal(group, &g) != nil {
		return false
	}
	for _, h := range g.Hooks {
		if strings.HasSuffix(h.Command, HookMarker) {
			return true
		}
	}
	return false
}

// sameJSON reports whether a and b are the same JSON (ignoring whitespace).
func sameJSON(a, b []byte) bool {
	var ca, cb bytes.Buffer
	if len(bytes.TrimSpace(a)) == 0 {
		a = []byte("{}")
	}
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return false
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}

// shellQuote single-quotes s for /bin/sh when it contains anything unsafe.
func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !shellSafe(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellSafe(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune("/._-+:@%", r)
}
