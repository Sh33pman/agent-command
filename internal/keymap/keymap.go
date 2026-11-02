// Package keymap is the single registry of acmd's shortcuts (SPEC
// §8.3.1): global terminal shortcuts (from config [keys], minus the ones
// init skipped because of conflicts), sidebar keys and mouse actions. The
// cheat sheet (`acmd keys`, `?`, cmd+shift+k) is generated from it.
package keymap

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/brettmostert/agent-command/internal/config"
)

// Contexts, in the order the sheet lists them by default.
const (
	Global  = "global"
	Sidebar = "sidebar"
	Groups  = "groups"
	Mouse   = "mouse"
)

// Contexts lists all contexts.
var Contexts = []string{Global, Sidebar, Groups, Mouse}

// Binding is one shortcut.
type Binding struct {
	Action  string `json:"action"`
	Context string `json:"context"`
	Key     string `json:"key"` // as shown, e.g. "⌘⇧N" or "⌘⇧S → n"
	Desc    string `json:"description"`
	Unbound string `json:"unbound,omitempty"` // why init didn't bind it
}

// global is a global action and its config key. The combo is the same
// in Kitty and Ghostty (in Ghostty via the sidebar, codes.go).
type global struct {
	action, desc string
	key          func(config.Keys) string
}

var globals = []global{
	{"new", "new session", func(k config.Keys) string { return k.New }},
	{"toggle", "hide/show sidebar", func(k config.Keys) string { return k.Toggle }},
	{"focus", "focus sidebar", func(k config.Keys) string { return k.Focus }},
	{"next-attention", "next needing you", func(k config.Keys) string { return k.Attention }},
	{"pick", "session picker", func(k config.Keys) string { return k.Picker }},
	{"open", "open in editor", func(k config.Keys) string { return k.Editor }},
	{"keys", "shortcuts (this sheet)", func(k config.Keys) string { return k.Cheatsheet }},
	{"adopt", "move this pane's session into acmd", func(k config.Keys) string { return k.Adopt }},
	{"term", "session terminal: create · show/hide", func(k config.Keys) string { return k.Term }},
	{"go", "show session N", func(k config.Keys) string {
		if k.GotoPrefix == "" {
			return ""
		}
		return k.GotoPrefix + "+1–9"
	}},
}

// local are the sidebar's own keys and mouse actions (as implemented in
// internal/sidebar).
var local = []Binding{
	{"show", Sidebar, "↵ 1–9", "show session", ""},
	{"move-cursor", Sidebar, "↑↓ j k", "move", ""},
	{"new", Sidebar, "n", "new session", ""},
	{"new-in", Sidebar, "N", "new session (same folder picker as n)", ""},
	{"rename", Sidebar, "r", "rename", ""},
	{"describe", Sidebar, "e", "describe", ""},
	{"open", Sidebar, "E", "open in editor", ""},
	{"move", Sidebar, "m", "move to group", ""},
	{"split-out", Sidebar, "s", "split out to a tab/window", ""},
	{"adopt", Sidebar, "M", "move your pane's session into acmd", ""},
	{"mark", Sidebar, "space", "mark (bulk move/archive)", ""},
	{"finish", Sidebar, "x", "finish (stop; stays listed)", ""},
	{"archive", Sidebar, "a", "archive", ""},
	{"archived", Sidebar, "A", "show archived", ""},
	{"filter", Sidebar, "/", "filter (g:group)", ""},
	{"view", Sidebar, "v", "groups ↔ list", ""},
	{"sort", Sidebar, "o O", "sort · direction", ""},
	{"attention", Sidebar, "Tab", "next needing you", ""},
	{"hide", Sidebar, "h", "hide sidebar", ""},
	{"term", Sidebar, "t", "session terminal: create · show/hide ($ = has one)", ""},
	{"quit", Sidebar, "q", "close this view (sessions keep running)", ""},
	{"compact", Sidebar, "c", "compact rows", ""},
	{"back", Sidebar, "Esc", "back to the session", ""},
	{"keys", Sidebar, "?", "shortcuts", ""},
	{"collapse", Groups, "space ← →", "collapse · expand", ""},
	{"focus-group", Groups, "f", "focus group (f/Esc leave)", ""},
	{"collapse-all", Groups, "z", "collapse/expand all", ""},
	{"reorder", Groups, "K J", "move group up · down", ""},
	{"delete-group", Groups, "D", "delete group", ""},
	{"click", Mouse, "click", "select · collapse", ""},
	{"right-click", Mouse, "right-click", "menu", ""},
	{"wheel", Mouse, "wheel", "scroll", ""},
}

// Registry builds the bindings for a terminal ("kitty" or "ghostty"; the
// keys are the same). skipped maps actions to the reason init didn't bind
// them in that terminal.
func Registry(keys config.Keys, _ string, skipped map[string]string) []Binding {
	var out []Binding
	for _, g := range globals {
		b := Binding{Action: g.action, Context: Global, Key: Pretty(g.key(keys)), Desc: g.desc}
		if reason, ok := skipped[g.action]; ok {
			b.Unbound = reason
		}
		if b.Key == "" {
			b.Unbound = "no key configured"
		}
		out = append(out, b)
	}
	return append(out, local...)
}

// Filter keeps bindings in context ("" = all) that match query (key or
// description, case-insensitive). groups=false drops group-only entries
// (no groups yet: the sidebar shows no headers).
func Filter(bs []Binding, context, query string, groups bool) []Binding {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []Binding
	for _, b := range bs {
		if context != "" && b.Context != context {
			continue
		}
		if !groups && b.Context == Groups {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(b.Key+" "+b.Desc+" "+b.Action), q) {
			continue
		}
		out = append(out, b)
	}
	return out
}

// Ordered returns contexts with first first.
func Ordered(first string) []string {
	out := []string{}
	if first != "" {
		out = append(out, first)
	}
	for _, c := range Contexts {
		if c != first {
			out = append(out, c)
		}
	}
	return out
}

var mods = map[string]string{"cmd": "⌘", "super": "⌘", "shift": "⇧", "ctrl": "⌃", "control": "⌃", "alt": "⌥", "opt": "⌥", "option": "⌥"}

// modOrder is the order modifier symbols are shown in, as in the SPEC's
// sheet (⌘⇧N, ⌃⌘1). macOS menus use ⌃⌥⇧⌘ instead.
var modOrder = []string{"⌃", "⌥", "⌘", "⇧"}

// Pretty renders "cmd+shift+n" as "⌘⇧N" and "ctrl+cmd+1–9" as "⌃⌘1–9".
func Pretty(key string) string {
	if key == "" {
		return ""
	}
	parts := strings.Split(key, "+")
	have := map[string]bool{}
	var rest []string
	for _, p := range parts {
		if m, ok := mods[strings.ToLower(p)]; ok {
			have[m] = true
		} else {
			rest = append(rest, p)
		}
	}
	var b strings.Builder
	for _, m := range modOrder {
		if have[m] {
			b.WriteString(m)
		}
	}
	k := strings.Join(rest, "+")
	if len(k) == 1 {
		k = strings.ToUpper(k)
	}
	return b.String() + k
}

// Lines renders the sheet as plain lines of at most width columns (0 =
// unlimited), sections in order.
func Lines(bs []Binding, order []string, width int) []string {
	var out []string
	for _, c := range order {
		var sec []Binding
		for _, b := range bs {
			if b.Context == c {
				sec = append(sec, b)
			}
		}
		if len(sec) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, "── "+title(c)+" ──")
		kw := 0
		for _, b := range sec {
			kw = max(kw, len([]rune(b.Key)))
		}
		for _, b := range sec {
			desc := b.Desc
			if b.Unbound != "" {
				desc += " (unbound: " + b.Unbound + ")"
			}
			l := fmt.Sprintf("%-*s  %s", kw, b.Key, desc)
			if width > 0 && len([]rune(l)) > width {
				l = string([]rune(l)[:width-1]) + "…"
			}
			out = append(out, l)
		}
	}
	return out
}

func title(c string) string { return strings.ToUpper(c[:1]) + c[1:] }

// Write prints the sheet as text, markdown or json.
func Write(w io.Writer, bs []Binding, order []string, format, terminal string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if bs == nil {
			bs = []Binding{}
		}
		return enc.Encode(bs)
	case "markdown":
		fmt.Fprintf(w, "# acmd shortcuts (%s)\n", terminal)
		for _, c := range order {
			first := true
			for _, b := range bs {
				if b.Context != c {
					continue
				}
				if first {
					fmt.Fprintf(w, "\n## %s\n\n| Key | Action |\n|---|---|\n", title(c))
					first = false
				}
				desc := b.Desc
				if b.Unbound != "" {
					desc += " (unbound: " + b.Unbound + ")"
				}
				fmt.Fprintf(w, "| `%s` | %s |\n", strings.ReplaceAll(b.Key, "|", "\\|"), desc)
			}
		}
		return nil
	case "text", "":
		fmt.Fprintf(w, "Shortcuts · %s\n\n", title(terminal))
		for _, l := range Lines(bs, order, 0) {
			fmt.Fprintln(w, l)
		}
		return nil
	}
	return fmt.Errorf("--format must be text, markdown or json, not %q", format)
}
