// Package keymap is the single registry of acmd's shortcuts (SPEC
// §8.3.1): global terminal shortcuts (from config [keys], minus the ones
// init skipped because of conflicts), sidebar keys and mouse actions. The
// cheat sheet (`acmd keys`, `?`, cmd+shift+k) is generated from it.
package keymap

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
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
	Section string `json:"section,omitempty"` // subsection within the sheet, e.g. "Groups"
	Key     string `json:"key"`               // as shown, e.g. "⌘⇧N" or "⌘⇧S → n"
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

// Sidebar subsections (Binding.Section), in the order the sheet lists
// them. Global and mouse keys have no subsections.
const (
	SecNavigate = "Navigate"
	SecSessions = "Sessions"
	SecGroups   = "Groups"
	SecFilter   = "Filter & sort"
	SecView     = "View"
)

// local are the sidebar's own keys and mouse actions (as implemented in
// internal/sidebar), in sheet order. Group-header keys are their own
// context (hidden while no groups exist) but share the sidebar's Groups
// subsection with the other grouping keys.
var local = []Binding{
	{"show", Sidebar, SecNavigate, "↵ 1–9", "show session", ""},
	{"move-cursor", Sidebar, SecNavigate, "↑↓ j k", "move", ""},
	{"attention", Sidebar, SecNavigate, "Tab", "next needing you", ""},
	{"back", Sidebar, SecNavigate, "Esc", "back to the session", ""},
	{"new", Sidebar, SecSessions, "n", "new session", ""},
	{"new-in", Sidebar, SecSessions, "N", "new session (same folder picker as n)", ""},
	{"rename", Sidebar, SecSessions, "r", "rename", ""},
	{"describe", Sidebar, SecSessions, "e", "describe", ""},
	{"open", Sidebar, SecSessions, "E", "open in editor", ""},
	{"term", Sidebar, SecSessions, "t", "session terminal: create · show/hide ($ = has one)", ""},
	{"mark", Sidebar, SecSessions, "space", "mark (bulk move/archive)", ""},
	{"finish", Sidebar, SecSessions, "x", "finish (stop; stays listed)", ""},
	{"archive", Sidebar, SecSessions, "a", "archive", ""},
	{"split-out", Sidebar, SecSessions, "s", "split out to a tab/window", ""},
	{"adopt", Sidebar, SecSessions, "M", "move your pane's session into acmd", ""},
	{"move", Sidebar, SecGroups, "m", "move to group", ""},
	{"view", Sidebar, SecGroups, "v", "groups ↔ list", ""},
	{"collapse", Groups, SecGroups, "space ← →", "collapse · expand", ""},
	{"focus-group", Groups, SecGroups, "f", "focus group (f/Esc leave)", ""},
	{"collapse-all", Groups, SecGroups, "z", "collapse/expand all", ""},
	{"reorder", Groups, SecGroups, "K J", "move group up · down", ""},
	{"delete-group", Groups, SecGroups, "D", "delete group", ""},
	{"filter", Sidebar, SecFilter, "/", "filter (g:group)", ""},
	{"archived", Sidebar, SecFilter, "A", "show archived", ""},
	{"sort", Sidebar, SecFilter, "o O", "sort · direction", ""},
	{"hide", Sidebar, SecView, "h", "hide sidebar", ""},
	{"compact", Sidebar, SecView, "c", "compact rows", ""},
	{"quit", Sidebar, SecView, "q", "close this view (sessions keep running)", ""},
	{"keys", Sidebar, SecView, "?", "shortcuts", ""},
	{"click", Mouse, "", "click", "select · collapse", ""},
	{"right-click", Mouse, "", "right-click", "menu", ""},
	{"wheel", Mouse, "", "wheel", "scroll", ""},
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

// Filter keeps bindings in context ("" = all) that match query (key,
// description or section, case-insensitive). groups=false drops group-only entries
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
		if q != "" && !strings.Contains(strings.ToLower(b.Key+" "+b.Desc+" "+b.Action+" "+b.Section), q) {
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

// section is one headed block of the sheet.
type section struct {
	heading string
	items   []Binding
}

// parent is the context a context's keys are listed under: group-header
// keys belong to the sidebar.
func parent(c string) string {
	if c == Groups {
		return Sidebar
	}
	return c
}

// Heading is the title b is listed under, e.g. "Global" or
// "Sidebar / Groups".
func Heading(b Binding) string {
	h := title(parent(b.Context))
	if b.Section != "" {
		h += " / " + b.Section
	}
	return h
}

// sections groups the bindings of the contexts in order under their
// headings: contexts in order, and within a context its headings in
// registry order. A heading shared by several contexts (Sidebar / Groups)
// comes with the first of them and lists the keys of all of them, in
// registry order.
func sections(bs []Binding, order []string) []section {
	var out []section
	at := map[string]int{}
	for _, c := range order {
		for _, b := range bs {
			if b.Context != c {
				continue
			}
			if _, ok := at[Heading(b)]; !ok {
				at[Heading(b)] = len(out)
				out = append(out, section{heading: Heading(b)})
			}
		}
	}
	for _, b := range bs {
		if i, ok := at[Heading(b)]; ok && slices.Contains(order, b.Context) {
			out[i].items = append(out[i].items, b)
		}
	}
	return out
}

// describe is b's description with why it's unbound, if it is.
func describe(b Binding) string {
	if b.Unbound != "" {
		return b.Desc + " (unbound: " + b.Unbound + ")"
	}
	return b.Desc
}

// clip cuts l to width columns (0 = unlimited).
func clip(l string, width int) string {
	if width > 0 && len([]rune(l)) > width {
		return string([]rune(l)[:width-1]) + "…"
	}
	return l
}

// Lines renders the sheet as plain lines of at most width columns (0 =
// unlimited), sections in order.
func Lines(bs []Binding, order []string, width int) []string {
	var out []string
	for _, sec := range sections(bs, order) {
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, clip("── "+sec.heading+" ──", width))
		kw := 0
		for _, b := range sec.items {
			kw = max(kw, len([]rune(b.Key)))
		}
		for _, b := range sec.items {
			out = append(out, clip(fmt.Sprintf("%-*s  %s", kw, b.Key, describe(b)), width))
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
		for _, sec := range sections(bs, order) {
			fmt.Fprintf(w, "\n## %s\n\n| Key | Action |\n|---|---|\n", sec.heading)
			for _, b := range sec.items {
				fmt.Fprintf(w, "| `%s` | %s |\n", strings.ReplaceAll(b.Key, "|", "\\|"), describe(b))
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
