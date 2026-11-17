package setup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/brettmostert/agent-command/internal/core"
)

// Antigravity's CLI (agy) reads hooks from a hooks.json whose top-level keys
// name hooks, each mapping events to handlers. acmd owns one named hook and
// never touches the others.

// AntigravityHookName is the named hook acmd registers.
const AntigravityHookName = "agent-command"

// AntigravityEvents are the agy events acmd registers. Not PreToolUse: its
// answer decides whether a tool may run.
var AntigravityEvents = []string{"SessionStart", "PreInvocation", "PostToolUse", "Stop"}

// antigravityGrouped are the events whose handlers sit in matcher groups.
var antigravityGrouped = map[string]bool{"PreToolUse": true, "PostToolUse": true}

// AntigravityHookTimeout is the per-hook timeout in seconds. agy waits for
// each hook before it goes on, and acmd's takes well under one.
const AntigravityHookTimeout = 5

// AntigravityConfigDir is where agy keeps its shared hooks.json.
func AntigravityConfigDir(home string) string { return filepath.Join(home, ".gemini", "config") }

// AntigravityHooksFile is the hooks file in Input's AntigravityDir.
func (in Input) AntigravityHooksFile() string { return filepath.Join(in.AntigravityDir, "hooks.json") }

// AntigravityHookCommand is the command agy runs for one event. agy wants a
// JSON answer from every hook: acmd prints `{}`, and the guard prints it when
// acmd has been removed (brew uninstall without acmd uninstall).
func AntigravityHookCommand(acmdPath, event string) string {
	q := shellQuote(acmdPath)
	return fmt.Sprintf("[ -x %s ] && %s hook %s%s || echo '{}' %s", q, q, core.AntigravityHookPrefix, event, HookMarker)
}

type agyHandler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type agyGroup struct {
	Matcher string       `json:"matcher"`
	Hooks   []agyHandler `json:"hooks"`
}

// utf8BOM starts files that some Windows editors save; agy accepts it.
var utf8BOM = []byte("\xef\xbb\xbf")

func parseAgyHooks(b []byte) (*object, error) {
	return parseObject(bytes.TrimPrefix(b, utf8BOM))
}

// AddAntigravityHooks registers acmd's named hook in a hooks.json, replacing an
// earlier one of acmd's and keeping every other named hook as it is. A hook of
// that name that acmd didn't write is an error rather than overwritten.
func AddAntigravityHooks(file []byte, acmdPath string) (out []byte, changed bool, err error) {
	root, err := parseAgyHooks(file)
	if err != nil {
		return nil, false, fmt.Errorf("hooks.json: %w", err)
	}
	if raw, ok := root.get(AntigravityHookName); ok && !agyOurs(raw) {
		return nil, false, fmt.Errorf("hooks.json already has a hook named %q that acmd didn't write", AntigravityHookName)
	}
	spec := newObject()
	for _, event := range AntigravityEvents {
		h := agyHandler{Type: "command", Command: AntigravityHookCommand(acmdPath, event), Timeout: AntigravityHookTimeout}
		var v any = []agyHandler{h}
		if antigravityGrouped[event] {
			v = []agyGroup{{Matcher: "*", Hooks: []agyHandler{h}}}
		}
		b, err := json.Marshal(v)
		if err != nil {
			return nil, false, err
		}
		spec.set(event, b)
	}
	sc, err := spec.compact()
	if err != nil {
		return nil, false, err
	}
	root.set(AntigravityHookName, sc)
	if out, err = root.pretty(); err != nil {
		return nil, false, err
	}
	return out, !sameJSON(file, out), nil
}

// RemoveAntigravityHooks removes acmd's named hook, and nothing else.
func RemoveAntigravityHooks(file []byte) (out []byte, changed bool, err error) {
	root, err := parseAgyHooks(file)
	if err != nil {
		return nil, false, fmt.Errorf("hooks.json: %w", err)
	}
	raw, ok := root.get(AntigravityHookName)
	if !ok || !agyOurs(raw) {
		return file, false, nil
	}
	root.del(AntigravityHookName)
	if out, err = root.pretty(); err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// AntigravityHookState is what a hooks.json says about acmd's hook.
type AntigravityHookState struct {
	Events   int      // how many of AntigravityEvents run acmd
	Paths    []string // the acmd binaries they run (sorted, distinct)
	Disabled bool     // the hook has "enabled": false
	Others   int      // other named hooks
}

// AntigravityHooks reads acmd's hook from a hooks.json.
func AntigravityHooks(file []byte) AntigravityHookState {
	var st AntigravityHookState
	root, err := parseAgyHooks(file)
	if err != nil {
		return st
	}
	st.Others = len(root.keys)
	raw, ok := root.get(AntigravityHookName)
	if !ok {
		return st
	}
	st.Others--
	var spec map[string]json.RawMessage
	if json.Unmarshal(raw, &spec) != nil {
		return st
	}
	var enabled bool
	if json.Unmarshal(spec["enabled"], &enabled) == nil && !enabled {
		st.Disabled = true
	}
	seen := map[string]bool{}
	for _, event := range AntigravityEvents {
		found := false
		for _, c := range agyCommands(event, spec[event]) {
			if !strings.HasSuffix(c, HookMarker) {
				continue
			}
			found = true
			// [ -x PATH ] && PATH hook antigravity:EVENT || echo '{}' # managed by acmd
			if argv, err := ParseCommand(c); err == nil && len(argv) > 2 && argv[0] == "[" && argv[1] == "-x" {
				seen[argv[2]] = true
			}
		}
		if found {
			st.Events++
		}
	}
	for p := range seen {
		st.Paths = append(st.Paths, p)
	}
	sort.Strings(st.Paths)
	return st
}

// agyOurs reports whether a named hook runs acmd.
func agyOurs(raw json.RawMessage) bool {
	var spec map[string]json.RawMessage
	if json.Unmarshal(raw, &spec) != nil {
		return false
	}
	for event, v := range spec {
		for _, c := range agyCommands(event, v) {
			if strings.HasSuffix(c, HookMarker) {
				return true
			}
		}
	}
	return false
}

// agyCommands lists the commands of an event's handlers, flat or in groups.
func agyCommands(event string, raw json.RawMessage) []string {
	var out []string
	if antigravityGrouped[event] {
		var groups []agyGroup
		if json.Unmarshal(raw, &groups) != nil {
			return nil
		}
		for _, g := range groups {
			for _, h := range g.Hooks {
				out = append(out, h.Command)
			}
		}
		return out
	}
	var handlers []agyHandler
	if json.Unmarshal(raw, &handlers) != nil {
		return nil
	}
	for _, h := range handlers {
		out = append(out, h.Command)
	}
	return out
}

// planAntigravity registers (add) or removes acmd's agy hooks. It adds them
// only when agy is installed. agy is optional, so a hooks.json acmd can't edit
// is a note, not a failed init.
func planAntigravity(p *Plan, in Input, add bool) error {
	if in.AntigravityDir == "" || (add && in.AgyPath == "") {
		return nil
	}
	path := in.AntigravityHooksFile()
	old, exists, err := readOptional(in.Read, path)
	if err != nil {
		return err
	}
	if !exists && !add {
		return nil
	}
	var out []byte
	if add {
		out, _, err = AddAntigravityHooks(old, in.AcmdPath)
	} else {
		out, _, err = RemoveAntigravityHooks(old)
	}
	if err != nil {
		p.Notes = append(p.Notes, fmt.Sprintf("%s was left alone (%v), so Antigravity sessions won't show their status", Tilde(path, in.Home), err))
		return nil
	}
	c := Change{Path: path, Exists: exists, Old: old, New: out, Mode: 0o644}
	kept := ""
	switch n := AntigravityHooks(old).Others; {
	case n == 1:
		kept = "; your 1 other named hook is kept"
	case n > 1:
		kept = fmt.Sprintf("; your %d other named hooks are kept", n)
	}
	switch {
	case c.Action() == "unchanged" && add:
		c.Summary = "acmd's Antigravity hooks are already registered"
	case c.Action() == "unchanged":
		c.Summary = "no acmd hooks registered for Antigravity"
	case add:
		c.Summary = fmt.Sprintf("register acmd's Antigravity hooks for %d events, as the hook %q%s", len(AntigravityEvents), AntigravityHookName, kept)
		c.Detail = []string{"command: " + AntigravityHookCommand(in.AcmdPath, "<Event>")}
	default:
		c.Summary = fmt.Sprintf("remove acmd's Antigravity hook %q%s", AntigravityHookName, kept)
	}
	if exists && c.Action() != "unchanged" && reformats(bytes.TrimPrefix(old, utf8BOM)) {
		c.Detail = append(c.Detail, "the file is re-indented with 2 spaces (content otherwise unchanged)")
	}
	p.Changes = append(p.Changes, c)
	return nil
}
